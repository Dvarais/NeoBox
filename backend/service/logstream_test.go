package service

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// collector captures batches emitted by a logStreamer.
type collector struct {
	mu      sync.Mutex
	batches [][]LogEntry
}

func (c *collector) emit(event string, data ...interface{}) {
	if event != "xray-log-batch" || len(data) == 0 {
		return
	}
	entries, ok := data[0].([]LogEntry)
	if !ok {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.batches = append(c.batches, entries)
}

func (c *collector) entries() []LogEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	var all []LogEntry
	for _, b := range c.batches {
		all = append(all, b...)
	}
	return all
}

func (c *collector) batchCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.batches)
}

// waitFor polls until cond holds or the deadline passes.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

func TestLogStreamerBatchesManyLinesIntoFewEvents(t *testing.T) {
	c := &collector{}
	ls := newLogStreamer(c.emit, func() bool { return true })
	defer ls.stopStreaming()

	const total = 200
	for i := 0; i < total; i++ {
		ls.WriteMessage(0, fmt.Sprintf("line %d", i))
	}

	if !waitFor(t, 2*time.Second, func() bool { return len(c.entries()) == total }) {
		t.Fatalf("got %d entries, want %d", len(c.entries()), total)
	}

	if n := c.batchCount(); n > 3 {
		t.Errorf("200 lines produced %d batches, want a handful", n)
	}

	got := c.entries()
	for i, entry := range got {
		if want := fmt.Sprintf("line %d", i); entry.Raw != want {
			t.Fatalf("line %d = %q, want %q (order must be preserved)", i, entry.Raw, want)
		}
	}
}

func TestLogStreamerHoldsLinesWhileWindowHidden(t *testing.T) {
	c := &collector{}
	var visible struct {
		sync.Mutex
		v bool
	}
	ls := newLogStreamer(c.emit, func() bool {
		visible.Lock()
		defer visible.Unlock()
		return visible.v
	})
	defer ls.stopStreaming()

	for i := 0; i < 50; i++ {
		ls.WriteMessage(0, fmt.Sprintf("hidden %d", i))
	}

	time.Sleep(4 * logFlushInterval)
	if n := len(c.entries()); n != 0 {
		t.Fatalf("emitted %d entries while window hidden, want 0", n)
	}

	visible.Lock()
	visible.v = true
	visible.Unlock()

	ls.flush()
	if n := len(c.entries()); n != 50 {
		t.Fatalf("flush produced %d entries on restore, want 50", n)
	}
}

func TestLogStreamerDropsOldestAndSaysSo(t *testing.T) {
	c := &collector{}
	ls := newLogStreamer(c.emit, func() bool { return false })
	defer ls.stopStreaming()

	const overflow = 50
	for i := 0; i < maxBufferedLogLines+overflow; i++ {
		ls.WriteMessage(0, fmt.Sprintf("line %d", i))
	}

	ls.mu.Lock()
	buffered := ls.ring.count
	dropped := ls.dropped
	ls.mu.Unlock()

	if buffered != maxBufferedLogLines {
		t.Errorf("buffered %d lines, want the cap of %d", buffered, maxBufferedLogLines)
	}
	if dropped != overflow {
		t.Errorf("dropped %d lines, want %d", dropped, overflow)
	}

	ls.flush()
	entries := c.entries()
	if len(entries) == 0 {
		t.Fatal("flush produced nothing")
	}
	if !strings.Contains(entries[0].Raw, "dropped") {
		t.Errorf("first entry = %q, want a notice that lines were dropped", entries[0].Raw)
	}
	if last := entries[len(entries)-1].Raw; last != fmt.Sprintf("line %d", maxBufferedLogLines+overflow-1) {
		t.Errorf("last entry = %q, want the newest line", last)
	}
}

func TestLogStreamerStopFlushesTail(t *testing.T) {
	c := &collector{}
	ls := newLogStreamer(c.emit, func() bool { return true })

	ls.WriteMessage(0, "tail line")
	ls.stopStreaming()

	if !waitFor(t, 2*time.Second, func() bool { return len(c.entries()) == 1 }) {
		t.Fatalf("got %d lines after stop, want the buffered tail", len(c.entries()))
	}
	ls.stopStreaming()
}

func TestFastScanLog(t *testing.T) {
	e1 := fastScanLog(3, "inbound/tun[tun-in]: connection to example.com:443")
	if e1.Category != "inbound" || e1.Component != "inbound/tun[tun-in]" {
		t.Errorf("e1 unexpected: %+v", e1)
	}

	e2 := fastScanLog(2, "dns: exchanged example.com A in 15ms")
	if e2.Category != "dns" || e2.Level != "DEBUG" {
		t.Errorf("e2 unexpected: %+v", e2)
	}

	e3 := fastScanLog(5, "router: match direct")
	if e3.Category != "router" || e3.Level != "ERROR" {
		t.Errorf("e3 unexpected: %+v", e3)
	}
}

func TestSanitizeLogText(t *testing.T) {
	raw := "outbound/vless to 185.220.101.5:443 with uuid=12345678-1234-1234-1234-123456789abc from 127.0.0.1"
	sanitized := SanitizeLogText(raw)
	if strings.Contains(sanitized, "185.220.101.5") {
		t.Errorf("IP not masked: %s", sanitized)
	}
	if !strings.Contains(sanitized, "185.220.*.*:443") {
		t.Errorf("masked IP format mismatch: %s", sanitized)
	}
	if strings.Contains(sanitized, "12345678-1234-1234-1234-123456789abc") {
		t.Errorf("UUID not masked: %s", sanitized)
	}
	if !strings.Contains(sanitized, "127.0.0.1") {
		t.Errorf("Loopback IP was incorrectly masked: %s", sanitized)
	}
}
