package core

import (
	"encoding/json"
	"net/netip"
	"os"
	"testing"
)

// testServerIP is a TEST-NET-3 literal (RFC 5737). Using an IP rather than a
// hostname keeps these tests offline and deterministic: GenerateConfig only
// performs a DNS lookup when the server is not already an IP.
const testServerIP = "203.0.113.10"

func testOutbound() map[string]interface{} {
	return map[string]interface{}{
		"type":        "vless",
		"tag":         "proxy",
		"server":      testServerIP,
		"server_port": 443,
	}
}

func generate(t *testing.T, s Settings) map[string]interface{} {
	t.Helper()
	cfg, err := GenerateConfig(testOutbound(), s, false, "cache.db", "secret")
	if err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
	}
	return cfg
}

func routeSection(t *testing.T, cfg map[string]interface{}) map[string]interface{} {
	t.Helper()
	route, ok := cfg["route"].(map[string]interface{})
	if !ok {
		t.Fatal("config has no route section")
	}
	return route
}

func routeRules(t *testing.T, cfg map[string]interface{}) []map[string]interface{} {
	t.Helper()
	rules, ok := routeSection(t, cfg)["rules"].([]map[string]interface{})
	if !ok {
		t.Fatal("route section has no rules")
	}
	return rules
}

func dnsRules(t *testing.T, cfg map[string]interface{}) []map[string]interface{} {
	t.Helper()
	dns, ok := cfg["dns"].(map[string]interface{})
	if !ok {
		t.Fatal("config has no dns section")
	}
	rules, ok := dns["rules"].([]map[string]interface{})
	if !ok {
		t.Fatal("dns section has no rules")
	}
	return rules
}

// indexOfRule returns the position of the first rule matching pred, or -1.
// Position matters: sing-box evaluates route rules in order and the first match
// wins, so several of the guarantees below are about ordering, not presence.
func indexOfRule(rules []map[string]interface{}, pred func(map[string]interface{}) bool) int {
	for i, r := range rules {
		if pred(r) {
			return i
		}
	}
	return -1
}

func hasKeyValue(rule map[string]interface{}, key string, value interface{}) bool {
	got, ok := rule[key]
	if !ok {
		return false
	}
	return equalJSON(got, value)
}

// equalJSON compares two values structurally. The config is built from
// interface{} maps that end up as JSON, so comparing their encodings is both
// simpler and closer to what sing-box actually receives.
func equalJSON(a, b interface{}) bool {
	ja, err1 := json.Marshal(a)
	jb, err2 := json.Marshal(b)
	if err1 != nil || err2 != nil {
		return false
	}
	return string(ja) == string(jb)
}

// ─── Contract ───────────────────────────────────────────────────────────────

func TestGenerateConfigRequiresTag(t *testing.T) {
	for name, outbound := range map[string]map[string]interface{}{
		"missing tag": {"type": "vless", "server": testServerIP},
		"empty tag":   {"type": "vless", "tag": "", "server": testServerIP},
		"non-string":  {"type": "vless", "tag": 42, "server": testServerIP},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := GenerateConfig(outbound, Settings{}, false, "cache.db", "s"); err == nil {
				t.Error("expected an error when the outbound tag is unusable")
			}
		})
	}
}

// The caller's map is reused elsewhere (PingServer parses the same link), so
// GenerateConfig must not write into it.
func TestGenerateConfigDoesNotMutateCallerOutbound(t *testing.T) {
	outbound := testOutbound()
	before, err := json.Marshal(outbound)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	if _, err := GenerateConfig(outbound, Settings{TunMode: true}, true, "cache.db", "s"); err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
	}

	after, err := json.Marshal(outbound)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("GenerateConfig mutated the caller's outbound map:\n before: %s\n after:  %s", before, after)
	}
}

// ─── IPv6 leak protection ───────────────────────────────────────────────────

