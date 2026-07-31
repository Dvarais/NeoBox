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
	batches [][]string
}

func (c *collector) emit(event string, data ...interface{}) {
	if event != "xray-log-batch" || len(data) == 0 {
		return
	}
	lines, ok := data[0].([]string)
	if !ok {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.batches = append(c.batches, lines)
}

func (c *collector) lines() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var all []string
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

// waitFor polls until cond holds or the deadline passes, so the timing-based
// assertions below do not depend on a fixed sleep being long enough.
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

	// Well under maxBufferedLogLines so nothing is dropped, but far more than
	// the number of events we are willing to push into WebView2.
	const total = 200
	for i := 0; i < total; i++ {
		ls.WriteMessage(0, fmt.Sprintf("line %d", i))
	}

	if !waitFor(t, 2*time.Second, func() bool { return len(c.lines()) == total }) {
		t.Fatalf("got %d lines, want %d", len(c.lines()), total)
	}

	// The whole point of the type: 200 lines must not become 200 events.
	if n := c.batchCount(); n > 3 {
		t.Errorf("200 lines produced %d batches, want a handful", n)
	}

	got := c.lines()
	for i, line := range got {
		if want := fmt.Sprintf("line %d", i); line != want {
			t.Fatalf("line %d = %q, want %q (order must be preserved)", i, line, want)
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

	for i := 0; i < 10; i++ {
		ls.WriteMessage(0, fmt.Sprintf("hidden %d", i))
	}

	// Give the flush loop several ticks to prove it stays quiet.
	time.Sleep(4 * logFlushInterval)
	if n := c.batchCount(); n != 0 {
		t.Fatalf("emitted %d batches while hidden, want 0", n)
	}

	visible.Lock()
	visible.v = true
	visible.Unlock()

	if !waitFor(t, 2*time.Second, func() bool { return len(c.lines()) == 10 }) {
		t.Fatalf("after restore got %d lines, want 10", len(c.lines()))
	}
}

func TestLogStreamerDropsOldestAndSaysSo(t *testing.T) {
	c := &collector{}
	// Never visible, so everything piles up in the buffer and the cap is what
	// keeps memory bounded — the situation this type exists to survive.
	ls := newLogStreamer(c.emit, func() bool { return false })
	defer ls.stopStreaming()

	const overflow = 50
	for i := 0; i < maxBufferedLogLines+overflow; i++ {
		ls.WriteMessage(0, fmt.Sprintf("line %d", i))
	}

	ls.mu.Lock()
	buffered := len(ls.buf)
	dropped := ls.dropped
	ls.mu.Unlock()

	if buffered != maxBufferedLogLines {
		t.Errorf("buffered %d lines, want the cap of %d", buffered, maxBufferedLogLines)
	}
	if dropped != overflow {
		t.Errorf("dropped %d lines, want %d", dropped, overflow)
	}

	ls.flush()
	lines := c.lines()
	if len(lines) == 0 {
		t.Fatal("flush produced nothing")
	}
	if !strings.Contains(lines[0], "dropped") {
		t.Errorf("first line = %q, want a notice that lines were dropped", lines[0])
	}
	// The tail is what the user is watching, so that is what must survive.
	if last := lines[len(lines)-1]; last != fmt.Sprintf("line %d", maxBufferedLogLines+overflow-1) {
		t.Errorf("last line = %q, want the newest line", last)
	}
}

func TestLogStreamerStopFlushesTail(t *testing.T) {
	c := &collector{}
	ls := newLogStreamer(c.emit, func() bool { return true })

	ls.WriteMessage(0, "tail line")
	ls.stopStreaming()

	if !waitFor(t, 2*time.Second, func() bool { return len(c.lines()) == 1 }) {
		t.Fatalf("got %d lines after stop, want the buffered tail", len(c.lines()))
	}
	// Stopping twice must not panic on the closed channel.
	ls.stopStreaming()
}

func TestLogStreamerConcurrentWrites(t *testing.T) {
	c := &collector{}
	ls := newLogStreamer(c.emit, func() bool { return true })
	defer ls.stopStreaming()

	// sing-box writes from many goroutines at once; this is what -race checks.
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				ls.WriteMessage(0, fmt.Sprintf("g%d line %d", g, i))
			}
		}(g)
	}
	wg.Wait()

	if !waitFor(t, 2*time.Second, func() bool { return len(c.lines()) == 400 }) {
		t.Fatalf("got %d lines, want 400", len(c.lines()))
	}
}
