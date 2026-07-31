package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"NeoBox/backend/core"
	"NeoBox/backend/i18n"

	sclog "github.com/sagernet/sing-box/log"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// VPN session lifecycle: starting, stopping and monitoring the sing-box core.

// StartXray parses the selected proxy URL and runs sing-box.
// NOTE: settingsJSON is kept for API compatibility but is intentionally ignored —
// settings are always read fresh from disk to prevent stale/empty frontend state
// (e.g., after a DPAPI key change) from launching VPN with wrong configuration.
func (s *AppService) StartXray(link string, _ string, useSystemProxy bool) map[string]interface{} {
	response := map[string]interface{}{"success": false}

	// 1. Read settings directly from disk (authoritative source)
	var settings core.Settings
	if err := json.Unmarshal([]byte(s.GetSettings()), &settings); err != nil {
		response["error"] = i18n.T(i18n.ErrParseSettings, err)
		return response
	}

	// 1b. Check for admin privileges. TUN mode needs them for the adapter, and the
	// Kill Switch needs them to write Windows Firewall rules. Checking up front
	// means the user gets the existing UAC prompt instead of a connection that
	// silently comes up without the protection they asked for.
	if (settings.TunMode || settings.KillSwitch) && !s.CheckAdmin() {
		response["error"] = "admin_required"
		return response
	}

	// The proxy port is deliberately NOT probed before starting: checking
	// availability and then releasing it leaves a window for another process to
	// take the port. sing-box binds it directly and the bind error is handled
	// below, which makes the check and the acquisition atomic.

	// 2. Parse proxy URL
	outbound, err := core.ParseProxyLink(link)
	if err != nil {
		response["error"] = i18n.T(i18n.ErrParseLink, err)
		return response
	}

	// 3. Generate configuration
	// Generate a fresh per-session Clash API secret to prevent other local
	// processes from controlling the VPN core via the unauthenticated API.
	secret, err := generateClashSecret()
	if err != nil {
		// Connecting anyway would expose the Clash API to every local process.
		response["error"] = i18n.T(i18n.ErrSecretGeneration, err)
		return response
	}
	s.stateMu.Lock()
	s.clashSecret = secret
	s.stateMu.Unlock()

	cachePath := filepath.Join(s.userDataDir, "cache.db")
	config, err := core.GenerateConfig(outbound, settings, useSystemProxy, cachePath, secret)
	if err != nil {
		response["error"] = i18n.T(i18n.ErrGenerateConfig, err)
		return response
	}

	configBytes, err := json.Marshal(config)
	if err != nil {
		response["error"] = i18n.T(i18n.ErrSerializeConfig, err)
		return response
	}

	// 4. Start core manager
	// Read wailsCtx under its lock: SetContext may write it concurrently.
	var logWriter sclog.PlatformWriter
	s.wailsCtxMu.RLock()
	wCtx := s.wailsCtx
	s.wailsCtxMu.RUnlock()
	if wCtx != nil {
		// A previous session's streamer may still be running — the watchdog
		// reconnects by calling StartXray again — and overwriting the field
		// would strand its goroutine.
		s.stopLogStream()
		// emitSafe re-reads wailsCtx under its own lock on every send, so the
		// streamer never captures a context that SetContext may replace.
		ls := newLogStreamer(s.emitSafe, s.isWindowVisible)
		s.stateMu.Lock()
		s.logStream = ls
		s.stateMu.Unlock()
		logWriter = ls
	}

	// A new session starts its traffic counters from zero.
	s.resetSessionTraffic()

	if err := s.coreManager.Start(string(configBytes), logWriter); err != nil {
		// The streamer was already running to catch startup output; wind it
		// down so a failed attempt does not leave its goroutine behind.
		s.stopLogStream()
		errMsg := err.Error()
		// Provide a user-friendly message if the proxy port is already in use.
		// This replaces the TOCTOU check with atomic error detection from sing-box itself.
		if strings.Contains(errMsg, "address already in use") || strings.Contains(errMsg, "bind") {
			response["error"] = i18n.T(i18n.ErrPortBusy, core.ProxyListenPort)
		} else {
			response["error"] = i18n.T(i18n.ErrStartCore, err)
		}
		return response
	}

	// 4b. Enable Firewall Kill Switch if requested in settings.
	//
	// A kill switch that silently failed to arm is worse than none at all: the UI
	// says leaks are impossible while nothing is actually blocking them. So a
	// failure here aborts the connection instead of being discarded.
	if settings.KillSwitch {
		serverIP, _ := outbound["server"].(string)
		if err := s.enableKillSwitch(serverIP); err != nil {
			fmt.Printf("[killswitch] failed to arm: %v\n", err)
			_ = s.coreManager.Stop()
			s.stopLogStream()
			s.disableKillSwitch()
			response["error"] = i18n.T(i18n.ErrKillSwitchFailed, err)
			return response
		}
	}

	// 5. Update system proxy registry settings if requested (and not in TUN mode)
	if useSystemProxy && !settings.TunMode {
		s.SetSystemProxy(true)
	} else {
		s.SetSystemProxy(false)
	}

	// Start background traffic monitoring
	s.stateMu.Lock()
	if s.cancelMonitor != nil {
		s.cancelMonitor()
	}
	monitorCtx, cancel := context.WithCancel(context.Background())
	s.cancelMonitor = cancel
	s.stateMu.Unlock()
	go s.startTrafficMonitor(monitorCtx)

	s.setTrayStatus(i18n.TrayStatusConnected, parseServerNameFromLink(link))
	// Notify user via Windows toast when connected (window may be hidden in tray)
	go sendToast(i18n.T(i18n.ToastConnectedTitle), i18n.T(i18n.ToastConnectedBody, parseServerNameFromLink(link)))

	// Start watchdog — auto-reconnect if tunnel drops
	go s.startWatchdog(link, useSystemProxy)

	// Tell the UI the session is up. It used to infer this by matching
	// "sing-box started" in the log stream, which tied the connected state to
	// the core's log level — and to the UI receiving a line it does not
	// otherwise need. Every start path (including the watchdog's reconnect)
	// comes through here, so this is both the earlier and the surer signal.
	s.emitSafe("xray-started", nil)

	response["success"] = true
	return response
}

