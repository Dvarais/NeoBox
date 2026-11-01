package service

import (
	"fmt"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"

	sclog "github.com/sagernet/sing-box/log"
)

// Every Go -> JS message in Wails v2 is delivered as a separately compiled
// script: EventsEmit ends up in Frontend.Notify, which hands
// `window.wails.EventsNotify('<payload>')` to ICoreWebView2.ExecuteScript.
// Bound-method results take the same route. There is no cheaper transport, so
// the only lever on renderer memory is how many messages we send.
//
// logStreamer buffers sing-box log lines and delivers them to the frontend in
// batches. It satisfies sing-box's log.PlatformWriter.
const (
	// maxBufferedLogLines caps the buffer.
	maxBufferedLogLines = 2000

	// logFlushInterval bounds how often the frontend is woken. Four batches a
	// second still reads as live output to a human.
	logFlushInterval = 250 * time.Millisecond
)

// LogEntry represents a structured, pre-parsed log record delivered to Wails.
type LogEntry struct {
	Timestamp int64  `json:"t"`                   // Unix timestamp in milliseconds
	Level     string `json:"level"`               // "DEBUG", "INFO", "WARN", "ERROR"
	Category  string `json:"category"`            // "dns", "router", "inbound", "outbound", "system"
	Component string `json:"component,omitempty"` // e.g. "inbound/tun[tun-in]"
	Message   string `json:"message"`             // Clean payload
	Raw       string `json:"raw"`                 // Original raw line for exports
}

// stripANSI removes terminal ANSI color escape sequences without regex allocations.
func stripANSI(s string) string {
	if !strings.Contains(s, "\x1b[") {
		return s
	}
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			i += 2
			for i < len(s) && !(s[i] >= 0x40 && s[i] <= 0x7e) {
				i++
			}
			continue
		}
		b = append(b, s[i])
	}
	return string(b)
}

// fastScanLog performs zero-regex, fast string extraction for sing-box lines.
func fastScanLog(level uint8, raw string) LogEntry {
	now := time.Now().UnixMilli()
	var lvlStr string
	switch sclog.Level(level) {
	case sclog.LevelPanic, sclog.LevelFatal, sclog.LevelError:
		lvlStr = "ERROR"
	case sclog.LevelWarn:
		lvlStr = "WARN"
	case sclog.LevelInfo:
		lvlStr = "INFO"
	case sclog.LevelDebug:
		lvlStr = "DEBUG"
	case sclog.LevelTrace:
		lvlStr = "TRACE"
	default:
		lvlStr = "INFO"
	}

	cleanRaw := stripANSI(raw)
	trimmed := strings.TrimSpace(cleanRaw)
	msg := trimmed

	// Handle prefix like "INFO[0001] " or "WARN [NeoBox] "
	if strings.HasPrefix(msg, "INFO[") || strings.HasPrefix(msg, "WARN[") ||
		strings.HasPrefix(msg, "ERROR[") || strings.HasPrefix(msg, "DEBUG[") ||
		strings.HasPrefix(msg, "FATAL[") || strings.HasPrefix(msg, "PANIC[") ||
		strings.HasPrefix(msg, "WARN [") || strings.HasPrefix(msg, "INFO [") ||
		strings.HasPrefix(msg, "ERROR [") {
		if bracketIdx := strings.IndexByte(msg, ']'); bracketIdx > 0 {
			prefix := msg[:bracketIdx]
			if strings.Contains(prefix, "ERROR") || strings.Contains(prefix, "FATAL") {
				lvlStr = "ERROR"
			} else if strings.Contains(prefix, "WARN") {
				lvlStr = "WARN"
			} else if strings.Contains(prefix, "DEBUG") {
				lvlStr = "DEBUG"
			}
			msg = strings.TrimSpace(msg[bracketIdx+1:])
		}
	}

	// Extract session tag prefix if present: "[3542283088 0ms] "
	if strings.HasPrefix(msg, "[") {
		if endTag := strings.IndexByte(msg, ']'); endTag > 0 {
			msg = strings.TrimSpace(msg[endTag+1:])
		}
	}

	category := "system"
	component := ""

	switch {
	case strings.HasPrefix(msg, "dns:"):
		category = "dns"
		component = "dns"
	case strings.HasPrefix(msg, "router:"):
		category = "router"
		component = "router"
	case strings.HasPrefix(msg, "reference:"):
		category = "router"
		component = "reference"
		msg = strings.TrimSpace(strings.TrimPrefix(msg, "reference:"))
	case strings.HasPrefix(msg, "inbound/"):
		category = "inbound"
		if colon := strings.IndexByte(msg, ':'); colon > 0 {
			component = msg[:colon]
			msg = strings.TrimSpace(msg[colon+1:])
		}
	case strings.HasPrefix(msg, "outbound/"):
		category = "outbound"
		if colon := strings.IndexByte(msg, ':'); colon > 0 {
			component = msg[:colon]
			msg = strings.TrimSpace(msg[colon+1:])
		}
	default:
		if strings.Contains(msg, "connection opened") || strings.Contains(msg, "connection closed") {
			category = "router"
		}
	}

	return LogEntry{
		Timestamp: now,
		Level:     lvlStr,
		Category:  category,
		Component: component,
		Message:   msg,
		Raw:       trimmed,
	}
}

