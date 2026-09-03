package core

import (
	"net/http"
	"testing"
)

// suggest is a readability wrapper: nearly every case below cares about all
// three return values at once.
func suggest(t *testing.T, host, ip string) (string, string, bool) {
	t.Helper()
	return SuggestRuleTarget(host, ip)
}

// ─── The host path ──────────────────────────────────────────────────────────

func TestSuggestRuleTargetPrefersTheSniffedHost(t *testing.T) {
	ruleType, value, ok := suggest(t, "api.example.com", "203.0.113.10")
	if !ok {
		t.Fatal("a sniffed hostname produced no rule")
	}
	if ruleType != "domain_suffix" {
		t.Errorf("rule type = %q, want domain_suffix", ruleType)
	}
	if value != "api.example.com" {
		t.Errorf("rule value = %q, want the host rather than the address", value)
	}
}

func TestSuggestRuleTargetNormalisesTheHost(t *testing.T) {
	for name, host := range map[string]string{
		"trailing root dot": "api.example.com.",
		"upper case":        "API.Example.COM",
		"surrounding space": "  api.example.com  ",
		"leading dot":       ".api.example.com",
	} {
		t.Run(name, func(t *testing.T) {
			ruleType, value, ok := suggest(t, host, "")
			if !ok {
				t.Fatalf("%q produced no rule", host)
			}
			if ruleType != "domain_suffix" || value != "api.example.com" {
				t.Errorf("got (%q, %q), want (domain_suffix, api.example.com)", ruleType, value)
			}
		})
	}
}

// ─── The address fallback ───────────────────────────────────────────────────

func TestSuggestRuleTargetFallsBackToTheDestinationIP(t *testing.T) {
	for name, tc := range map[string]struct{ ip, want string }{
		"ipv4":                {"203.0.113.10", "203.0.113.10/32"},
		"ipv6":                {"2001:db8::1", "2001:db8::1/128"},
		"ipv4-mapped ipv6":    {"::ffff:203.0.113.10", "203.0.113.10/32"},
		"surrounding space":   {"  203.0.113.10 ", "203.0.113.10/32"},
		"ipv6 upper case hex": {"2001:DB8::1", "2001:db8::1/128"},
	} {
		t.Run(name, func(t *testing.T) {
			ruleType, value, ok := suggest(t, "", tc.ip)
			if !ok {
				t.Fatalf("%q produced no rule", tc.ip)
			}
			if ruleType != "ip_cidr" {
				t.Errorf("rule type = %q, want ip_cidr", ruleType)
			}
			if value != tc.want {
				t.Errorf("rule value = %q, want %q", value, tc.want)
			}
		})
	}
}

// Every address rejected here is already covered by a rule that GenerateConfig
// emits *after* the custom rules — ip_is_private → direct, in particular. A
// custom rule for one of them would win, and the user would lose their router,
// their printer or their local resolver with nothing on screen explaining why.
func TestSuggestRuleTargetRefusesPrivateAndLoopbackAddresses(t *testing.T) {
	for name, ip := range map[string]string{
		"rfc1918 /24":            "192.168.1.1",
		"rfc1918 /12":            "172.16.0.5",
		"rfc1918 /8":             "10.0.0.1",
		"loopback":               "127.0.0.1",
		"ipv6 loopback":          "::1",
		"unspecified":            "0.0.0.0",
		"link-local":             "169.254.10.1",
		"ipv6 link-local":        "fe80::1",
		"unique local ipv6":      "fd00::1",
		"multicast":              "224.0.0.1",
		"ipv6 multicast":         "ff02::1",
		"ipv4-mapped rfc1918":    "::ffff:192.168.1.1",
		"ipv4-mapped loopback":   "::ffff:127.0.0.1",
		"private via ipv6 range": "fc00::abcd",
	} {
		t.Run(name, func(t *testing.T) {
			if _, value, ok := suggest(t, "", ip); ok {
				t.Errorf("%s produced the rule %q; it would preempt ip_is_private → direct", name, value)
			}
		})
	}
}

// The FakeIP route is deliberately the last of the exemptions in
// GenerateConfig — see TestFakeIPRuleComesAfterExclusions. Addresses inside the
// range are synthetic, so a rule naming one is meaningless on its own and
// harmful in its position.
func TestSuggestRuleTargetRefusesTheFakeIPRange(t *testing.T) {
	for _, ip := range []string{"198.18.0.1", "198.18.255.255", "198.19.0.1"} {
		if _, value, ok := suggest(t, "", ip); ok {
			t.Errorf("fakeip address %s produced the rule %q", ip, value)
		}
	}
}

