package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"NeoBox/backend/core"
)

// clashPayload builds a /connections body in the exact shape
// TrackerMetadata.MarshalJSON produces, so these tests break if sing-box
// changes the contract rather than silently decoding into zero values.
func clashPayload(conns ...map[string]interface{}) []byte {
	if conns == nil {
		conns = []map[string]interface{}{}
	}
	payload, err := json.Marshal(map[string]interface{}{
		"downloadTotal": 0,
		"uploadTotal":   0,
		"memory":        0,
		"connections":   conns,
	})
	if err != nil {
		panic(err)
	}
	return payload
}

func clashConn(id string, overrides map[string]interface{}) map[string]interface{} {
	meta := map[string]interface{}{
		"network":         "tcp",
		"type":            "mixed",
		"sourceIP":        "127.0.0.1",
		"destinationIP":   "203.0.113.10",
		"sourcePort":      "51000",
		"destinationPort": "443",
		"host":            "api.example.com",
		"dnsMode":         "normal",
		"processPath":     `C:\Program Files\Google\Chrome\Application\chrome.exe (Dvarais)`,
	}
	conn := map[string]interface{}{
		"id":          id,
		"metadata":    meta,
		"upload":      int64(1024),
		"download":    int64(4096),
		"start":       time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC),
		"chains":      []string{"proxy"},
		"rule":        "domain_suffix=example.com => route",
		"rulePayload": "",
	}
	for k, v := range overrides {
		if sub, ok := v.(map[string]interface{}); ok && k == "metadata" {
			for mk, mv := range sub {
				meta[mk] = mv
			}
			continue
		}
		conn[k] = v
	}
	return conn
}

func decode(t *testing.T, payload []byte) ([]connectionRow, int) {
	t.Helper()
	rows, total, err := decodeConnections(payload)
	if err != nil {
		t.Fatalf("decodeConnections failed: %v", err)
	}
	return rows, total
}

// ─── Mapping ────────────────────────────────────────────────────────────────

func TestDecodeConnectionsMapsTheClashPayload(t *testing.T) {
	rows, total := decode(t, clashPayload(clashConn("abc-123", nil)))

	if total != 1 || len(rows) != 1 {
		t.Fatalf("got %d rows and total %d, want 1 and 1", len(rows), total)
	}
	row := rows[0]

	for name, tc := range map[string]struct{ got, want string }{
		"id":       {row.ID, "abc-123"},
		"host":     {row.Host, "api.example.com"},
		"dest ip":  {row.DestIP, "203.0.113.10"},
		"port":     {row.Port, "443"},
		"network":  {row.Network, "tcp"},
		"rule":     {row.Rule, "domain_suffix=example.com => route"},
		"outbound": {row.Outbound, "proxy"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", name, tc.got, tc.want)
		}
	}

	// The full path and the " (username)" sing-box appends are both noise in a
	// table cell; what identifies the application is the file name.
	if row.Process != "chrome.exe" {
		t.Errorf("process = %q, want chrome.exe", row.Process)
	}
	if row.Upload != 1024 || row.Download != 4096 {
		t.Errorf("counters = (%d, %d), want (1024, 4096)", row.Upload, row.Download)
	}
	want := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC).UnixMilli()
	if row.StartMs != want {
		t.Errorf("startMs = %d, want %d", row.StartMs, want)
	}
}

func TestDecodeConnectionsNormalisesTheProcessField(t *testing.T) {
	for name, tc := range map[string]struct{ path, want string }{
		"windows path with user": {`C:\Windows\System32\svchost.exe (SYSTEM)`, "svchost.exe"},
		"windows path with uid":  {`C:\app\game.exe (1000)`, "game.exe"},
		"bare path":              {`C:\app\game.exe`, "game.exe"},
		"bare uid only":          {"1000", "1000"},
		"empty":                  {"", ""},
		"parenthesis in name":    {`C:\app\my (old) app.exe`, "my (old) app.exe"},
	} {
		t.Run(name, func(t *testing.T) {
			rows, _ := decode(t, clashPayload(clashConn("id", map[string]interface{}{
				"metadata": map[string]interface{}{"processPath": tc.path},
			})))
			if rows[0].Process != tc.want {
				t.Errorf("process = %q, want %q", rows[0].Process, tc.want)
			}
		})
	}
}

// A connection the core reports with no chain at all still has to render; the
// outbound column is simply blank.
func TestDecodeConnectionsToleratesAnEmptyChain(t *testing.T) {
	rows, _ := decode(t, clashPayload(clashConn("id", map[string]interface{}{
		"chains": []string{},
		"rule":   "final",
	})))
	if rows[0].Outbound != "" {
		t.Errorf("outbound = %q, want empty", rows[0].Outbound)
	}
	if rows[0].Rule != "final" {
		t.Errorf("rule = %q, want the literal final", rows[0].Rule)
	}
}

