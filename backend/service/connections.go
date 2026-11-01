package service

import (
	"bytes"
	"cmp"
	"container/heap"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"slices"
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

// ConnectionsPayload represents the typed Wails IPC response for GetConnections.
type ConnectionsPayload struct {
	Running     bool            `json:"running"`
	Connections []connectionRow `json:"connections"`
	Total       int             `json:"total"`
}

// GetConnections returns a snapshot of the connections the core currently has
// open, newest first and capped at maxConnectionRows.
func (s *AppService) GetConnections() ConnectionsPayload {
	empty := ConnectionsPayload{
		Running:     false,
		Connections: []connectionRow{},
		Total:       0,
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

	rows, total, err := s.fetchAndDecodeConnections(ctx, secret)
	if err != nil {
		// A failure here is nearly always a core that is shutting down between
		// the IsRunning check and the request. The view polls again in a second;
		// there is nothing worth reporting to the user.
		return empty
	}

	return ConnectionsPayload{
		Running:     true,
		Connections: rows,
		Total:       total,
	}
}

// fetchAndDecodeConnections streams and decodes live connections from the Clash API.
func (s *AppService) fetchAndDecodeConnections(ctx context.Context, secret string) ([]connectionRow, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+core.ClashAPIAddr+"/connections", nil)
	if err != nil {
		return nil, 0, err
	}
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}

	resp, err := clashClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		b, _ := io.ReadAll(resp.Body)
		return nil, 0, fmt.Errorf("clash api GET /connections: %s (response: %s)", resp.Status, string(b))
	}

	return decodeConnectionsReader(resp.Body)
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

// fetchClashWithBody performs one authenticated request against the core's Clash API
// with an optional body and returns the response body.
func fetchClashWithBody(ctx context.Context, method, addr, path, secret string, body io.Reader) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, "http://"+addr+path, body)
	if err != nil {
		return nil, err
	}
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := clashClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("clash api %s %s: %s (response: %s)", method, path, resp.Status, string(b))
	}
	return io.ReadAll(resp.Body)
}

// fetchClash performs one authenticated request against the core's Clash API
// and returns the body.
//
// The per-session secret is sent as a Bearer token and never leaves Go: the
// frontend has no way to reach 127.0.0.1:9097 itself — the page's CSP does not
// allow it — and no reason to hold the credential that guards the core.
func fetchClash(ctx context.Context, method, addr, path, secret string) ([]byte, error) {
	return fetchClashWithBody(ctx, method, addr, path, secret, nil)
}

// candidateRef holds connection index and millisecond timestamp for Top-K selection.
type candidateRef struct {
	index   int
	startMs int64
}

type minCandidateHeap []candidateRef

func (h minCandidateHeap) Len() int           { return len(h) }
func (h minCandidateHeap) Less(i, j int) bool { return h[i].startMs < h[j].startMs }
func (h minCandidateHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *minCandidateHeap) Push(x any)        { *h = append(*h, x.(candidateRef)) }
func (h *minCandidateHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}

// decodeConnections turns a /connections payload into rows for the view. It
// delegates to decodeConnectionsReader for backward-compatibility with tests.
func decodeConnections(payload []byte) ([]connectionRow, int, error) {
	return decodeConnectionsReader(bytes.NewReader(payload))
}

// decodeConnectionsReader streams JSON from r, extracts the newest maxConnectionRows
// using a Min-Heap, and computes rule suggestions only for surviving rows.
func decodeConnectionsReader(r io.Reader) ([]connectionRow, int, error) {
	var snapshot clashSnapshot
	if err := json.NewDecoder(r).Decode(&snapshot); err != nil {
		return nil, 0, err
	}

	totalConns := len(snapshot.Connections)
	if totalConns == 0 {
		return []connectionRow{}, 0, nil
	}

	// Fast path: connection count is within maxConnectionRows
	if totalConns <= maxConnectionRows {
		rows := make([]connectionRow, 0, totalConns)
		for _, conn := range snapshot.Connections {
			if conn.ID == "" {
				continue
			}
			rows = append(rows, mapConnectionRow(conn))
		}
		slices.SortFunc(rows, func(a, b connectionRow) int {
			return cmp.Compare(b.StartMs, a.StartMs) // newest first
		})
		return rows, len(rows), nil
	}

	// Heavy path (> maxConnectionRows): use a Top-K Min-Heap to select only
	// the newest connections without computing SuggestRuleTarget on discarded rows.
	h := make(minCandidateHeap, 0, maxConnectionRows)
	validCount := 0

	for i, conn := range snapshot.Connections {
		if conn.ID == "" {
			continue
		}
		validCount++
		var startMs int64
		if !conn.Start.IsZero() {
			startMs = conn.Start.UnixMilli()
		}

		if h.Len() < maxConnectionRows {
			heap.Push(&h, candidateRef{index: i, startMs: startMs})
		} else if startMs > h[0].startMs {
			h[0] = candidateRef{index: i, startMs: startMs}
			heap.Fix(&h, 0)
		}
	}

	if validCount == 0 {
		return []connectionRow{}, 0, nil
	}

	// Pop from Min-Heap gives items ascending from smallest to largest.
	// Placing them from len(h)-1 down to 0 produces descending order (newest first)
	// without requiring an extra sorting pass.
	rows := make([]connectionRow, len(h))
	for i := len(h) - 1; i >= 0; i-- {
		top := heap.Pop(&h).(candidateRef)
		rows[i] = mapConnectionRow(snapshot.Connections[top.index])
	}

	return rows, validCount, nil
}

func mapConnectionRow(conn clashConnection) connectionRow {
	meta := conn.Metadata
	ruleType, ruleValue, _ := core.SuggestRuleTarget(meta.Host, meta.DestinationIP)

	var startMs int64
	if !conn.Start.IsZero() {
		startMs = conn.Start.UnixMilli()
	}

	return connectionRow{
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
	}
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