func isIPv6Reject(r map[string]interface{}) bool {
	return hasKeyValue(r, "ip_version", 6) && hasKeyValue(r, "action", "reject")
}

// Absent from settings.json must mean protected. The setting is a *bool
// precisely so "not configured" is distinguishable from "explicitly disabled";
// getting that backwards would silently leak IPv6 for every existing user.
func TestIPv6LeakProtectionDefaultsOn(t *testing.T) {
	cfg := generate(t, Settings{})
	if indexOfRule(routeRules(t, cfg), isIPv6Reject) < 0 {
		t.Error("IPv6 traffic is not rejected when ipv6Leak is unset — IPv6 would bypass the tunnel")
	}
}

func TestIPv6LeakProtectionRespectsExplicitFalse(t *testing.T) {
	off := false
	cfg := generate(t, Settings{Ipv6LeakProtection: &off})
	if indexOfRule(routeRules(t, cfg), isIPv6Reject) >= 0 {
		t.Error("IPv6 is still rejected even though the user disabled the protection")
	}
}

func TestIPv6LeakProtectionExplicitTrue(t *testing.T) {
	on := true
	cfg := generate(t, Settings{Ipv6LeakProtection: &on})
	if indexOfRule(routeRules(t, cfg), isIPv6Reject) < 0 {
		t.Error("IPv6 is not rejected even though the protection is enabled")
	}
}

// Ordering guarantee, not a presence one: sing-box takes the first matching
// rule. If the IPv6 reject came first, a DNS query carried over IPv6 would be
// rejected instead of hijacked, and the resolver would fall back outside the
// tunnel — a DNS leak. The comment in GenerateConfig says so; this test is what
// keeps a future edit from reordering them anyway.
func TestDNSHijackPrecedesIPv6Reject(t *testing.T) {
	rules := routeRules(t, generate(t, Settings{}))

	port53 := indexOfRule(rules, func(r map[string]interface{}) bool {
		return hasKeyValue(r, "action", "hijack-dns") && hasKeyValue(r, "port", []int{53})
	})
	protoDNS := indexOfRule(rules, func(r map[string]interface{}) bool {
		return hasKeyValue(r, "action", "hijack-dns") && hasKeyValue(r, "protocol", "dns")
	})
	reject := indexOfRule(rules, isIPv6Reject)

	if port53 < 0 || protoDNS < 0 {
		t.Fatalf("DNS hijack rules are missing (port53=%d, protocol=%d)", port53, protoDNS)
	}
	if reject < 0 {
		t.Fatal("IPv6 reject rule is missing")
	}
	if port53 > reject || protoDNS > reject {
		t.Errorf("DNS hijack must precede the IPv6 reject (port53=%d, protocol=%d, reject=%d)",
			port53, protoDNS, reject)
	}
}

// ─── DNS leak protection ────────────────────────────────────────────────────

func TestDNSLeakProtectionDefaultsToTunnel(t *testing.T) {
	cfg := generate(t, Settings{})
	if got := routeSection(t, cfg)["default_domain_resolver"]; got != "dns-remote" {
		t.Errorf("default_domain_resolver = %v, want dns-remote so lookups stay inside the tunnel", got)
	}
}

func TestDNSLeakProtectionRespectsExplicitFalse(t *testing.T) {
	off := false
	cfg := generate(t, Settings{DnsLeakProtection: &off})
	if got := routeSection(t, cfg)["default_domain_resolver"]; got != "dns-direct" {
		t.Errorf("default_domain_resolver = %v, want dns-direct when the user disabled the protection", got)
	}
}

