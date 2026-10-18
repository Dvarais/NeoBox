package core

import (
	"testing"
)

// TestRuleOrderWhitelistPrivateAndBypassRu verifies that in whitelist split-tunneling mode,
// private network traffic (LAN, router, loopback) and bypass-RU traffic take strict precedence
// over whitelisted application routing to the VPN tunnel.
//
// If a whitelisted process rule (e.g. process_name: [browser.exe] -> outboundTag) were placed
// before ip_is_private: true -> direct, any connection from the browser to 192.168.1.1 (home router)
// or 10.0.0.x (local NAS) would be forced into the remote VPN tunnel and dropped.
func TestRuleOrderWhitelistPrivateAndBypassRu(t *testing.T) {
	rules := routeRules(t, generate(t, Settings{
		TunMode:              true,
		FakeDns:              true,
		BypassRu:             true,
		ProcessMode:          "whitelist",
		ProcessListWhitelist: []string{"browser.exe"},
	}))

	privateIdx := indexOfRule(rules, func(r map[string]interface{}) bool {
		return hasKeyValue(r, "ip_is_private", true)
	})
	if privateIdx < 0 {
		t.Fatal("missing ip_is_private rule in generated route.rules")
	}

	bypassRuIdx := indexOfRule(rules, func(r map[string]interface{}) bool {
		return hasKeyValue(r, "rule_set", []string{"geoip-ru", "geosite-category-ru"})
	})
	if bypassRuIdx < 0 {
		t.Fatal("missing bypass-RU rule in generated route.rules")
	}

	whitelistProcessIdx := indexOfRule(rules, func(r map[string]interface{}) bool {
		return hasKeyValue(r, "process_name", []string{"browser.exe"})
	})
	if whitelistProcessIdx < 0 {
		t.Fatal("missing whitelisted process rule in generated route.rules")
	}

	catchAllIdx := indexOfCatchAll(rules)
	if catchAllIdx < 0 {
		t.Fatal("missing direct catch-all rule in whitelist mode")
	}

	// VERIFY STRICT PRECEDENCE:
	// 1. ip_is_private MUST be evaluated before whitelist process rule so browser can reach LAN
	if privateIdx > whitelistProcessIdx {
		t.Fatalf("FAILED PRECEDENCE: ip_is_private rule (index %d) is placed AFTER whitelisted process rule (index %d). "+
			"This routes LAN/router traffic from browser.exe into the VPN tunnel!", privateIdx, whitelistProcessIdx)
	}

	// 2. bypass-RU MUST be evaluated before whitelist process rule so browser accesses Russian sites directly
	if bypassRuIdx > whitelistProcessIdx {
		t.Fatalf("FAILED PRECEDENCE: bypass-RU rule (index %d) is placed AFTER whitelisted process rule (index %d). "+
			"This routes Russian sites from browser.exe into the VPN tunnel despite BypassRu being enabled!", bypassRuIdx, whitelistProcessIdx)
	}

	// 3. whitelisted process rule must precede the direct catch-all
	if whitelistProcessIdx > catchAllIdx {
		t.Fatalf("FAILED PRECEDENCE: whitelisted process rule (index %d) is placed AFTER catch-all (index %d)",
			whitelistProcessIdx, catchAllIdx)
	}
}