// sing-box reverses the chain before emitting it, so the outbound that did the
// work is first and the rule-matched entry is last.
func TestDecodeConnectionsReportsTheOutboundThatCarriedTheConnection(t *testing.T) {
	rows, _ := decode(t, clashPayload(clashConn("id", map[string]interface{}{
		"chains": []string{"proxy", "auto-select"},
	})))
	if rows[0].Outbound != "proxy" {
		t.Errorf("outbound = %q, want proxy — the chain's first entry", rows[0].Outbound)
	}
}

// ─── Capping and ordering ───────────────────────────────────────────────────

func TestDecodeConnectionsCapsTheRowCount(t *testing.T) {
	const count = 1000
	conns := make([]map[string]interface{}, 0, count)
	for i := 0; i < count; i++ {
		conns = append(conns, clashConn(fmt.Sprintf("id-%d", i), nil))
	}

	rows, total := decode(t, clashPayload(conns...))

	if len(rows) != maxConnectionRows {
		t.Errorf("got %d rows, want the cap of %d", len(rows), maxConnectionRows)
	}
	// The pre-cap count is what lets the view say "200 of 1000" instead of
	// pretending the core only has 200 connections open.
	if total != count {
		t.Errorf("total = %d, want the pre-cap %d", total, count)
	}
}

// The view's DOM reconciler prepends arriving rows and never moves a surviving
// one. That is only correct while rows arrive newest first.
func TestDecodeConnectionsOrdersNewestFirst(t *testing.T) {
	base := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	rows, _ := decode(t, clashPayload(
		clashConn("older", map[string]interface{}{"start": base}),
		clashConn("newest", map[string]interface{}{"start": base.Add(2 * time.Minute)}),
		clashConn("middle", map[string]interface{}{"start": base.Add(time.Minute)}),
	))

	got := []string{rows[0].ID, rows[1].ID, rows[2].ID}
	want := []string{"newest", "middle", "older"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

// Capping happens after sorting: dropping arbitrary rows and then ordering what
// survived would hide the newest connections, which are the ones the user
// opened the view to see.
func TestDecodeConnectionsKeepsTheNewestRowsWhenCapping(t *testing.T) {
	base := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	conns := make([]map[string]interface{}, 0, maxConnectionRows+50)
	for i := 0; i < maxConnectionRows+50; i++ {
		conns = append(conns, clashConn(fmt.Sprintf("id-%d", i), map[string]interface{}{
			"start": base.Add(time.Duration(i) * time.Second),
		}))
	}

	rows, _ := decode(t, clashPayload(conns...))

	newest := base.Add(time.Duration(maxConnectionRows+49) * time.Second).UnixMilli()
	if rows[0].StartMs != newest {
		t.Errorf("first row started at %d, want the newest connection at %d", rows[0].StartMs, newest)
	}
	oldestKept := base.Add(50 * time.Second).UnixMilli()
	if rows[len(rows)-1].StartMs != oldestKept {
		t.Errorf("last row started at %d, want %d — the cap dropped the wrong end",
			rows[len(rows)-1].StartMs, oldestKept)
	}
}

// ─── Robustness ─────────────────────────────────────────────────────────────

// Anything that is not a Clash snapshot has to come back as an error rather
// than as a panic or as a half-built row. A proxy error page in place of the
// API is the realistic case.
func TestDecodeConnectionsRejectsAMalformedPayload(t *testing.T) {
	for name, payload := range map[string]string{
		"not json":      "<html>gateway timeout</html>",
		"truncated":     `{"connections":[{"id":`,
		"wrong shape":   `{"connections":"none"}`,
		"a bare number": `42`,
		"empty body":    ``,
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := decodeConnections([]byte(payload)); err == nil {
				t.Error("malformed payload decoded without an error")
			}
		})
	}
}