// The remote resolver is only leak-proof if its own traffic is detoured through
// the proxy. A detour pointing anywhere else would send every lookup in the
// clear while the UI still reported DNS leak protection as on.
func TestRemoteDNSIsDetouredThroughTheProxy(t *testing.T) {
	cfg := generate(t, Settings{})

	dns, ok := cfg["dns"].(map[string]interface{})
	if !ok {
		t.Fatal("config has no dns section")
	}
	servers, ok := dns["servers"].([]map[string]interface{})
	if !ok {
		t.Fatal("dns section has no servers")
	}

	found := false
	for _, srv := range servers {
		if srv["tag"] != "dns-remote" {
			continue
		}
		found = true
		if got := srv["detour"]; got != "proxy" {
			t.Errorf("dns-remote detour = %v, want the outbound tag %q", got, "proxy")
		}
		if got := srv["type"]; got != "https" {
			t.Errorf("dns-remote type = %v, want https (plain DNS would be observable)", got)
		}
	}
	if !found {
		t.Error("there is no dns-remote server")
	}

	if got := dns["final"]; got != "dns-remote" {
		t.Errorf("dns final = %v, want dns-remote", got)
	}
	if got := dns["strategy"]; got != "ipv4_only" {
		t.Errorf("dns strategy = %v, want ipv4_only", got)
	}
}

// Without fakeip, AAAA lookups must still be steered at the remote resolver so
// they cannot escape the tunnel.
func TestAAAAQueriesRoutedRemotelyWhenIPv6ProtectionOn(t *testing.T) {
	cfg := generate(t, Settings{})

	idx := indexOfRule(dnsRules(t, cfg), func(r map[string]interface{}) bool {
		return hasKeyValue(r, "query_type", []string{"AAAA"}) && hasKeyValue(r, "server", "dns-remote")
	})
	if idx < 0 {
		t.Error("AAAA queries are not routed through dns-remote when IPv6 leak protection is on")
	}
}

// ─── Server exemption ───────────────────────────────────────────────────────

// The proxy server itself must be reachable outside the tunnel, otherwise the
// tunnel would have to carry the connection that establishes it.
func TestServerIsExcludedFromTheTunnel(t *testing.T) {
	cfg := generate(t, Settings{})

	idx := indexOfRule(routeRules(t, cfg), func(r map[string]interface{}) bool {
		return hasKeyValue(r, "ip_cidr", []string{testServerIP + "/32"}) &&
			hasKeyValue(r, "outbound", "direct")
	})
	if idx < 0 {
		t.Fatalf("no direct route for the VPN server %s — the tunnel would carry its own transport", testServerIP)
	}

	// It must also be exempt in DNS, and as an ip_cidr rather than a domain:
	// an IP literal in the domain field never matches anything.
	dnsIdx := indexOfRule(dnsRules(t, cfg), func(r map[string]interface{}) bool {
		return hasKeyValue(r, "ip_cidr", []string{testServerIP + "/32"}) &&
			hasKeyValue(r, "server", "dns-direct")
	})
	if dnsIdx < 0 {
		t.Error("the VPN server IP is not exempt in the DNS rules")
	}
}

// The outbound resolves its own server address directly; going through
// dns-remote would require the tunnel that is not up yet.
func TestOutboundResolvesItsServerDirectly(t *testing.T) {
	cfg := generate(t, Settings{})

	outbounds, ok := cfg["outbounds"].([]interface{})
	if !ok || len(outbounds) == 0 {
		t.Fatal("config has no outbounds")
	}
	proxy, ok := outbounds[0].(map[string]interface{})
	if !ok {
		t.Fatal("first outbound is not a map")
	}
	if got := proxy["domain_resolver"]; got != "dns-direct" {
		t.Errorf("outbound domain_resolver = %v, want dns-direct", got)
	}
}

// ─── FakeIP ─────────────────────────────────────────────────────────────────

func isFakeIPRoute(r map[string]interface{}) bool {
	return hasKeyValue(r, "ip_cidr", []string{"198.18.0.0/15"})
}

