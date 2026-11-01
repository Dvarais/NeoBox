package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"NeoBox/backend/core"
	"NeoBox/backend/platform"
	"NeoBox/backend/i18n"

	sclog "github.com/sagernet/sing-box/log"
)

// VPN session lifecycle: starting, stopping and monitoring the sing-box core.

// StartXray parses the selected proxy URL and runs sing-box.
// NOTE: settingsJSON is kept for API compatibility but is intentionally ignored —
// settings are always read fresh from disk to prevent stale/empty frontend state
// (e.g., after a DPAPI key change) from launching VPN with wrong configuration.
//
// This is the connection a person asked for, so it opens a new session: the
// traffic totals start from zero. The watchdog reconnects through startCore
// instead — see there.
func (s *AppService) StartXray(link string, settingsJSON string, useSystemProxy bool) map[string]interface{} {
	// [INTERNET SEARCH PATTERN]:
	// Atomic CAS transition Disconnected -> Connecting.
	// Reject concurrent attempts or re-entrancy from rapid UI clicks.
	if !s.casVPNState(VPNStateDisconnected, VPNStateConnecting) {
		current := s.GetVPNState()
		return map[string]interface{}{
			"success": false,
			"error":   fmt.Sprintf("cannot start VPN: already %s", current),
		}
	}

	res := s.startCore(link, settingsJSON, useSystemProxy, true)
	if success, _ := res["success"].(bool); success {
		// Only transition to Connected if we were not cancelled/disconnected while startCore was running.
		if !s.casVPNState(VPNStateConnecting, VPNStateConnected) {
			// A disconnect was requested concurrently while startCore was finishing. Clean up all resources immediately.
			_ = s.teardownVpnCore(true, true)
			return map[string]interface{}{
				"success": false,
				"error":   "connection aborted: disconnect requested during startup",
			}
		}
	} else {
		// Startup failed: transition back to Disconnected so the user can retry.
		s.casVPNState(VPNStateConnecting, VPNStateDisconnected)
	}
	return res
}