func TestSuggestRuleTargetRefusesValuesThatAreNotRoutable(t *testing.T) {
	for name, tc := range map[string]struct{ host, ip string }{
		"both empty":        {"", ""},
		"whitespace only":   {"   ", "  "},
		"wildcard":          {"*", ""},
		"single label":      {"localhost", ""},
		"spaces inside":     {"a b", ""},
		"a path":            {"example.com/admin", ""},
		"host and port":     {"example.com:443", ""},
		"bracketed address": {"[2001:db8::1]", ""},
		"dots only":         {"...", ""},
		"garbage address":   {"", "not-an-address"},
		"cidr as address":   {"", "203.0.113.0/24"},
	} {
		t.Run(name, func(t *testing.T) {
			if ruleType, value, ok := suggest(t, tc.host, tc.ip); ok {
				t.Errorf("got (%q, %q, true), want a refusal", ruleType, value)
			}
		})
	}
}

// An IP literal that arrives in the host field is an address, not a domain, and
// has to come out as ip_cidr — a domain_suffix rule naming an address never
// matches anything and the click would appear to do nothing.
func TestSuggestRuleTargetTreatsAnIPInTheHostFieldAsAnAddress(t *testing.T) {
	ruleType, value, ok := suggest(t, "203.0.113.10", "203.0.113.10")
	if !ok {
		t.Fatal("an address in the host field produced no rule")
	}
	if ruleType != "ip_cidr" || value != "203.0.113.10/32" {
		t.Errorf("got (%q, %q), want (ip_cidr, 203.0.113.10/32)", ruleType, value)
	}
}

// ─── The payoff ─────────────────────────────────────────────────────────────

// A suggestion is only correct if it survives the trip through GenerateConfig
// and lands where custom rules are supposed to land: ahead of the private
// address exemption, the bypass-RU sets, the FakeIP route and the whitelist
// catch-all. Two of those orderings have already been broken once each (see the
// comments in config.go), so the guarantee is pinned here rather than assumed.
func TestSuggestedRuleSurvivesGenerateConfig(t *testing.T) {
	for name, tc := range map[string]struct{ host, ip string }{
		"from a host":       {"tracker.example.com", "203.0.113.10"},
		"from an address":   {"", "203.0.113.10"},
		"from an ipv6 host": {"", "2001:db8::1"},
	} {
		t.Run(name, func(t *testing.T) {
			ruleType, value, ok := SuggestRuleTarget(tc.host, tc.ip)
			if !ok {
				t.Fatalf("SuggestRuleTarget(%q, %q) refused", tc.host, tc.ip)
			}

			rules := routeRules(t, generate(t, Settings{
				TunMode:              true,
				FakeDns:              true,
				BypassRu:             true,
				ProcessMode:          "whitelist",
				ProcessListWhitelist: []string{"browser.exe"},
				CustomRules:          []CustomRule{{Action: "block", Type: ruleType, Value: value}},
			}))

			custom := indexOfRule(rules, func(r map[string]interface{}) bool {
				return hasKeyValue(r, "outbound", "block") && hasKeyValue(r, ruleType, []string{value})
			})
			if custom < 0 {
				t.Fatalf("the suggested rule (%s = %s) never reached route.rules", ruleType, value)
			}

			for label, idx := range map[string]int{
				"private addresses": indexOfRule(rules, func(r map[string]interface{}) bool {
					return hasKeyValue(r, "ip_is_private", true)
				}),
				"bypass RU": indexOfRule(rules, func(r map[string]interface{}) bool {
					return hasKeyValue(r, "rule_set", []string{"geoip-ru", "geosite-category-ru"})
				}),
				"fakeip":              indexOfRule(rules, isFakeIPRoute),
				"whitelist catch-all": indexOfCatchAll(rules),
			} {
				if idx < 0 {
					t.Errorf("%s rule is missing from the config", label)
					continue
				}
				if custom > idx {
					t.Errorf("the suggested rule sits at %d, after the %s rule at %d, so it can never match",
						custom, label, idx)
				}
			}
		})
	}
}

// Проверка «только HTTPS» обязана держаться на всей цепочке перенаправлений, а
// не только на адресе, который ввёл человек: тело подписки — это список
// серверов, и тот, кто может его подменить, выбирает, через что пойдёт трафик.
func TestRefuseInsecureRedirect(t *testing.T) {
	https, _ := http.NewRequest("GET", "https://example.com/sub", nil)
	plain, _ := http.NewRequest("GET", "http://example.com/sub", nil)

	if err := refuseInsecureRedirect(https, nil); err != nil {
		t.Errorf("перенаправление на HTTPS отклонено: %v", err)
	}
	if err := refuseInsecureRedirect(plain, nil); err == nil {
		t.Error("перенаправление на HTTP пропущено — проверка схемы обходится одним заголовком Location")
	}

	// Цикл перенаправлений всё ещё обрывается: подменив CheckRedirect, мы
	// заменили и стандартный предел в десять переходов.
	via := make([]*http.Request, 10)
	if err := refuseInsecureRedirect(https, via); err == nil {
		t.Error("цепочка из десяти перенаправлений не оборвана")
	}
}
