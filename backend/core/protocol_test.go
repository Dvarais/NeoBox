package core

import "testing"

func TestProtocolOf(t *testing.T) {
	tests := []struct {
		link string
		want string
	}{
		{"vless://uuid@example.com:443#n", "vless"},
		{"vmess://base64payload", "vmess"},
		{"ss://method:pass@example.com:8388", "ss"},
		{"trojan://pw@example.com:443", "trojan"},
		{"tuic://uuid:pw@example.com:443", "tuic"},
		{"hysteria://example.com:443?auth=x", "hysteria"},

		// Aliases collapse onto the canonical name, so callers never have to
		// know that hy2 and hysteria2 are the same protocol.
		{"hysteria2://pw@example.com:443", "hysteria2"},
		{"hy2://pw@example.com:443", "hysteria2"},

		// Matching is case-insensitive. The clipboard import used to compare
		// case-sensitively while every other path lower-cased first, so a link
		// pasted in this shape was accepted by one path and dropped by another.
		{"VLESS://uuid@example.com:443", "vless"},
		{"Hy2://pw@example.com:443", "hysteria2"},
		{"  vless://uuid@example.com:443  ", "vless"},

		// Not proxy links.
		{"https://example.com/sub", ""},
		{"http://example.com/sub", ""},
		{"example.com", ""},
		{"", ""},
		{"://nohost", ""},
		{"vlessx://uuid@example.com", ""},
	}

	for _, tc := range tests {
		if got := ProtocolOf(tc.link); got != tc.want {
			t.Errorf("ProtocolOf(%q) = %q, want %q", tc.link, got, tc.want)
		}
	}
}

func TestIsProxyLink(t *testing.T) {
	for _, link := range []string{
		"vless://uuid@example.com:443",
		"HY2://pw@example.com:443",
		"ss://method:pass@example.com:8388",
	} {
		if !IsProxyLink(link) {
			t.Errorf("IsProxyLink(%q) = false, want true", link)
		}
	}

	for _, link := range []string{
		"https://example.com/subscription",
		"not a link",
		"",
	} {
		if IsProxyLink(link) {
			t.Errorf("IsProxyLink(%q) = true, want false", link)
		}
	}
}

// Every scheme ParseProxyLink handles must also be recognised by ProtocolOf,
// otherwise a link would be accepted for import and then fail to parse.
func TestProtocolOfCoversParsedSchemes(t *testing.T) {
	links := []string{
		"vless://uuid@example.com:443",
		"trojan://pw@example.com:443",
		"ss://YWVzLTI1Ni1nY206cGFzcw==@example.com:8388",
		"tuic://uuid:pw@example.com:443",
		"hysteria2://pw@example.com:443",
		"hy2://pw@example.com:443",
		"hysteria://example.com:443?auth=x",
	}

	for _, link := range links {
		if !IsProxyLink(link) {
			t.Errorf("%q parses as a proxy link but IsProxyLink rejects it", link)
		}
		if _, err := ParseProxyLink(link); err != nil {
			t.Errorf("ParseProxyLink(%q) failed: %v", link, err)
		}
	}
}