// StopXray stops sing-box and disables system proxy settings.
func (s *AppService) StopXray() map[string]interface{} {
	response := map[string]interface{}{"success": false}

	// Stop watchdog first so it doesn't try to restart while we're stopping
	s.stopWatchdog()

	s.SetSystemProxy(false)
	s.disableKillSwitch() // Disable firewall rules when disconnecting

	s.stateMu.Lock()
	if s.cancelMonitor != nil {
		s.cancelMonitor()
		s.cancelMonitor = nil
	}
	s.stateMu.Unlock()

	if err := s.coreManager.Stop(); err != nil {
		response["error"] = err.Error()
		return response
	}
	s.stopLogStream()

	s.wailsCtxMu.RLock()
	wCtxStop := s.wailsCtx
	s.wailsCtxMu.RUnlock()
	if wCtxStop != nil {
		wailsruntime.EventsEmit(wCtxStop, "xray-stopped", nil)
	}

	s.setTrayStatus(i18n.TrayStatusDisconnected)
	// Notify user via Windows toast on disconnect
	go sendToast(i18n.T(i18n.ToastDisconnectedTitle), i18n.T(i18n.ToastDisconnectedBody))

	response["success"] = true
	return response
}

// RestartXray restarts the VPN core without disturbing the system proxy backup.
// Unlike calling StopXray + StartXray separately, this preserves the proxy backup
// state so the user's original proxy settings are correctly restored on final disconnect.
func (s *AppService) RestartXray(link string, settingsJSON string, useSystemProxy bool) map[string]interface{} {
	// Stop watchdog before restarting; StartXray will re-launch it.
	s.stopWatchdog()

	// Stop the core and traffic monitor only — do NOT touch system proxy or kill switch.
	s.stateMu.Lock()
	if s.cancelMonitor != nil {
		s.cancelMonitor()
		s.cancelMonitor = nil
	}
	s.stateMu.Unlock()
	_ = s.coreManager.Stop()
	s.stopLogStream()

	// Emit stopped event so UI knows the old session ended
	s.wailsCtxMu.RLock()
	wCtxRestart := s.wailsCtx
	s.wailsCtxMu.RUnlock()
	if wCtxRestart != nil {
		wailsruntime.EventsEmit(wCtxRestart, "xray-stopped", nil)
	}

	// Start fresh session — proxy backup is still intact from the original StartXray call.
	return s.StartXray(link, settingsJSON, useSystemProxy)
}