// The fakeip range catches every synthetic address, so a rule matching it must
// come last among the exemptions. Placed earlier it would swallow the direct
// routes for the VPN server, the user's direct domains and split tunneling.
func TestFakeIPRuleComesAfterExclusions(t *testing.T) {
	cfg := generate(t, Settings{
		FakeDns:              true,
		TunMode:              true,
		CustomDirect:         []string{"example.com"},
		ProcessMode:          "blacklist",
		ProcessListBlacklist: []string{"game.exe"},
	})
	rules := routeRules(t, cfg)

	fakeIdx := indexOfRule(rules, isFakeIPRoute)
	if fakeIdx < 0 {
		t.Fatal("the fakeip route rule is missing with FakeDns+TunMode")
	}

	later := map[string]int{
		"server exemption": indexOfRule(rules, func(r map[string]interface{}) bool {
			return hasKeyValue(r, "ip_cidr", []string{testServerIP + "/32"})
		}),
		"custom direct domains": indexOfRule(rules, func(r map[string]interface{}) bool {
			return hasKeyValue(r, "domain_suffix", []string{"example.com"})
		}),
		"split tunneling": indexOfRule(rules, func(r map[string]interface{}) bool {
			return hasKeyValue(r, "process_name", []string{"game.exe"})
		}),
		"private addresses": indexOfRule(rules, func(r map[string]interface{}) bool {
			return hasKeyValue(r, "ip_is_private", true)
		}),
	}
	for name, idx := range later {
		if idx < 0 {
			t.Errorf("%s rule is missing", name)
			continue
		}
		if idx > fakeIdx {
			t.Errorf("fakeip rule (index %d) precedes the %s rule (index %d) and would swallow it",
				fakeIdx, name, idx)
		}
	}
}

// fakeip only works with the TUN inbound; enabling it without TUN must not add
// a fake resolver that nothing can route.
func TestFakeIPIgnoredWithoutTunMode(t *testing.T) {
	cfg := generate(t, Settings{FakeDns: true, TunMode: false})

	if indexOfRule(routeRules(t, cfg), isFakeIPRoute) >= 0 {
		t.Error("a fakeip route rule was added without TUN mode")
	}

	dns := cfg["dns"].(map[string]interface{})
	servers := dns["servers"].([]map[string]interface{})
	for _, srv := range servers {
		if srv["tag"] == "dns-fake" {
			t.Error("a dns-fake server was added without TUN mode")
		}
	}
}

func TestFakeIPEnabledWithTunMode(t *testing.T) {
	cfg := generate(t, Settings{FakeDns: true, TunMode: true})

	dns := cfg["dns"].(map[string]interface{})
	servers := dns["servers"].([]map[string]interface{})
	found := false
	for _, srv := range servers {
		if srv["tag"] == "dns-fake" {
			found = true
			if got := srv["inet4_range"]; got != "198.18.0.0/15" {
				t.Errorf("dns-fake inet4_range = %v, want 198.18.0.0/15", got)
			}
		}
	}
	if !found {
		t.Error("no dns-fake server with FakeDns+TunMode")
	}

	idx := indexOfRule(dnsRules(t, cfg), func(r map[string]interface{}) bool {
		return hasKeyValue(r, "server", "dns-fake")
	})
	if idx < 0 {
		t.Error("no DNS rule routes queries to dns-fake")
	}
}

// ─── Inbounds ───────────────────────────────────────────────────────────────

func inbounds(t *testing.T, cfg map[string]interface{}) []map[string]interface{} {
	t.Helper()
	in, ok := cfg["inbounds"].([]map[string]interface{})
	if !ok {
		t.Fatal("config has no inbounds")
	}
	return in
}