// startCore is StartXray with the one decision the watchdog needs to make
// differently: whether this is a new session or the continuation of one.
//
// «За сессию» is the user's session, not the core process's. The watchdog
// restarts the core whenever the link drops, and folding that into StartXray
// meant every automatic recovery silently reset the volume to zero — the tunnel
// had a new process, but for the person watching nothing had ended. So the
// reset moved to the paths a person actually initiates: connecting, and the
// explicit Restart button.
func (s *AppService) startCore(link string, _ string, useSystemProxy bool, newSession bool) map[string]interface{} {
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

	// 2. Parse proxy URL and build candidate list for Clash API selector
	var outbound map[string]interface{}
	var candidates []map[string]interface{}
	var linkToTag map[string]string

	if link == core.AutoServerLink {
		candidates, linkToTag = s.buildCandidates(link, nil)
		if len(candidates) < 2 {
			response["error"] = i18n.T(i18n.ErrNoAutoNodes)
			return response
		}
		// Use the first candidate as the primary template for DNS/direct routing, but instruct
		// GenerateConfigWithCandidates to set selector default to OutboundAutoTag
		outbound = make(map[string]interface{}, len(candidates[0]))
		for k, v := range candidates[0] {
			outbound[k] = v
		}
		outbound["_selector_default"] = core.OutboundAutoTag
	} else {
		var err error
		outbound, err = core.ParseProxyLink(link)
		if err != nil {
			response["error"] = i18n.T(i18n.ErrParseLink, err)
			return response
		}
		// 2b. Build candidate list and link-to-tag mapping for Clash API selector
		candidates, linkToTag = s.buildCandidates(link, outbound)
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
	config, err := core.GenerateConfigWithCandidates(candidates, outbound, settings, useSystemProxy, cachePath, secret)
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
	wCtx := s.context()
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

	// A new session starts its traffic counters from zero. A watchdog recovery
	// is not a new session and keeps them running.
	if newSession {
		s.resetSessionTraffic()
	}

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
		// Ask ServerEndpoint rather than reading outbound["server"]: a WireGuard
		// endpoint has no such field — its address lives in the first peer — so
		// the direct read handed the Kill Switch an empty host, which it rightly
		// refused to arm on. Every WireGuard node was unconnectable as a result.
		serverHost, _ := core.ServerEndpoint(outbound)
		if err := s.enableKillSwitch(serverHost); err != nil {
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

	// Start background traffic monitoring and record active selector mappings
	s.stateMu.Lock()
	s.serverLinkToTag = linkToTag
	s.activeServerLink = link
	if s.cancelMonitor != nil {
		s.cancelMonitor()
	}
	monitorCtx, cancel := context.WithCancel(context.Background())
	s.cancelMonitor = cancel
	s.stateMu.Unlock()
	// Каждый монитор подключается к своему ядру впервые — иначе он объявил бы
	// «поток восстановлен» на обычном первом подключении.
	s.trafficMu.Lock()
	s.trafficStreamOpened = false
	s.trafficMu.Unlock()
	go s.startTrafficMonitor(monitorCtx)

	s.setTrayConnected(true, parseServerNameFromLink(link))
	// Notify user via Windows toast when connected (window may be hidden in tray)
	go sendToast(i18n.T(i18n.ToastConnectedTitle), i18n.T(i18n.ToastConnectedBody, parseServerNameFromLink(link)))

	// Start watchdog — auto-reconnect if tunnel drops
	go s.startWatchdog(link, useSystemProxy)

	// Tell the UI the session is up. It used to infer this by matching
	// "sing-box started" in the log stream, which tied the connected state to
	// the core's log level — and to the UI receiving a line it does not
	// otherwise need. Every start path (including the watchdog's reconnect)
	// comes through here, so this is both the earlier and the surer signal.
	_ = platform.Current().DNS().FlushCache()
	s.emitSafe("xray-started", nil)

	response["success"] = true
	return response
}

// teardownVpnCore stops the running core, monitoring goroutines, and log streams,
// optionally clearing system proxy and kill switch settings.
func (s *AppService) teardownVpnCore(clearSystemProxy, clearKillSwitch bool) error {
	s.stopWatchdog()
	if clearSystemProxy {
		s.SetSystemProxy(false)
	}
	if clearKillSwitch {
		s.disableKillSwitch()
	}
	s.stateMu.Lock()
	s.serverLinkToTag = nil
	s.activeServerLink = ""
	if s.cancelMonitor != nil {
		s.cancelMonitor()
		s.cancelMonitor = nil
	}
	s.stateMu.Unlock()

	err := s.coreManager.Stop()
	s.stopLogStream()
	_ = platform.Current().DNS().FlushCache()
	if clearSystemProxy && clearKillSwitch {
		s.setTrayConnected(false, "")
	}
	s.emitSafe("xray-stopped", nil)
	releaseIdleMemory()
	return err
}

// StopXray stops sing-box and disables system proxy settings.
func (s *AppService) StopXray() map[string]interface{} {
	response := map[string]interface{}{"success": false}

	// [INTERNET SEARCH PATTERN]:
	// Atomic CAS loop guarantees thread-safe transition to Disconnecting
	// and idempotency across rapid repeated clicks or concurrent stop triggers.
	for {
		current := s.GetVPNState()
		switch current {
		case VPNStateDisconnected:
			// Already disconnected: return idempotent success without redundant teardown.
			response["success"] = true
			return response
		case VPNStateDisconnecting:
			response["error"] = "vpn is already disconnecting"
			return response
		case VPNStateConnecting, VPNStateConnected, VPNStateReconnecting:
			if s.casVPNState(current, VPNStateDisconnecting) {
				goto executeTeardown
			}
			// If CAS failed due to an interleaved state change, loop and re-evaluate.
		default:
			response["error"] = fmt.Sprintf("invalid vpn state: %s", current)
			return response
		}
	}

executeTeardown:
	defer s.setVPNState(VPNStateDisconnected)

	if err := s.teardownVpnCore(true, true); err != nil {
		response["error"] = err.Error()
		return response
	}

	// Notify user via Windows toast on disconnect
	go sendToast(i18n.T(i18n.ToastDisconnectedTitle), i18n.T(i18n.ToastDisconnectedBody))

	response["success"] = true
	return response
}

// RestartXray restarts the VPN core without disturbing the system proxy backup.
// Unlike calling StopXray + StartXray separately, this preserves the proxy backup
// state so the user's original proxy settings are correctly restored on final disconnect.
func (s *AppService) RestartXray(link string, settingsJSON string, useSystemProxy bool) map[string]interface{} {
	for {
		current := s.GetVPNState()
		if current == VPNStateDisconnecting || current == VPNStateConnecting || current == VPNStateReconnecting {
			return map[string]interface{}{
				"success": false,
				"error":   fmt.Sprintf("cannot restart VPN: currently %s", current),
			}
		}
		if s.casVPNState(current, VPNStateReconnecting) {
			break
		}
	}

	// Stop watchdog, core and monitor without clearing system proxy or kill switch
	_ = s.teardownVpnCore(false, false)

	// Start fresh session directly while holding Reconnecting state
	res := s.startCore(link, settingsJSON, useSystemProxy, true)
	if ok, _ := res["success"].(bool); ok {
		if !s.casVPNState(VPNStateReconnecting, VPNStateConnected) {
			// Disconnect was requested during restart
			_ = s.teardownVpnCore(true, true)
			return map[string]interface{}{
				"success": false,
				"error":   "restart aborted: disconnect requested",
			}
		}
	} else {
		// Restart failed: transition back to Disconnected
		s.casVPNState(VPNStateReconnecting, VPNStateDisconnected)
	}
	return res
}

// PingServer measures genuine round-trip latency to a proxy node by testing
// end-to-end HTTP 204 connectivity through the proxy protocol.
// When the VPN is connected, it uses the fast-path Clash API /proxies/{tag}/delay
// on the running core with zero CPU/RAM overhead; otherwise it falls back to
// the standalone ephemeral core with worker pool concurrency limiting.
func (s *AppService) PingServer(link string) int {
	trimmed := strings.TrimSpace(link)
	if trimmed == "" {
		return -1
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	// Fast path: if VPN is connected and node tag is registered in the Clash API selector
	if s.GetVPNState() == VPNStateConnected {
		s.stateMu.Lock()
		clashAddr := core.ClashAPIAddr
		if s.clashAddr != "" {
			clashAddr = s.clashAddr
		}
		secret := s.clashSecret
		targetTag, found := s.serverLinkToTag[trimmed]
		s.stateMu.Unlock()

		if found && targetTag != "" {
			path := fmt.Sprintf("/proxies/%s/delay?url=%s&timeout=3500", url.PathEscape(targetTag), url.QueryEscape(core.DefaultDelayTestURL))
			respBytes, err := fetchClash(ctx, http.MethodGet, clashAddr, path, secret)
			if err == nil {
				var result struct {
					Delay int `json:"delay"`
				}
				if json.Unmarshal(respBytes, &result) == nil && result.Delay > 0 {
					return result.Delay
				}
			}
			// When registered in the running core's selector and Clash API returned error or timeout,
			// the node is genuinely unreachable. Avoid spinning up an ephemeral instance with the remaining context.
			return -1
		}
	}

	// Fallback path: standalone ephemeral sing-box instance with semaphore rate-limiting
	delay, err := core.MeasureProxyDelay(ctx, trimmed, "")
	if err == nil && delay >= 0 {
		return delay
	}
	return -1
}

// MeasureActiveConnectionSpeed downloads the speedtest payload through the currently active VPN tunnel
// via local HTTP proxy (127.0.0.1:10808) with zero extra process overhead.
func (s *AppService) MeasureActiveConnectionSpeed(ctx context.Context, testURL string, byteCount int64) (float64, error) {
	if testURL == "" {
		testURL = core.DefaultSpeedTestURL
	}
	proxyURL, err := url.Parse("http://" + core.ProxyListenAddr)
	if err != nil {
		return 0, err
	}

	transport := &http.Transport{
		Proxy:             http.ProxyURL(proxyURL),
		DisableKeepAlives: true,
	}
	defer transport.CloseIdleConnections()

	client := &http.Client{
		Transport: transport,
		Timeout:   20 * time.Second,
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, testURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", core.GetRandomUserAgent())

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return 0, fmt.Errorf("speedtest endpoint returned HTTP %d", resp.StatusCode)
	}

	var src io.Reader = resp.Body
	if byteCount > 0 {
		src = io.LimitReader(resp.Body, byteCount)
	}
	totalRead, err := io.Copy(io.Discard, src)
	if err != nil {
		return 0, err
	}

	elapsed := time.Since(start)
	if totalRead == 0 {
		return 0, fmt.Errorf("no bytes transferred during speedtest")
	}
	if elapsed < 10*time.Millisecond {
		elapsed = 10 * time.Millisecond
	}

	mbps := (float64(totalRead) * 8.0) / (elapsed.Seconds() * 1_000_000.0)
	return mbps, nil
}

// TestServerSpeed tests the effective downstream throughput in Mbps of a proxy server.
// If the VPN is active and testing the current server, it downloads through the live tunnel.
// Otherwise, it spins up an ephemeral core instance to measure throughput before connecting.
func (s *AppService) TestServerSpeed(link string) map[string]interface{} {
	trimmedLink := strings.TrimSpace(link)
	if trimmedLink == "" {
		s.stateMu.Lock()
		trimmedLink = s.activeServerLink
		s.stateMu.Unlock()
	}
	if trimmedLink == "" {
		return map[string]interface{}{
			"success": false,
			"error":   "no server specified",
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	s.stateMu.Lock()
	activeLink := s.activeServerLink
	s.stateMu.Unlock()
	vpnConnected := s.GetVPNState() == VPNStateConnected

	if trimmedLink == core.AutoServerLink && !vpnConnected {
		return map[string]interface{}{
			"success": false,
			"error":   "speed test in auto mode requires an active connection",
		}
	}

	var mbps float64
	var err error

	// If VPN is connected and testing the currently active server (or in Auto mode)
	if vpnConnected && (trimmedLink == activeLink || trimmedLink == core.AutoServerLink) {
		mbps, err = s.MeasureActiveConnectionSpeed(ctx, "", 5_000_000)
	} else {
		// Testing a disconnected or non-active server
		mbps, err = core.MeasureProxySpeed(ctx, trimmedLink, "", 5_000_000)
	}

	if err != nil {
		return map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		}
	}

	// Round to 1 decimal place
	roundedMbps := math.Round(mbps*10) / 10

	return map[string]interface{}{
		"success": true,
		"mbps":    roundedMbps,
	}
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

// Пауза перед повторной попыткой подключиться к потоку трафика. Начинается с
// той же половины секунды, что раньше стояла одиноким Sleep'ом, и удваивается
// до пяти: поток восстанавливают, а не долбят.
const (
	trafficRetryMin = 500 * time.Millisecond
	trafficRetryMax = 5 * time.Second
)

// startTrafficMonitor keeps the traffic counters fed for as long as the session
// lasts, reconnecting to the sing-box clash_api /traffic stream whenever it
// breaks. The per-session clashSecret goes out as a Bearer token so only NeoBox
// can consume the Clash API.
//
// Раньше это была одна попытка без права на ошибку. Любая осечка — clash_api не
// успел встать за отведённые ему полсекунды, оборвалось соединение, пришёл
// нечитаемый кадр — заканчивала горутину навсегда и молча. Счётчик замирал (а
// в случае стартовой гонки не оживал вовсе), и понять почему было нельзя: в
// журнал не попадало ничего.
//
// Теперь отказ — это пауза, а не конец, и о нём говорят вслух. Заодно исчезла
// сама стартовая гонка: фиксированный Sleep стал первой паузой цикла, а
// проигранная гонка — обычной неудачной попыткой, за которой следует ещё одна.
func (s *AppService) startTrafficMonitor(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "[vpn] recovered panic in startTrafficMonitor: %v\n", r)
		}
	}()

	delay := trafficRetryMin
	attempts := 0
	timer := time.NewTimer(delay)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		attempts++
		err := s.streamTraffic(ctx)
		if ctx.Err() != nil {
			// Сессия закончилась — это не отказ, а плановая остановка.
			return
		}

		// Первую неудачу не показываем: подключиться к clash_api раньше, чем
		// он успел открыть порт, — это норма запуска, и жаловаться на неё
		// значит пугать пользователя штатным ходом дел.
		if attempts == 2 {
			s.logNotice("WARN", "traffic stream unavailable, retrying: %v", err)
		}

		delay *= 2
		if delay > trafficRetryMax {
			delay = trafficRetryMax
		}
		timer.Reset(delay)
	}
}

// streamTraffic runs one connection to the /traffic endpoint and returns when
// it ends — because the session was cancelled, or because it broke.
func (s *AppService) streamTraffic(ctx context.Context) error {
	// Snapshot the current session secret and clash address (protected by stateMu)
	s.stateMu.Lock()
	clashAddr := core.ClashAPIAddr
	if s.clashAddr != "" {
		clashAddr = s.clashAddr
	}
	secret := s.clashSecret
	s.stateMu.Unlock()

	client := &http.Client{Timeout: 0} // infinite timeout for stream
	req, err := http.NewRequestWithContext(ctx, "GET", "http://"+clashAddr+"/traffic", nil)
	if err != nil {
		return err
	}
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("clash api answered %s", resp.Status)
	}

	// Сюда доходят только когда поток действительно открыт. Если это не первое
	// подключение за сессию, о восстановлении сообщаем: пользователь уже видел
	// предупреждение выше и вправе узнать, чем дело кончилось.
	s.trafficMu.Lock()
	reconnected := s.trafficStreamOpened
	s.trafficStreamOpened = true
	s.trafficMu.Unlock()
	if reconnected {
		s.logNotice("INFO", "traffic stream restored")
	}

	dec := json.NewDecoder(resp.Body)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			var stats struct {
				Up   int64 `json:"up"`
				Down int64 `json:"down"`
			}
			if err := dec.Decode(&stats); err != nil {
				// Поток кончился или сломался — наружу, там решат, ждать ли.
				return err
			}

			// Keep draining the stream regardless of window state — stopping
			// would break the HTTP connection — but fold every sample into the
			// session totals here rather than in JS. The frontend used to sum
			// the per-second speeds itself, which cannot survive a period where
			// no events are delivered at all.
			totalUp, totalDown := s.addSessionTraffic(stats.Up, stats.Down)

			// Подсказка иконки обновляется до проверки видимости, а не после:
			// пока окно в трее, это единственное место, где цифры ещё видно.
			s.refreshTrayTooltip()

			// While the window is hidden nobody can see the speedometer, and
			// every event is a separate ExecuteScript into WebView2. Skip it;
			// onWindowRestored sends one catch-up sample with the totals.
			if !s.isWindowVisible() {
				continue
			}

			// Emit stats to the Wails frontend
			s.emitSafe("traffic-stats", map[string]interface{}{
				"up":        stats.Up,
				"down":      stats.Down,
				"totalUp":   totalUp,
				"totalDown": totalDown,
			})
		}
	}
}