// PingServer measures TCP round-trip latency to the server host and port.
func (s *AppService) PingServer(link string) int {
	outbound, err := core.ParseProxyLink(link)
	if err != nil {
		return -1
	}

	server, _ := outbound["server"].(string)
	port, _ := outbound["server_port"].(int)

	if server == "" || port == 0 {
		return -1
	}

	// Use net.JoinHostPort so IPv6 addresses are correctly wrapped in brackets: [::1]:port
	address := net.JoinHostPort(server, strconv.Itoa(port))
	start := time.Now()
	conn, err := net.DialTimeout("tcp", address, 3*time.Second)
	if err != nil {
		return -1
	}
	defer conn.Close()

	elapsed := time.Since(start)
	return int(elapsed.Milliseconds())
}

// CheckTunStatus checks if the "tun-neobox" network interface is active and up.
func (s *AppService) CheckTunStatus() bool {
	ifaces, err := net.Interfaces()
	if err != nil {
		return false
	}
	for _, iface := range ifaces {
		if iface.Name == "tun-neobox" {
			return (iface.Flags & net.FlagUp) != 0
		}
	}
	return false
}

// startTrafficMonitor connects to sing-box clash_api /traffic endpoint
// and streams real-time upload and download speeds to the Wails frontend.
// The per-session clashSecret is sent as a Bearer token so only NeoBox
// can consume the Clash API (security improvement #1).
func (s *AppService) startTrafficMonitor(ctx context.Context) {
	// Give clash_api half a second to bind and boot up
	time.Sleep(500 * time.Millisecond)

	// Snapshot the current session secret (protected by mu)
	s.stateMu.Lock()
	secret := s.clashSecret
	s.stateMu.Unlock()

	client := &http.Client{Timeout: 0} // infinite timeout for stream
	req, err := http.NewRequestWithContext(ctx, "GET", "http://"+core.ClashAPIAddr+"/traffic", nil)
	if err != nil {
		return
	}
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}

	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	dec := json.NewDecoder(resp.Body)
	for {
		select {
		case <-ctx.Done():
			return
		default:
			var stats struct {
				Up   int64 `json:"up"`
				Down int64 `json:"down"`
			}
			if err := dec.Decode(&stats); err != nil {
				// Exit if stream is broken or closed
				return
			}

			// Keep draining the stream regardless of window state — stopping
			// would break the HTTP connection — but fold every sample into the
			// session totals here rather than in JS. The frontend used to sum
			// the per-second speeds itself, which cannot survive a period where
			// no events are delivered at all.
			totalUp, totalDown := s.addSessionTraffic(stats.Up, stats.Down)

			// While the window is hidden nobody can see the speedometer, and
			// every event is a separate ExecuteScript into WebView2. Skip it;
			// onWindowRestored sends one catch-up sample with the totals.
			if !s.isWindowVisible() {
				continue
			}

			// Emit stats to the Wails frontend
			s.wailsCtxMu.RLock()
			wCtx := s.wailsCtx
			s.wailsCtxMu.RUnlock()
			if wCtx != nil {
				wailsruntime.EventsEmit(wCtx, "traffic-stats", map[string]interface{}{
					"up":        stats.Up,
					"down":      stats.Down,
					"totalUp":   totalUp,
					"totalDown": totalDown,
				})
			}
		}
	}
}