// circularLogBuffer implements an allocation-free O(1) circular ring buffer.
type circularLogBuffer struct {
	entries []LogEntry
	head    int
	count   int
	cap     int
}

func newCircularLogBuffer(capacity int) *circularLogBuffer {
	return &circularLogBuffer{
		entries: make([]LogEntry, capacity),
		cap:     capacity,
	}
}

func (c *circularLogBuffer) Push(entry LogEntry) bool {
	if c.count < c.cap {
		c.entries[(c.head+c.count)%c.cap] = entry
		c.count++
		return false
	}
	c.entries[c.head] = entry
	c.head = (c.head + 1) % c.cap
	return true
}

func (c *circularLogBuffer) Drain() []LogEntry {
	if c.count == 0 {
		return nil
	}
	out := make([]LogEntry, c.count)
	for i := 0; i < c.count; i++ {
		out[i] = c.entries[(c.head+i)%c.cap]
	}
	c.head = 0
	c.count = 0
	return out
}

// logStreamer buffers sing-box log lines and delivers them to the frontend in
// batches. It satisfies sing-box's log.PlatformWriter.
type logStreamer struct {
	mu      sync.Mutex
	ring    *circularLogBuffer
	dropped int

	emit    func(event string, data ...interface{})
	visible func() bool

	stop     chan struct{}
	stopOnce sync.Once
}

// newLogStreamer starts the flush loop. Call stop() when the core session ends.
func newLogStreamer(emit func(event string, data ...interface{}), visible func() bool) *logStreamer {
	ls := &logStreamer{
		ring:    newCircularLogBuffer(maxBufferedLogLines),
		emit:    emit,
		visible: visible,
		stop:    make(chan struct{}),
	}
	go ls.run()
	return ls
}

// WriteMessage is called by sing-box from its own goroutines, once per log line.
func (l *logStreamer) WriteMessage(level uint8, message string) {
	// Discard TRACE messages (packet-level XTLS padding/inspection spam) completely.
	if sclog.Level(level) == sclog.LevelTrace {
		return
	}

	entry := fastScanLog(level, message)

	l.mu.Lock()
	defer l.mu.Unlock()

	if dropped := l.ring.Push(entry); dropped {
		l.dropped++
	}
}

func (l *logStreamer) run() {
	ticker := time.NewTicker(logFlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-l.stop:
			l.flush()
			return
		case <-ticker.C:
			if l.visible == nil || l.visible() {
				l.flush()
			}
		}
	}
}

// flush sends everything buffered as a single event.
func (l *logStreamer) flush() {
	l.mu.Lock()
	if l.ring.count == 0 && l.dropped == 0 {
		l.mu.Unlock()
		return
	}
	entries := l.ring.Drain()
	dropped := l.dropped
	l.dropped = 0
	l.mu.Unlock()

	if dropped > 0 {
		notice := fastScanLog(4, fmt.Sprintf("WARN [NeoBox] %d log lines dropped (output faster than the UI can consume)", dropped))
		entries = append([]LogEntry{notice}, entries...)
	}

	if l.emit != nil {
		l.emit("xray-log-batch", entries)
	}
}

// stopStreaming ends the flush loop after one final flush. Safe to call twice.
func (l *logStreamer) stopStreaming() {
	l.stopOnce.Do(func() { close(l.stop) })
}

var (
	reIPv4 = regexp.MustCompile(`\b([0-9]{1,3}\.[0-9]{1,3}\.)[0-9]{1,3}\.[0-9]{1,3}(:[0-9]+)?\b`)
	reUUID = regexp.MustCompile(`\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b`)
	reKey  = regexp.MustCompile(`(?i)(password|secret|key|uuid|token)=([^&\s]+)`)
)

// SanitizeLogText masks external IP addresses, private credentials, and UUIDs
// according to specification NBX-192-SEC.
func SanitizeLogText(content string) string {
	content = reUUID.ReplaceAllString(content, "[REDACTED-UUID]")
	content = reKey.ReplaceAllString(content, `$1=[REDACTED]`)
	content = reIPv4.ReplaceAllStringFunc(content, func(matched string) string {
		host, port, err := net.SplitHostPort(matched)
		if err != nil {
			host = matched
			port = ""
		}
		ip := net.ParseIP(host)
		if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
			return matched
		}
		parts := strings.Split(host, ".")
		if len(parts) != 4 {
			return matched
		}
		maskedHost := fmt.Sprintf("%s.%s.*.*", parts[0], parts[1])
		if port != "" {
			return maskedHost + ":" + port
		}
		return maskedHost
	})
	return content
}