func TestTunModeAddsTunInbound(t *testing.T) {
	withoutTun := inbounds(t, generate(t, Settings{}))
	for _, in := range withoutTun {
		if in["type"] == "tun" {
			t.Fatal("a tun inbound was added without TUN mode")
		}
	}

	withTun := inbounds(t, generate(t, Settings{TunMode: true}))
	var tun map[string]interface{}
	for _, in := range withTun {
		if in["type"] == "tun" {
			tun = in
		}
	}
	if tun == nil {
		t.Fatal("no tun inbound in TUN mode")
	}
	// strict_route is what stops traffic escaping around the interface.
	if got := tun["strict_route"]; got != true {
		t.Errorf("tun strict_route = %v, want true", got)
	}
	if got := tun["auto_route"]; got != true {
		t.Errorf("tun auto_route = %v, want true", got)
	}
	if got := tun["interface_name"]; got != "tun-neobox" {
		t.Errorf("tun interface_name = %v, want tun-neobox (CheckTunStatus looks for it)", got)
	}
}

func TestSystemProxyFlagIsPassedThrough(t *testing.T) {
	for _, want := range []bool{true, false} {
		cfg, err := GenerateConfig(testOutbound(), Settings{}, want, "cache.db", "s")
		if err != nil {
			t.Fatalf("GenerateConfig failed: %v", err)
		}
		if got := inbounds(t, cfg)[0]["set_system_proxy"]; got != want {
			t.Errorf("set_system_proxy = %v, want %v", got, want)
		}
	}
}

// ─── Split tunneling and custom rules ───────────────────────────────────────

// Whitelist mode routes the listed processes through the proxy and everything
// else direct, so it needs a catch-all after the process rule. Without it the
// mode would behave like a blacklist.
func TestWhitelistProcessModeAddsCatchAll(t *testing.T) {
	rules := routeRules(t, generate(t, Settings{
		TunMode:              true,
		ProcessMode:          "whitelist",
		ProcessListWhitelist: []string{"browser.exe"},
	}))

	procIdx := indexOfRule(rules, func(r map[string]interface{}) bool {
		return hasKeyValue(r, "process_name", []string{"browser.exe"}) &&
			hasKeyValue(r, "outbound", "proxy")
	})
	if procIdx < 0 {
		t.Fatal("whitelisted process is not routed through the proxy")
	}

	catchAll := -1
	for i := procIdx + 1; i < len(rules); i++ {
		_, hasDomain := rules[i]["domain"]
		_, hasIP := rules[i]["ip_cidr"]
		_, hasProc := rules[i]["process_name"]
		_, hasPrivate := rules[i]["ip_is_private"]
		if !hasDomain && !hasIP && !hasProc && !hasPrivate &&
			hasKeyValue(rules[i], "outbound", "direct") {
			catchAll = i
			break
		}
	}
	if catchAll < 0 {
		t.Error("whitelist mode has no direct catch-all after the process rule")
	}
}

func TestBlacklistProcessModeRoutesListedProcessesDirect(t *testing.T) {
	rules := routeRules(t, generate(t, Settings{
		TunMode:              true,
		ProcessMode:          "blacklist",
		ProcessListBlacklist: []string{"game.exe"},
	}))

	if indexOfRule(rules, func(r map[string]interface{}) bool {
		return hasKeyValue(r, "process_name", []string{"game.exe"}) &&
			hasKeyValue(r, "outbound", "direct")
	}) < 0 {
		t.Error("blacklisted process is not routed direct")
	}
}

// Split tunneling relies on process names, which sing-box can only see for
// traffic arriving through the TUN inbound.
func TestProcessRulesRequireTunMode(t *testing.T) {
	rules := routeRules(t, generate(t, Settings{
		TunMode:              false,
		ProcessMode:          "blacklist",
		ProcessListBlacklist: []string{"game.exe"},
	}))

	if indexOfRule(rules, func(r map[string]interface{}) bool {
		_, ok := r["process_name"]
		return ok
	}) >= 0 {
		t.Error("process rules were emitted without TUN mode, where they cannot match")
	}
}

