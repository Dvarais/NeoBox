package core

import (
	"net"
	"strconv"
	"testing"
)

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

		{"anytls://pw@example.com:443", "anytls"},
		{"socks://example.com:1080", "socks"},
		{"socks5://user:pw@example.com:1080", "socks"},

		// An HTTP proxy is told from a web address by its shape: explicit port,
		// no path. Both spell "http://", and only the former is a node.
		{"http://1.2.3.4:8080#Home", "http"},
		{"http://user:pw@1.2.3.4:8080", "http"},
		{"http://[2001:db8::1]:8080", "http"},
		{"http://example.com:8080/", "http"},

		// Not proxy links.
		{"https://example.com/sub", ""},
		{"http://example.com/sub", ""},
		{"http://example.com:8080/sub", ""},
		{"http://example.com", ""},
		{"http://[2001:db8::1]", ""},
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

// Every protocol NeoBox can parse must yield an endpoint. The Kill Switch
// refuses to arm without one and aborts the connection, so a protocol missing
// here is a protocol that cannot be used with the Kill Switch on at all — which
// is exactly what happened to WireGuard while callers read outbound["server"]
// directly instead of asking ServerEndpoint.
func TestServerEndpointCoversEveryProtocol(t *testing.T) {
	links := []string{
		"vless://uuid@example.com:443",
		"vmess://eyJhZGQiOiJleGFtcGxlLmNvbSIsInBvcnQiOjQ0MywiaWQiOiJ1dWlkIn0=",
		"trojan://pw@example.com:443",
		"ss://YWVzLTI1Ni1nY206cGFzcw==@example.com:8388",
		"tuic://uuid:pw@example.com:443",
		"hysteria2://pw@example.com:443",
		"hysteria://example.com:443?auth=x",
		"anytls://pw@example.com:443",
		"socks5://user:pw@example.com:1080",
		"http://example.com:8080",
		"wireguard://cHJpdmF0ZWtleQ==@example.com:51820?publickey=cGVlcmtleQ==&address=172.16.0.2",
	}

	for _, link := range links {
		outbound, err := ParseProxyLink(link)
		if err != nil {
			t.Errorf("ParseProxyLink(%q) failed: %v", link, err)
			continue
		}
		host, port := ServerEndpoint(outbound)
		if host == "" {
			t.Errorf("ServerEndpoint(%q) returned no host", link)
		}
		if port <= 0 || port > 65535 {
			t.Errorf("ServerEndpoint(%q) returned port %d, which is not usable", link, port)
		}
	}
}

// WireGuard is the reason ServerEndpoint exists: it is an endpoint rather than
// an outbound, so its address lives in the first peer and not in "server".
func TestServerEndpointReadsWireGuardPeer(t *testing.T) {
	outbound, err := ParseProxyLink(
		"wireguard://cHJpdmF0ZWtleQ==@vpn.example.com:51821?publickey=cGVlcmtleQ==&address=172.16.0.2")
	if err != nil {
		t.Fatalf("ParseProxyLink failed: %v", err)
	}
	if _, ok := outbound["server"]; ok {
		t.Fatal("a WireGuard outbound now has a top-level \"server\" field; this test is no longer meaningful")
	}

	host, port := ServerEndpoint(outbound)
	if got := net.JoinHostPort(host, strconv.Itoa(port)); got != "vpn.example.com:51821" {
		t.Errorf("ServerEndpoint = %q, want vpn.example.com:51821", got)
	}
}

// The port is an int as ParseProxyLink writes it, but the same maps are also
// built by decoding JSON (float64) and from links carrying the port as text.
func TestServerEndpointAcceptsEveryPortShape(t *testing.T) {
	for name, value := range map[string]interface{}{
		"int":     443,
		"float64": float64(443),
		"string":  "443",
	} {
		_, port := ServerEndpoint(map[string]interface{}{"server": "example.com", "server_port": value})
		if port != 443 {
			t.Errorf("port given as %s read as %d, want 443", name, port)
		}
	}

	for name, value := range map[string]interface{}{
		"absent":             nil,
		"non-numeric string": "https",
		"bool":               true,
	} {
		_, port := ServerEndpoint(map[string]interface{}{"server": "example.com", "server_port": value})
		if port != 0 {
			t.Errorf("port given as %s read as %d, want 0", name, port)
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
		"anytls://pw@example.com:443",
		"socks://example.com:1080",
		"socks5://user:pw@example.com:1080",
		"http://1.2.3.4:8080",
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
