package service

import (
	"context"
	"fmt"
	"net"
	"os"
	"runtime/debug"
	"strconv"
	"time"

	"NeoBox/backend/core"
)

// Watchdog: detects a dropped tunnel and reconnects, with backoff.

// Watchdog tuning.
//
// The cooldown starts at watchdogInitialBackoff and doubles up to
// watchdogMaxBackoff after every recovery attempt that could not succeed, so a
// machine left without upstream (router down after a power cut, cable pulled)
// is not subjected to an endless 45-second cycle of core teardowns and rebuilds.
const (
	watchdogProbeInterval   = 15 * time.Second
	watchdogFailThreshold   = 3
	watchdogInitialBackoff  = 30 * time.Second
	watchdogMaxBackoff      = 5 * time.Minute
	watchdogUpstreamTimeout = 5 * time.Second
)

// startWatchdog probes the local SOCKS proxy port every 15 s.
// After 3 consecutive failures it restarts the VPN core — but only once the VPN
// server is actually reachable again, see the upstream check below.
func (s *AppService) startWatchdog(link string, useSystemProxy bool) {
	// This goroutine drives core restarts, so a panic in it would take the whole
	// application down (the sing-box core shares this process). Record it rather
	// than letting the window disappear silently.
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "[watchdog] recovered from panic: %v\n%s\n", r, debug.Stack())
		}
	}()

	s.watchdogMu.Lock()
	// Cancel any previous watchdog before starting a new one
	if s.cancelWatchdog != nil {
		s.cancelWatchdog()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancelWatchdog = cancel
	s.watchdogLink = link
	s.watchdogProxy = useSystemProxy
	s.watchdogMu.Unlock()

	// Address of the VPN server itself. It is the only meaningful upstream probe
	// available here: the Kill Switch explicitly allows it, and sing-box keeps it
	// routed outside the tunnel, so it answers exactly when real connectivity is
	// back. A generic probe (8.8.8.8 and friends) would be useless — in TUN mode
	// it goes through the dead tunnel, and under the Kill Switch it is blocked
	// outright.
	upstream := upstreamProbeAddr(link)

	// Give the VPN core time to fully initialise before probing
	select {
	case <-time.After(20 * time.Second):
	case <-ctx.Done():
		return
	}

	failCount := 0
	backoff := watchdogInitialBackoff
	var retryAfter time.Time // zero value == no cooldown in effect
	ticker := time.NewTicker(watchdogProbeInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			conn, err := net.DialTimeout("tcp", core.ProxyListenAddr, 3*time.Second)
			if err == nil {
				_ = conn.Close()
				failCount = 0
				backoff = watchdogInitialBackoff
				retryAfter = time.Time{}
				continue
			}
			failCount++
			if failCount >= watchdogFailThreshold {
				// A previous attempt failed recently — stay quiet until it cools
				// down instead of retrying on every tick.
				if time.Now().Before(retryAfter) {
					continue
				}

				// Do not tear down and rebuild the core while the machine has no
				// upstream at all. Recreating sing-box (TUN adapter, interface
				// monitor, DNS transports) against a dead network cannot succeed,
				// and repeating it is what turns a brief outage into a crash loop.
				// Wait for the server to answer again instead.
				if upstream != "" && !probeUpstream(ctx, upstream) {
					s.emitSafe("watchdog-waiting")
					retryAfter = time.Now().Add(backoff)
					backoff = nextWatchdogBackoff(backoff)
					continue
				}

				failCount = 0
				s.emitSafe("watchdog-reconnecting")
				s.watchdogMu.Lock()
				savedLink := s.watchdogLink
				savedProxy := s.watchdogProxy
				s.watchdogMu.Unlock()
				// Restart without touching system proxy backup or kill switch
				_ = s.coreManager.Stop()
				s.stateMu.Lock()
				if s.cancelMonitor != nil {
					s.cancelMonitor()
					s.cancelMonitor = nil
				}
				s.stateMu.Unlock()

				// Verify we haven't been cancelled (disconnected by user) while stopping the core
				select {
				case <-ctx.Done():
					return
				default:
				}

				res := s.StartXray(savedLink, "", savedProxy)
				if ok, _ := res["success"].(bool); ok {
					s.emitSafe("watchdog-reconnected")
					backoff = watchdogInitialBackoff
					retryAfter = time.Time{}
				} else {
					errMsg, _ := res["error"].(string)
					s.emitSafe("watchdog-failed", errMsg)
					retryAfter = time.Now().Add(backoff)
					backoff = nextWatchdogBackoff(backoff)
				}
			}
		}
	}
}

// upstreamProbeAddr extracts the VPN server's host:port from a proxy link.
// Returns "" when the link yields no usable address, in which case the watchdog
// falls back to restarting unconditionally (the previous behaviour).
func upstreamProbeAddr(link string) string {
	outbound, err := core.ParseProxyLink(link)
	if err != nil {
		return ""
	}
	host, _ := outbound["server"].(string)
	if host == "" {
		return ""
	}

	// server_port is filled in by different parser branches as an int, and may
	// arrive as another numeric shape when the link carried it verbatim.
	port := 0
	switch p := outbound["server_port"].(type) {
	case int:
		port = p
	case float64:
		port = int(p)
	case string:
		port, _ = strconv.Atoi(p)
	}
	if port <= 0 || port > 65535 {
		return ""
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// probeUpstream reports whether the VPN server accepts a direct TCP connection,
// which is the signal that the machine has real connectivity again.
func probeUpstream(ctx context.Context, addr string) bool {
	dialCtx, cancel := context.WithTimeout(ctx, watchdogUpstreamTimeout)
	defer cancel()

	var dialer net.Dialer
	conn, err := dialer.DialContext(dialCtx, "tcp", addr)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// nextWatchdogBackoff doubles the cooldown up to watchdogMaxBackoff.
func nextWatchdogBackoff(current time.Duration) time.Duration {
	next := current * 2
	if next > watchdogMaxBackoff {
		return watchdogMaxBackoff
	}
	return next
}

// stopWatchdog cancels the running watchdog goroutine if any.
func (s *AppService) stopWatchdog() {
	s.watchdogMu.Lock()
	defer s.watchdogMu.Unlock()
	if s.cancelWatchdog != nil {
		s.cancelWatchdog()
		s.cancelWatchdog = nil
	}
}