// FlushDNS flushes the OS-level local DNS resolver cache.
func (s *AppService) FlushDNS() (bool, error) {
	if err := platform.Current().DNS().FlushCache(); err != nil {
		return false, err
	}
	return true, nil
}

// buildCandidates collects valid non-wireguard proxy candidates from subscriptions,
// assigns unique outbound tags, and builds the link-to-tag mapping for the Clash API selector.
// When activeOutbound is nil (e.g. for Auto mode), all valid subscription proxies are collected.
func (s *AppService) buildCandidates(activeLink string, activeOutbound map[string]interface{}) ([]map[string]interface{}, map[string]string) {
	linkToTag := make(map[string]string)
	trimmedActive := strings.TrimSpace(activeLink)
	var activeTag string
	var activeType string
	if activeOutbound != nil {
		activeType, _ = activeOutbound["type"].(string)
		activeTag, _ = activeOutbound["tag"].(string)
		if activeTag == "" {
			activeTag = parseServerNameFromLink(trimmedActive)
			if activeTag == "" {
				activeTag = "proxy"
			}
			activeOutbound["tag"] = activeTag
		}
		linkToTag[trimmedActive] = activeTag

		// WireGuard is defined as an endpoint in sing-box, not an outbound.
		// Endpoints cannot be placed inside a selector outbound group.
		if activeType == "wireguard" {
			return []map[string]interface{}{activeOutbound}, linkToTag
		}
	}

	s.fileMu.Lock()
	data := s.readSubscriptionsLocked()
	s.fileMu.Unlock()

	var subs []Subscription
	if data != nil {
		if err := json.Unmarshal(data, &subs); err != nil {
			fmt.Printf("[vpn] warning: buildCandidates failed to unmarshal subscriptions: %v\n", err)
		}
	}

	totalEstimated := 1
	for _, sub := range subs {
		totalEstimated += len(sub.Links)
	}

	usedTags := make(map[string]bool, totalEstimated)
	seenLinks := make(map[string]bool, totalEstimated)
	candidates := make([]map[string]interface{}, 0, totalEstimated)

	if activeOutbound != nil {
		usedTags[activeTag] = true
		seenLinks[trimmedActive] = true
		candidates = append(candidates, activeOutbound)
	}

	for _, sub := range subs {
		for _, rawLink := range sub.Links {
			lnk := strings.TrimSpace(rawLink)
			if lnk == "" || seenLinks[lnk] || lnk == core.AutoServerLink {
				continue
			}
			seenLinks[lnk] = true

			candOut, err := core.ParseProxyLink(lnk)
			if err != nil {
				continue
			}
			cType, _ := candOut["type"].(string)
			if cType == "wireguard" {
				continue
			}

			cTag, _ := candOut["tag"].(string)
			if cTag == "" {
				cTag = parseServerNameFromLink(lnk)
			}
			if cTag == "" {
				cTag = "proxy"
			}
			uniqueTag := cTag
			counter := 2
			for usedTags[uniqueTag] {
				uniqueTag = fmt.Sprintf("%s-%d", cTag, counter)
				counter++
			}
			usedTags[uniqueTag] = true
			candOut["tag"] = uniqueTag
			linkToTag[lnk] = uniqueTag
			candidates = append(candidates, candOut)
		}
	}

	if len(candidates) >= 2 {
		linkToTag[core.AutoServerLink] = core.OutboundAutoTag
	}

	return candidates, linkToTag
}

