package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"NeoBox/backend/core"
)

// Live connections, read from the core's Clash API.
//
// This is a pull, not a push: the Connections view calls GetConnections on a
// timer while it is the visible view and the core is running. That is unlike
// the traffic monitor next door in vpn.go, and deliberately so.
//
// GET /connections is not a stream. sing-box only streams it over a WebSocket
// upgrade; a plain request renders one snapshot and closes the body, so the
// json.Decoder loop startTrafficMonitor uses would decode a single object and
// exit. Given that a request per sample is unavoidable, having the frontend
// drive the timer removes everything a pushed version would need: a goroutine
// to cancel in StopXray and RestartXray, a context.CancelFunc and a field on
// AppService to hold it, and a separate RPC for the view to say whether anybody
// is looking. Here the caller *is* the view, so it stops asking when it closes,
// and nothing has to be torn down when the session ends.
//
// The other half of the reason is the cost on the core side: Snapshot() calls
// runtime.ReadMemStats, which stops the world in this process, on every single
// request. One sample a second is the ceiling, and the frontend enforces it.

const (
	// maxConnectionRows caps how many rows cross into WebView2. It mirrors
	// MAX_CONNECTION_ROWS in renderer.ts for the same reason maxBufferedLogLines
	// mirrors MAX_LOG_ENTRIES: the UI never renders more than this, so shipping
	// more would only be bytes we are about to discard. The cap is not
	// cosmetic — the NAT-PMP flood documented in config.go opened thousands of
	// connections in seconds, and that is exactly when someone opens this view.
	maxConnectionRows = 200

	// clashRequestTimeout bounds a single Clash API call. Finite, unlike the
	// traffic monitor's client: this request is holding up a UI tick, not
	// carrying a long-lived stream, and a core that has wedged must not turn
	// into a frontend that has wedged.
	clashRequestTimeout = 2 * time.Second
)

// clashClient is shared by every call. At one request a second a fresh
// http.Client per call would open a new TCP connection each time and leave the
// old one in TIME_WAIT; one client keeps a single keep-alive connection to the
// loopback API instead.
var clashClient = &http.Client{Timeout: clashRequestTimeout}

// connectionRow is one live connection, trimmed to what the view draws.
//
// The Clash payload carries a good deal more per connection — source address
// and port, the inbound descriptor, dnsMode, rulePayload, the full outbound
// chain. None of it appears on screen, and all of it would be paid for once a
// second in a single ExecuteScript into WebView2, so it is dropped here rather
// than in the renderer.
type connectionRow struct {
	ID      string `json:"id"`
	Host    string `json:"host"`
	DestIP  string `json:"destIP"`
	Port    string `json:"destPort"`
	Network string `json:"network"`
	Process string `json:"process"`
	Rule    string `json:"rule"`
	// Outbound is the outbound that actually carried the connection.
	Outbound string `json:"outbound"`
	Upload   int64  `json:"upload"`
	Download int64  `json:"download"`
	// StartMs is unix milliseconds rather than the RFC 3339 string sing-box
	// emits: shorter on the wire, and the view recomputes the age on every tick
	// anyway.
	StartMs int64 `json:"startMs"`
	// RuleType and RuleValue are the CustomRule this row could become, or empty
	// when no safe rule exists for it (see core.SuggestRuleTarget). Empty means
	// the view must offer no rule buttons for the row.
	RuleType  string `json:"ruleType"`
	RuleValue string `json:"ruleValue"`
}

// clashConnection mirrors the parts of TrackerMetadata.MarshalJSON this code
// reads. Fields the view does not use are simply absent and discarded by
// encoding/json.
type clashConnection struct {
	ID       string `json:"id"`
	Metadata struct {
		Network         string `json:"network"`
		Host            string `json:"host"`
		DestinationIP   string `json:"destinationIP"`
		DestinationPort string `json:"destinationPort"`
		ProcessPath     string `json:"processPath"`
	} `json:"metadata"`
	Upload   int64     `json:"upload"`
	Download int64     `json:"download"`
	Start    time.Time `json:"start"`
	Chains   []string  `json:"chains"`
	Rule     string    `json:"rule"`
}

type clashSnapshot struct {
	Connections []clashConnection `json:"connections"`
}

// GetConnections returns a snapshot of the connections the core currently has
// open, newest first and capped at maxConnectionRows.
//
// The shape is map[string]interface{} to match CheckUpdates and StartXray;
// frontend/modules/api.ts is the hand-written description of it.
func (s *AppService) GetConnections() map[string]interface{} {
	empty := map[string]interface{}{
		"running":     false,
		"connections": []connectionRow{},
		"total":       0,
	}

	// StopXray does not clear clashSecret, so without this gate a stopped
	// session would keep firing a doomed request every second.
	if !s.coreManager.IsRunning() {
		return empty
	}

	// One lock, taken and released before any I/O: the HTTP call below must not
	// happen while stateMu is held. See the locking note on AppService.
	s.stateMu.Lock()
	secret := s.clashSecret
	s.stateMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), clashRequestTimeout)
	defer cancel()

	payload, err := fetchClash(ctx, http.MethodGet, core.ClashAPIAddr, "/connections", secret)
	if err != nil {
		// A failure here is nearly always a core that is shutting down between
		// the IsRunning check and the request. The view polls again in a second;
		// there is nothing worth reporting to the user.
		return empty
	}

	rows, total, err := decodeConnections(payload)
	if err != nil {
		return empty
	}

	return map[string]interface{}{
		"running":     true,
		"connections": rows,
		"total":       total,
	}
}