func TestCustomRules(t *testing.T) {
	cfg := generate(t, Settings{CustomRules: []CustomRule{
		{Action: "direct", Type: "domain", Value: "a.example"},
		{Action: "block", Type: "domain_suffix", Value: "b.example"},
		{Action: "proxy", Type: "domain_keyword", Value: "keyword"},
		{Action: "direct", Type: "ip_cidr", Value: "10.1.2.0/24"},

		// Malformed entries must be skipped rather than emitted half-built.
		{Action: "", Type: "domain", Value: "no-action.example"},
		{Action: "direct", Type: "", Value: "no-type.example"},
		{Action: "direct", Type: "domain", Value: ""},
		{Action: "teleport", Type: "domain", Value: "bad-action.example"},
		{Action: "direct", Type: "wormhole", Value: "bad-type.example"},
	}})
	rules := routeRules(t, cfg)

	expected := []struct {
		key      string
		value    interface{}
		outbound string
	}{
		{"domain", []string{"a.example"}, "direct"},
		{"domain_suffix", []string{"b.example"}, "block"},
		{"domain_keyword", []string{"keyword"}, "proxy"},
		{"ip_cidr", []string{"10.1.2.0/24"}, "direct"},
	}
	for _, e := range expected {
		if indexOfRule(rules, func(r map[string]interface{}) bool {
			return hasKeyValue(r, e.key, e.value) && hasKeyValue(r, "outbound", e.outbound)
		}) < 0 {
			t.Errorf("custom rule %s=%v -> %s is missing", e.key, e.value, e.outbound)
		}
	}

	for _, bad := range []string{"no-action.example", "no-type.example", "bad-action.example", "bad-type.example"} {
		if indexOfRule(rules, func(r map[string]interface{}) bool {
			return hasKeyValue(r, "domain", []string{bad}) ||
				hasKeyValue(r, "domain_suffix", []string{bad}) ||
				hasKeyValue(r, "domain_keyword", []string{bad})
		}) >= 0 {
			t.Errorf("malformed custom rule %q was emitted", bad)
		}
	}
}

func TestCustomDirectDomainsAreTrimmedAndFiltered(t *testing.T) {
	cfg := generate(t, Settings{CustomDirect: []string{"  example.com  ", "", "   "}})

	idx := indexOfRule(routeRules(t, cfg), func(r map[string]interface{}) bool {
		return hasKeyValue(r, "domain_suffix", []string{"example.com"})
	})
	if idx < 0 {
		t.Error("custom direct domain was not trimmed into a usable rule")
	}
}

func TestBypassRuAddsRuleSets(t *testing.T) {
	cfg := generate(t, Settings{BypassRu: true})

	if indexOfRule(routeRules(t, cfg), func(r map[string]interface{}) bool {
		return hasKeyValue(r, "rule_set", []string{"geoip-ru", "geosite-ru"}) &&
			hasKeyValue(r, "outbound", "direct")
	}) < 0 {
		t.Error("BypassRu did not add a rule referencing the RU rule-sets")
	}

	sets, ok := routeSection(t, cfg)["rule_set"].([]map[string]interface{})
	if !ok {
		t.Fatal("BypassRu did not declare the rule_set sources")
	}
	for _, set := range sets {
		// The rule-set download must not go through the tunnel it configures.
		if got := set["download_detour"]; got != "direct" {
			t.Errorf("rule-set %v download_detour = %v, want direct", set["tag"], got)
		}
	}

	if _, ok := routeSection(t, generate(t, Settings{}))["rule_set"]; ok {
		t.Error("rule_set sources were declared without BypassRu")
	}
}

// ─── Clash API ──────────────────────────────────────────────────────────────

func TestClashSecretIsInjected(t *testing.T) {
	cfg, err := GenerateConfig(testOutbound(), Settings{}, false, "cache.db", "s3cr3t")
	if err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
	}

	experimental, ok := cfg["experimental"].(map[string]interface{})
	if !ok {
		t.Fatal("config has no experimental section")
	}
	clash, ok := experimental["clash_api"].(map[string]interface{})
	if !ok {
		t.Fatal("config has no clash_api section")
	}
	if got := clash["secret"]; got != "s3cr3t" {
		t.Errorf("clash_api secret = %v, want the per-session secret", got)
	}
}