// switchSelectorTag issues a PUT /proxies/proxy-out request to sing-box Clash API
// to switch active outbound to targetTag with zero-downtime.
func (s *AppService) switchSelectorTag(ctx context.Context, addr, secret, targetTag string) error {
	payload, err := json.Marshal(map[string]string{"name": targetTag})
	if err != nil {
		return err
	}
	_, err = fetchClashWithBody(ctx, http.MethodPut, addr, "/proxies/"+core.OutboundSelectorTag, secret, bytes.NewReader(payload))
	return err
}

// SwitchServer performs zero-downtime hot-switching to the specified proxy server link
// via sing-box's Clash API selector without restarting TUN interface, dropping sockets,
// or experiencing 3-second downtime.
//
// If the VPN is not connected, the target node is WireGuard, or the node was not
// in the selector candidates, it reports fallbackRequired: true so the caller can
// cleanly fall back to RestartXray.
func (s *AppService) SwitchServer(link string) map[string]interface{} {
	trimmedLink := strings.TrimSpace(link)
	if trimmedLink == "" {
		return map[string]interface{}{
			"success":          false,
			"hotSwitched":      false,
			"fallbackRequired": false,
			"error":            "empty proxy link",
		}
	}

	if s.GetVPNState() != VPNStateConnected {
		return map[string]interface{}{
			"success":          false,
			"hotSwitched":      false,
			"fallbackRequired": true,
			"error":            "vpn is not connected",
		}
	}

	s.stateMu.Lock()
	activeLink := s.activeServerLink
	secret := s.clashSecret
	targetTag, found := s.serverLinkToTag[trimmedLink]
	oldTag := s.serverLinkToTag[activeLink]
	s.stateMu.Unlock()

	// Already active on this exact server
	if activeLink == trimmedLink {
		return map[string]interface{}{
			"success":       true,
			"hotSwitched":   true,
			"alreadyActive": true,
		}
	}

	// If node is not found in selector mapping (e.g. WireGuard, or node added after startCore)
	if !found || targetTag == "" {
		return map[string]interface{}{
			"success":          false,
			"hotSwitched":      false,
			"fallbackRequired": true,
			"error":            "node is not in selector group",
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), clashRequestTimeout)
	defer cancel()

	clashAddr := core.ClashAPIAddr
	s.stateMu.Lock()
	if s.clashAddr != "" {
		clashAddr = s.clashAddr
	}
	s.stateMu.Unlock()

	if err := s.switchSelectorTag(ctx, clashAddr, secret, targetTag); err != nil {
		return map[string]interface{}{
			"success":          false,
			"hotSwitched":      false,
			"fallbackRequired": true,
			"error":            fmt.Sprintf("clash api selector switch failed: %v", err),
		}
	}

	// Update Windows Firewall Kill Switch if active (after core outbound switch succeeds)
	var settings core.Settings
	if err := json.Unmarshal([]byte(s.GetSettings()), &settings); err == nil && settings.KillSwitch {
		if trimmedLink != core.AutoServerLink {
			targetOutbound, err := core.ParseProxyLink(trimmedLink)
			if err != nil {
				// Rollback selector if parsing fails
				if oldTag != "" {
					_ = s.switchSelectorTag(ctx, clashAddr, secret, oldTag)
				}
				return map[string]interface{}{
					"success":          false,
					"hotSwitched":      false,
					"fallbackRequired": true,
					"error":            fmt.Sprintf("failed to parse target link for killswitch: %v", err),
				}
			}
			newServerHost, _ := core.ServerEndpoint(targetOutbound)
			if err := s.enableKillSwitch(newServerHost); err != nil {
				// Rollback selector if firewall update fails
				if oldTag != "" {
					_ = s.switchSelectorTag(ctx, clashAddr, secret, oldTag)
				}
				return map[string]interface{}{
					"success":          false,
					"hotSwitched":      false,
					"fallbackRequired": true,
					"error":            fmt.Sprintf("failed to update killswitch firewall rule: %v", err),
				}
			}
		}
	}

	// Hot-switch succeeded: update internal state
	s.stateMu.Lock()
	s.activeServerLink = trimmedLink
	s.stateMu.Unlock()

	// Keep watchdog synchronized so it probes the newly active node
	s.watchdogMu.Lock()
	s.watchdogLink = trimmedLink
	s.watchdogMu.Unlock()

	// Persist lastSelectedServer to state.json
	s.fileMu.Lock()
	secrets := s.readSecretSettingsLocked()
	secrets["lastSelectedServer"] = trimmedLink
	if err := s.writeSecretSettingsLocked(secrets); err != nil {
		fmt.Printf("[vpn] warning: failed to persist lastSelectedServer: %v\n", err)
	}
	s.fileMu.Unlock()

	// Update tray icon & tooltip with new server name
	srvName := parseServerNameFromLink(trimmedLink)
	s.setTrayConnected(true, srvName)

	// Flush local OS DNS cache
	_ = platform.Current().DNS().FlushCache()

	// Notify frontend
	s.emitSafe("server-switched", map[string]interface{}{
		"link": trimmedLink,
		"name": srvName,
	})

	return map[string]interface{}{
		"success":     true,
		"hotSwitched": true,
	}
}