// CloseConnection asks the core to tear down one live connection. It reports
// whether the core accepted the request.
//
// This pairs with the block action: adding a rule changes nothing for
// connections that are already open, so a block only feels immediate if the
// user can also cut what is running.
func (s *AppService) CloseConnection(id string) bool {
	if strings.TrimSpace(id) == "" || !s.coreManager.IsRunning() {
		return false
	}

	s.stateMu.Lock()
	secret := s.clashSecret
	s.stateMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), clashRequestTimeout)
	defer cancel()

	// The id comes back from a row this same file produced, but it is still
	// interpolated into a URL path, so anything that could change the path's
	// shape disqualifies it.
	if strings.ContainsAny(id, "/?#%") {
		return false
	}

	_, err := fetchClash(ctx, http.MethodDelete, core.ClashAPIAddr, "/connections/"+id, secret)
	return err == nil
}

// fetchClash performs one authenticated request against the core's Clash API
// and returns the body.
//
// The per-session secret is sent as a Bearer token and never leaves Go: the
// frontend has no way to reach 127.0.0.1:9097 itself — the page's CSP does not
// allow it — and no reason to hold the credential that guards the core.
func fetchClash(ctx context.Context, method, addr, path, secret string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, "http://"+addr+path, nil)
	if err != nil {
		return nil, err
	}
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}

	resp, err := clashClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("clash api %s %s: %s", method, path, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// decodeConnections turns a /connections payload into rows for the view. It
// returns the rows it kept and the number of connections the core reported, so
// the UI can say "200 of 5374" rather than quietly lying about the total.
func decodeConnections(payload []byte) ([]connectionRow, int, error) {
	var snapshot clashSnapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return nil, 0, err
	}

	rows := make([]connectionRow, 0, len(snapshot.Connections))
	for _, conn := range snapshot.Connections {
		// A row with no id cannot be keyed by the view's reconciler and cannot
		// be closed. Skipping it beats rendering half a row.
		if conn.ID == "" {
			continue
		}
		meta := conn.Metadata
		ruleType, ruleValue, _ := core.SuggestRuleTarget(meta.Host, meta.DestinationIP)

		var startMs int64
		if !conn.Start.IsZero() {
			startMs = conn.Start.UnixMilli()
		}

		rows = append(rows, connectionRow{
			ID:        conn.ID,
			Host:      meta.Host,
			DestIP:    meta.DestinationIP,
			Port:      meta.DestinationPort,
			Network:   meta.Network,
			Process:   processName(meta.ProcessPath),
			Rule:      conn.Rule,
			Outbound:  finalOutbound(conn.Chains),
			Upload:    conn.Upload,
			Download:  conn.Download,
			StartMs:   startMs,
			RuleType:  ruleType,
			RuleValue: ruleValue,
		})
	}

	// Newest first: it is what someone watching live connections expects, and
	// it is what lets the view prepend arriving rows without ever moving one
	// that is already on screen.
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].StartMs > rows[j].StartMs })

	total := len(rows)
	if len(rows) > maxConnectionRows {
		rows = rows[:maxConnectionRows]
	}
	return rows, total, nil
}

// processName reduces the process descriptor to something that fits a table
// cell. sing-box reports a full path and appends " (username)" or " (uid)";
// what identifies the application is the file name, and the full path is still
// in the logs for anyone who needs it.
func processName(processPath string) string {
	name := strings.TrimSpace(processPath)
	if name == "" {
		return ""
	}
	if open := strings.LastIndex(name, " ("); open > 0 && strings.HasSuffix(name, ")") {
		name = name[:open]
	}
	// The path is Windows-shaped here, but a bare uid can arrive when there is
	// no process information at all, and that has no separator to split on.
	if base := filepath.Base(name); base != "." && base != string(filepath.Separator) {
		name = base
	}
	return name
}

// finalOutbound returns the outbound that actually carried the connection.
//
// sing-box walks outward from the outbound the rule matched, following any
// group to whatever it currently points at, then reverses the result — so the
// innermost outbound, the one that did the work, ends up first and the
// rule-matched entry last. NeoBox emits no outbound groups today, which makes
// the chain one element long and the distinction invisible; it would stop being
// invisible the moment a selector or urltest group appears.
func finalOutbound(chains []string) string {
	if len(chains) == 0 {
		return ""
	}
	return chains[0]
}