// ─── Outbounds ──────────────────────────────────────────────────────────────

// "direct" and "block" are referenced by the routing rules above; a config
// missing either would be rejected by sing-box at startup.
func TestBaseOutboundsArePresent(t *testing.T) {
	cfg := generate(t, Settings{})

	outbounds, ok := cfg["outbounds"].([]interface{})
	if !ok {
		t.Fatal("config has no outbounds")
	}

	tags := map[string]bool{}
	for _, o := range outbounds {
		if m, ok := o.(map[string]interface{}); ok {
			if tag, ok := m["tag"].(string); ok {
				tags[tag] = true
			}
		}
	}
	for _, want := range []string{"proxy", "direct", "block"} {
		if !tags[want] {
			t.Errorf("outbound %q is missing (referenced by the routing rules)", want)
		}
	}

	if got := routeSection(t, cfg)["final"]; got != "proxy" {
		t.Errorf("route final = %v, want the proxy tag", got)
	}
}

// The whole config must survive a JSON round trip: CoreManager hands it to
// sing-box as marshalled bytes, so an unencodable value fails at runtime only.
func TestConfigIsJSONSerialisable(t *testing.T) {
	cfg := generate(t, Settings{
		TunMode:              true,
		FakeDns:              true,
		BypassRu:             true,
		CustomDirect:         []string{"example.com"},
		ProcessMode:          "whitelist",
		ProcessListWhitelist: []string{"browser.exe"},
		CustomRules:          []CustomRule{{Action: "block", Type: "domain", Value: "ads.example"}},
	})

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("config is not JSON-serialisable: %v", err)
	}
	var round map[string]interface{}
	if err := json.Unmarshal(data, &round); err != nil {
		t.Fatalf("config does not round trip through JSON: %v", err)
	}
	for _, key := range []string{"log", "dns", "inbounds", "outbounds", "route", "experimental"} {
		if _, ok := round[key]; !ok {
			t.Errorf("section %q is missing after a JSON round trip", key)
		}
	}
}

func logLevel(t *testing.T, cfg map[string]interface{}) string {
	t.Helper()
	logSection, ok := cfg["log"].(map[string]interface{})
	if !ok {
		t.Fatal("config has no log section")
	}
	level, ok := logSection["level"].(string)
	if !ok {
		t.Fatal("log section has no level")
	}
	return level
}

// The default is deliberately quiet: at "info" sing-box logs every connection
// open and close, and each of those lines has to cross into the WebView2
// renderer, which is what made memory grow without bound.
func TestLogLevelDefaultsToWarn(t *testing.T) {
	if got := logLevel(t, generate(t, Settings{})); got != "warn" {
		t.Errorf("default log level = %q, want %q", got, "warn")
	}
}

func TestVerboseLoggingRaisesLogLevel(t *testing.T) {
	if got := logLevel(t, generate(t, Settings{VerboseLogging: true})); got != "info" {
		t.Errorf("verbose log level = %q, want %q", got, "info")
	}
}

// The core's log stream must never reach os.Stderr. InitCrashLog redirects
// stderr into crash.log so that a panic in a core goroutine leaves a trace, and
// with sing-box's default output that file collected every connection instead --
// 728 MB in one session. The UI gets the lines through the platform log writer.
func TestCoreLogIsNotWrittenToStderr(t *testing.T) {
	for _, verbose := range []bool{false, true} {
		cfg := generate(t, Settings{VerboseLogging: verbose})
		logSection, ok := cfg["log"].(map[string]interface{})
		if !ok {
			t.Fatal("config has no log section")
		}
		output, ok := logSection["output"].(string)
		if !ok {
			t.Fatalf("verbose=%v: log section has no output; sing-box defaults it to stderr", verbose)
		}
		if output != os.DevNull {
			t.Errorf("verbose=%v: log output = %q, want %q", verbose, output, os.DevNull)
		}
	}
}