// GetAutoSelectedNode queries Clash API GET /proxies/auto-out to report which proxy node
// sing-box's urltest group has currently selected based on lowest latency.
func (s *AppService) GetAutoSelectedNode() map[string]interface{} {
	if s.GetVPNState() != VPNStateConnected {
		return map[string]interface{}{
			"node":    "",
			"running": false,
			"isAuto":  false,
		}
	}

	s.stateMu.Lock()
	clashAddr := core.ClashAPIAddr
	if s.clashAddr != "" {
		clashAddr = s.clashAddr
	}
	secret := s.clashSecret
	activeLink := s.activeServerLink
	s.stateMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), clashRequestTimeout)
	defer cancel()

	respBytes, err := fetchClash(ctx, http.MethodGet, clashAddr, "/proxies/"+core.OutboundAutoTag, secret)
	if err != nil {
		return map[string]interface{}{
			"node":    "",
			"running": true,
			"isAuto":  activeLink == core.AutoServerLink,
			"error":   err.Error(),
		}
	}

	var data struct {
		Name string `json:"name"`
		Type string `json:"type"`
		Now  string `json:"now"`
	}
	if err := json.Unmarshal(respBytes, &data); err != nil {
		return map[string]interface{}{
			"node":    "",
			"running": true,
			"isAuto":  activeLink == core.AutoServerLink,
			"error":   err.Error(),
		}
	}

	return map[string]interface{}{
		"node":    data.Now,
		"running": true,
		"isAuto":  activeLink == core.AutoServerLink,
	}
}
