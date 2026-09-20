package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
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
//
// This is the connection a person asked for, so it opens a new session: the
// traffic totals start from zero. The watchdog reconnects through startCore
// instead — see there.
func (s *AppService) StartXray(link string, settingsJSON string, useSystemProxy bool) map[string]interface{} {
	return s.startCore(link, settingsJSON, useSystemProxy, true)
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

	// Start background traffic monitoring
	s.stateMu.Lock()
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

	wCtxStop := s.context()
	if wCtxStop != nil {
		wailsruntime.EventsEmit(wCtxStop, "xray-stopped", nil)
	}

	s.setTrayConnected(false, "")
	// Notify user via Windows toast on disconnect
	go sendToast(i18n.T(i18n.ToastDisconnectedTitle), i18n.T(i18n.ToastDisconnectedBody))

	// A session's buffers -- the gVisor stack's packets, the DNS tables, the
	// routing rules -- all become garbage at once here. Hand them back rather
	// than sitting on the peak until the scavenger gets round to it.
	releaseIdleMemory()

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
	wCtxRestart := s.context()
	if wCtxRestart != nil {
		wailsruntime.EventsEmit(wCtxRestart, "xray-stopped", nil)
	}

	// Start fresh session — proxy backup is still intact from the original StartXray call.
	return s.StartXray(link, settingsJSON, useSystemProxy)
}

// PingServer measures round-trip latency to a server: the TCP handshake where
// the protocol runs over TCP, and an ICMP echo where it does not.
//
// The split exists because a TCP dial to a UDP-only server — WireGuard, TUIC,
// Hysteria — can only ever time out, so every such node used to report -1 and
// sank to the bottom of the "fastest server" ordering however good it was.
func (s *AppService) PingServer(link string) int {
	outbound, err := core.ParseProxyLink(link)
	if err != nil {
		return -1
	}

	server, port := core.ServerEndpoint(outbound)
	if server == "" || port == 0 {
		return -1
	}

	if core.IsUDPOnly(core.ProtocolOf(link)) {
		// ICMP times the path to the host rather than the service on the port.
		// It is the closest thing to a round trip available without performing
		// the protocol's own handshake, and it returns -1 when the server
		// filters echo requests — the same "unknown" the caller already handles.
		return icmpEchoLatency(server, 3*time.Second)
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

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
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
	}
}

// streamTraffic runs one connection to the /traffic endpoint and returns when
// it ends — because the session was cancelled, or because it broke.
func (s *AppService) streamTraffic(ctx context.Context) error {
	// Snapshot the current session secret (protected by stateMu)
	s.stateMu.Lock()
	secret := s.clashSecret
	s.stateMu.Unlock()

	client := &http.Client{Timeout: 0} // infinite timeout for stream
	req, err := http.NewRequestWithContext(ctx, "GET", "http://"+core.ClashAPIAddr+"/traffic", nil)
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
			wCtx := s.context()
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