// Valid JSON that simply holds no connections is not an error — it is what an
// idle core returns, once a second, for as long as the view is open.
func TestDecodeConnectionsAcceptsAnEmptySnapshot(t *testing.T) {
	for name, payload := range map[string]string{
		"no connections":      string(clashPayload()),
		"connections is null": `{"connections":null}`,
		"empty object":        `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			rows, total, err := decodeConnections([]byte(payload))
			if err != nil {
				t.Fatalf("an empty snapshot produced an error: %v", err)
			}
			if len(rows) != 0 || total != 0 {
				t.Errorf("got %d rows and total %d, want an empty snapshot", len(rows), total)
			}
		})
	}
}

// A row with no id cannot be keyed by the reconciler and cannot be closed, so
// it is dropped rather than rendered half-built.
func TestDecodeConnectionsSkipsRowsWithoutAnID(t *testing.T) {
	rows, total := decode(t, clashPayload(
		clashConn("", nil),
		clashConn("kept", nil),
	))
	if len(rows) != 1 || rows[0].ID != "kept" {
		t.Fatalf("got %d rows %v, want only the one with an id", len(rows), rows)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1 — the skipped row must not be counted", total)
	}
}

// ─── The seam with core.SuggestRuleTarget ───────────────────────────────────

func TestDecodeConnectionsAttachesASuggestedRule(t *testing.T) {
	rows, _ := decode(t, clashPayload(clashConn("id", nil)))
	if rows[0].RuleType != "domain_suffix" || rows[0].RuleValue != "api.example.com" {
		t.Errorf("suggested rule = (%q, %q), want (domain_suffix, api.example.com)",
			rows[0].RuleType, rows[0].RuleValue)
	}
}

func TestDecodeConnectionsFallsBackToTheAddressWithoutAHost(t *testing.T) {
	rows, _ := decode(t, clashPayload(clashConn("id", map[string]interface{}{
		"metadata": map[string]interface{}{"host": ""},
	})))
	if rows[0].RuleType != "ip_cidr" || rows[0].RuleValue != "203.0.113.10/32" {
		t.Errorf("suggested rule = (%q, %q), want (ip_cidr, 203.0.113.10/32)",
			rows[0].RuleType, rows[0].RuleValue)
	}
}

// An empty ruleType is the signal the view uses to disable the rule buttons for
// a row. Suggesting anything here would preempt ip_is_private → direct.
func TestDecodeConnectionsOmitsARuleWhereNoneIsSafe(t *testing.T) {
	rows, _ := decode(t, clashPayload(clashConn("id", map[string]interface{}{
		"metadata": map[string]interface{}{"host": "", "destinationIP": "192.168.1.1"},
	})))
	if rows[0].RuleType != "" || rows[0].RuleValue != "" {
		t.Errorf("suggested rule = (%q, %q) for a private address, want no suggestion",
			rows[0].RuleType, rows[0].RuleValue)
	}
}

// ─── The HTTP call ──────────────────────────────────────────────────────────

func TestFetchClashSendsTheSessionSecret(t *testing.T) {
	var gotAuth, gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		gotMethod = r.Method
		w.Write(clashPayload())
	}))
	defer srv.Close()

	addr := strings.TrimPrefix(srv.URL, "http://")
	if _, err := fetchClash(context.Background(), http.MethodGet, addr, "/connections", "s3cr3t"); err != nil {
		t.Fatalf("fetchClash failed: %v", err)
	}

	if gotAuth != "Bearer s3cr3t" {
		t.Errorf("Authorization = %q, want Bearer s3cr3t", gotAuth)
	}
	if gotPath != "/connections" || gotMethod != http.MethodGet {
		t.Errorf("request = %s %s, want GET /connections", gotMethod, gotPath)
	}
}

// The Clash API answers 401 when the secret is wrong. That has to surface as an
// error rather than as a body the decoder would choke on later.
func TestFetchClashTreatsARejectionAsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	addr := strings.TrimPrefix(srv.URL, "http://")
	if _, err := fetchClash(context.Background(), http.MethodGet, addr, "/connections", "wrong"); err == nil {
		t.Error("a 401 decoded as success")
	}
}

// This call blocks a UI tick, so a core that has stopped answering must not
// turn into a frontend that has stopped answering.
func TestFetchClashGivesUpOnASlowCore(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer func() {
		close(release)
		srv.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	addr := strings.TrimPrefix(srv.URL, "http://")
	done := make(chan error, 1)
	go func() {
		_, err := fetchClash(ctx, http.MethodGet, addr, "/connections", "s")
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Error("a request that never got a response returned success")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("fetchClash hung past its context deadline")
	}
}

// ─── The bound methods ──────────────────────────────────────────────────────

func TestGetConnectionsReturnsNothingWhileTheCoreIsStopped(t *testing.T) {
	s := &AppService{coreManager: core.NewCoreManager()}

	snapshot := s.GetConnections()

	if running, _ := snapshot["running"].(bool); running {
		t.Error("running = true with no core started")
	}
	rows, ok := snapshot["connections"].([]connectionRow)
	if !ok {
		t.Fatalf("connections has type %T, want []connectionRow", snapshot["connections"])
	}
	if len(rows) != 0 {
		t.Errorf("got %d rows from a stopped core", len(rows))
	}
	// An empty slice rather than nil: the frontend iterates it without a guard.
	if rows == nil {
		t.Error("connections is nil, want an empty slice")
	}
}

func TestCloseConnectionRefusesWhatItCannotSend(t *testing.T) {
	s := &AppService{coreManager: core.NewCoreManager()}

	for name, id := range map[string]string{
		"empty":            "",
		"whitespace":       "   ",
		"path traversal":   "../configs",
		"query injection":  "abc?force=true",
		"fragment":         "abc#x",
		"percent encoding": "abc%2f",
	} {
		t.Run(name, func(t *testing.T) {
			if s.CloseConnection(id) {
				t.Errorf("CloseConnection(%q) reported success", id)
			}
		})
	}
}