func tunInbound(t *testing.T, cfg map[string]interface{}) map[string]interface{} {
	t.Helper()
	inbounds, ok := cfg["inbounds"].([]map[string]interface{})
	if !ok {
		t.Fatal("config has no inbounds")
	}
	for _, in := range inbounds {
		if in["type"] == "tun" {
			return in
		}
	}
	t.Fatal("no tun inbound in config")
	return nil
}

// Both values were inherited from the project's first commit without a stated
// reason and both cost real CPU. Pin them so a future change is a decision
// rather than a drift.
func TestTunStackAndMTU(t *testing.T) {
	tun := tunInbound(t, generate(t, Settings{TunMode: true}))

	// "mixed" keeps gVisor for UDP but takes TCP out of the userspace stack.
	if got := tun["stack"]; got != "mixed" {
		t.Errorf("tun stack = %v, want mixed", got)
	}
	// 1280 is the IPv6 minimum: safe, and about seven times more packets per
	// byte than sing-box's own default.
	if got := tun["mtu"]; got != 9000 {
		t.Errorf("tun mtu = %v, want 9000", got)
	}
}

// The system stack underneath "mixed" claims the address after the first one in
// each prefix for its NAT, and refuses to start without it.
func TestTunPrefixesLeaveRoomForSystemStackNAT(t *testing.T) {
	tun := tunInbound(t, generate(t, Settings{TunMode: true}))
	addrs, ok := tun["address"].([]string)
	if !ok || len(addrs) == 0 {
		t.Fatal("tun inbound has no address list")
	}
	for _, a := range addrs {
		prefix, err := netip.ParsePrefix(a)
		if err != nil {
			t.Fatalf("address %q does not parse: %v", a, err)
		}
		if !prefix.Contains(prefix.Addr().Next()) {
			t.Errorf("prefix %s has no address after %s; the system stack cannot NAT",
				prefix, prefix.Addr())
		}
	}
}

// NAT-PMP requests aimed at the tunnel's own gateway address can never be
// answered, so they repeat -- each retry from a fresh source port, and so each
// one a whole new connection for sing-box to set up. A capture measured 5374
// distinct source ports across 5452 packets in eight seconds, costing 1.4 CPU
// cores while carrying nothing. Refuse them before that work happens.
func TestNatPmpIsRejectedBeforeItBecomesAConnection(t *testing.T) {
	rules := routeRules(t, generate(t, Settings{TunMode: true}))

	natPmp := -1
	for i, r := range rules {
		ports, _ := r["port"].([]int)
		if r["action"] == "reject" && len(ports) == 1 && ports[0] == 5351 {
			natPmp = i
			break
		}
	}
	if natPmp < 0 {
		t.Fatal("no rule rejects NAT-PMP; every request becomes a connection")
	}

	if got := rules[natPmp]["network"]; got != "udp" {
		t.Errorf("NAT-PMP rule network = %v, want udp", got)
	}
	// An ICMP error tells the sender to give up; a silent drop reads as packet
	// loss and brings the retries straight back.
	if got := rules[natPmp]["method"]; got != "default" {
		t.Errorf("NAT-PMP reject method = %v, want default (ICMP error)", got)
	}

	// The tunnel's gateway address doubles as the DNS server, so DNS has to be
	// hijacked before anything addressed there is refused.
	lastHijack := -1
	for i, r := range rules {
		if r["action"] == "hijack-dns" {
			lastHijack = i
		}
	}
	if lastHijack < 0 {
		t.Fatal("no DNS hijack rule")
	}
	if natPmp < lastHijack {
		t.Errorf("NAT-PMP reject at %d precedes the DNS hijack at %d", natPmp, lastHijack)
	}
}
