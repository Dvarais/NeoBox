package service

import (
	"fmt"
	"sync"
	"time"
)

// Every Go -> JS message in Wails v2 is delivered as a separately compiled
// script: EventsEmit ends up in Frontend.Notify, which hands
// `window.wails.EventsNotify('<payload>')` to ICoreWebView2.ExecuteScript.
// Bound-method results take the same route. There is no cheaper transport, so
// the only lever on renderer memory is how many messages we send.
//
// That matters because sing-box logs the open and the close of every single
// connection at "info" level. Emitting one event per line meant an ordinary
// browsing session pushed hundreds of unique scripts per second into WebView2,
// and its renderer process grew without bound — 45 MB to over 1.7 GB within a
// single session.
//
// logStreamer fixes that at the source: lines land in a bounded ring buffer and
// leave in one batched event a few times per second, which is two orders of
// magnitude fewer ExecuteScript calls while the user still sees every line.
const (
	// maxBufferedLogLines caps the buffer. It matches MAX_LOG_ENTRIES in
	// renderer.js: the UI never shows more than this, so holding more here
	// would only be memory we are about to throw away anyway.
	maxBufferedLogLines = 500

	// logFlushInterval bounds how often the frontend is woken. Four batches a
	// second still reads as live output to a human.
	logFlushInterval = 250 * time.Millisecond
)

// logStreamer buffers sing-box log lines and delivers them to the frontend in
// batches. It satisfies sing-box's log.PlatformWriter.
type logStreamer struct {
	mu      sync.Mutex
	buf     []string
	dropped int

	// emit and visible are injected rather than taking an *AppService so the
	// streamer can be tested without a Wails context.
	emit    func(event string, data ...interface{})
	visible func() bool

	stop     chan struct{}
	stopOnce sync.Once
}

// newLogStreamer starts the flush loop. Call stop() when the core session ends.
func newLogStreamer(emit func(event string, data ...interface{}), visible func() bool) *logStreamer {
	ls := &logStreamer{
		buf:     make([]string, 0, 64),
		emit:    emit,
		visible: visible,
		stop:    make(chan struct{}),
	}
	go ls.run()
	return ls
}

// WriteMessage is called by sing-box from its own goroutines, once per log
// line. It must stay cheap: no formatting, no IPC, just an append.
func (l *logStreamer) WriteMessage(level uint8, message string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.buf) >= maxBufferedLogLines {
		// Drop the oldest line rather than the newest: the tail is what the
		// user is watching. Count the loss so the batch can say so.
		copy(l.buf, l.buf[1:])
		l.buf = l.buf[:len(l.buf)-1]
		l.dropped++
	}
	l.buf = append(l.buf, message)
}

func (l *logStreamer) run() {
	ticker := time.NewTicker(logFlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-l.stop:
			// Final flush so the tail of a session is not lost.
			l.flush()
			return
		case <-ticker.C:
			// While the window is hidden nothing can read the output, so
			// sending it would only keep WebView2 awake. Lines keep collecting
			// in the bounded buffer and go out in one batch on restore.
			if l.visible == nil || l.visible() {
				l.flush()
			}
		}
	}
}

// flush sends everything buffered as a single event. It is safe to call from
// any goroutine and does nothing when there is nothing to send.
func (l *logStreamer) flush() {
	l.mu.Lock()
	if len(l.buf) == 0 && l.dropped == 0 {
		l.mu.Unlock()
		return
	}
	lines := l.buf
	dropped := l.dropped
	l.buf = make([]string, 0, 64)
	l.dropped = 0
	l.mu.Unlock()

	if dropped > 0 {
		// Say it out loud instead of silently showing a gap in the log.
		notice := fmt.Sprintf("WARN [NeoBox] %d log lines dropped (output faster than the UI can consume)", dropped)
		lines = append([]string{notice}, lines...)
	}

	if l.emit != nil {
		l.emit("xray-log-batch", lines)
	}
}

// stopStreaming ends the flush loop after one final flush. Safe to call twice.
func (l *logStreamer) stopStreaming() {
	l.stopOnce.Do(func() { close(l.stop) })
}
