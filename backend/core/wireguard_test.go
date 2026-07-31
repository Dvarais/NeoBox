package core

import (
	"reflect"
	"strings"
	"testing"
)

// A WireGuard link carries a base64 private key, which can contain "/" — the
// one character that would otherwise end the authority and truncate the key.
const testWireGuardLink = "wireguard://qK%2FLp8Nz1a%2BbQfR3sVxYu0wE5tGhJkLmNoPqRsTuVw%3D@" +
	"engage.cloudflareclient.com:2408" +
	"?address=172.16.0.2%2C2606%3A4700%3A110%3A8a%3A1%3A%3Aa" +
	"&publickey=bmVvYm94LXRlc3QtcGVlci1wdWJsaWMta2V5LWFiY2Q%3D" +
	"&mtu=1408&reserved=12%2C34%2C56&keepalive=25#WARP"

func wireguardPeer(t *testing.T, outbound map[string]interface{}) map[string]interface{} {
	t.Helper()
	peers, ok := outbound["peers"].([]interface{})
	if !ok || len(peers) == 0 {
		t.Fatalf("outbound has no peers: %#v", outbound)
	}
	peer, ok := peers[0].(map[string]interface{})
	if !ok {
		t.Fatalf("peer is not a map: %#v", peers[0])
	}
	return peer
}

func TestParseWireGuardLink(t *testing.T) {
	outbound, err := ParseProxyLink(testWireGuardLink)
	if err != nil {
		t.Fatalf("ParseProxyLink failed: %v", err)
	}

	if outbound["type"] != "wireguard" {
		t.Errorf("type = %v, want wireguard", outbound["type"])
	}
	if outbound["tag"] != "WARP" {
		t.Errorf("tag = %v, want WARP", outbound["tag"])
	}
	// The "/" inside the key must survive: url.Parse hands the userinfo back
	// decoded, and a truncated key fails the handshake with no useful error.
	if got := outbound["private_key"]; got != "qK/Lp8Nz1a+bQfR3sVxYu0wE5tGhJkLmNoPqRsTuVw=" {
		t.Errorf("private_key = %v, want the full key", got)
	}
	// A bare address gains the prefix length sing-box demands, and IPv6 gets
	// /128 rather than /32.
	want := []string{"172.16.0.2/32", "2606:4700:110:8a:1::a/128"}
	if got, _ := outbound["address"].([]string); !reflect.DeepEqual(got, want) {
		t.Errorf("address = %#v, want %#v", got, want)
	}
	if outbound["mtu"] != 1408 {
		t.Errorf("mtu = %v, want 1408", outbound["mtu"])
	}

	peer := wireguardPeer(t, outbound)
	if peer["address"] != "engage.cloudflareclient.com" || peer["port"] != 2408 {
		t.Errorf("peer endpoint = %v:%v, want engage.cloudflareclient.com:2408", peer["address"], peer["port"])
	}
	if peer["persistent_keepalive_interval"] != 25 {
		t.Errorf("keepalive = %v, want 25", peer["persistent_keepalive_interval"])
	}
	if got, _ := peer["reserved"].([]int); !reflect.DeepEqual(got, []int{12, 34, 56}) {
		t.Errorf("reserved = %#v, want [12 34 56]", got)
	}
	if allowed, _ := peer["allowed_ips"].([]string); len(allowed) != 2 {
		t.Errorf("allowed_ips = %#v, want the full-tunnel pair", allowed)
	}
}

// Without an interface address the tunnel cannot come up, and sing-box would
// reject the config with a message about a missing field.
func TestWireGuardLinkWithoutAddress(t *testing.T) {
	_, err := ParseProxyLink("wireguard://key@example.com:51820?publickey=pub")
	if err == nil {
		t.Fatal("expected an error for a link with no address")
	}
}

func TestWireGuardFromClash(t *testing.T) {
	payload := []byte(`
proxies:
  - name: WARP
    type: wireguard
    server: engage.cloudflareclient.com
    port: 2408
    ip: 172.16.0.2
    ipv6: 2606:4700:110:8a:1::a
    private-key: cHJpdmF0ZS1rZXktZm9yLXRoZS1uZW9ib3gtdGVzdHM=
    public-key: cHVibGljLWtleS1mb3ItdGhlLW5lb2JveC10ZXN0cw==
    reserved: [12, 34, 56]
    mtu: 1408
    persistent-keepalive: 25
    udp: true
`)

	links := parseClashYAML(payload)
	if len(links) != 1 {
		t.Fatalf("got %d links, want 1: %v", len(links), links)
	}
	if !strings.HasPrefix(links[0], "wireguard://") {
		t.Fatalf("unexpected link: %s", links[0])
	}

	outbound, err := ParseProxyLink(links[0])
	if err != nil {
		t.Fatalf("ParseProxyLink(%q): %v", links[0], err)
	}
	if outbound["tag"] != "WARP" {
		t.Errorf("tag = %v, want WARP", outbound["tag"])
	}
	if outbound["private_key"] != "cHJpdmF0ZS1rZXktZm9yLXRoZS1uZW9ib3gtdGVzdHM=" {
		t.Errorf("private_key = %v", outbound["private_key"])
	}
	want := []string{"172.16.0.2/32", "2606:4700:110:8a:1::a/128"}
	if got, _ := outbound["address"].([]string); !reflect.DeepEqual(got, want) {
		t.Errorf("address = %#v, want %#v", got, want)
	}

	peer := wireguardPeer(t, outbound)
	if peer["public_key"] != "cHVibGljLWtleS1mb3ItdGhlLW5lb2JveC10ZXN0cw==" {
		t.Errorf("peer public_key = %v", peer["public_key"])
	}
	if got, _ := peer["reserved"].([]int); !reflect.DeepEqual(got, []int{12, 34, 56}) {
		t.Errorf("reserved = %#v, want [12 34 56]", got)
	}
}

// sing-box keeps WireGuard in "endpoints", so a config carrying one used to
// import as an empty subscription.
func TestWireGuardFromSingboxEndpoints(t *testing.T) {
	payload := []byte(`{
	  "outbounds": [{"type": "direct", "tag": "direct"}],
	  "endpoints": [{
	    "type": "wireguard",
	    "tag": "wg",
	    "address": ["172.16.0.2/32"],
	    "private_key": "cHJpdmF0ZS1rZXktZm9yLXRoZS1uZW9ib3gtdGVzdHM=",
	    "mtu": 1408,
	    "peers": [{
	      "address": "engage.cloudflareclient.com",
	      "port": 2408,
	      "public_key": "cHVibGljLWtleS1mb3ItdGhlLW5lb2JveC10ZXN0cw==",
	      "persistent_keepalive_interval": 25
	    }]
	  }]
	}`)

	links := parseSingboxOutbounds(payload)
	if len(links) != 1 {
		t.Fatalf("got %d links, want 1: %v", len(links), links)
	}
	outbound, err := ParseProxyLink(links[0])
	if err != nil {
		t.Fatalf("ParseProxyLink(%q): %v", links[0], err)
	}
	if outbound["tag"] != "wg" || outbound["mtu"] != 1408 {
		t.Errorf("tag/mtu = %v/%v, want wg/1408", outbound["tag"], outbound["mtu"])
	}
}

func wireguardOutbound() map[string]interface{} {
	return map[string]interface{}{
		"type":        "wireguard",
		"tag":         "proxy",
		"address":     []string{"172.16.0.2/32"},
		"private_key": "cHJpdmF0ZS1rZXktZm9yLXRoZS1uZW9ib3gtdGVzdHM=",
		"peers": []interface{}{
			map[string]interface{}{
				"address":    testServerIP,
				"port":       2408,
				"public_key": "cHVibGljLWtleS1mb3ItdGhlLW5lb2JveC10ZXN0cw==",
			},
		},
	}
}

// A "wireguard" outbound is a stub in sing-box 1.13 that refuses to start; the
// real thing has to go in the endpoints section.
func TestWireGuardGoesIntoEndpoints(t *testing.T) {
	cfg, err := GenerateConfig(wireguardOutbound(), Settings{}, false, "cache.db", "secret")
	if err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
	}

	endpoints, ok := cfg["endpoints"].([]interface{})
	if !ok || len(endpoints) != 1 {
		t.Fatalf("config has no endpoints section: %#v", cfg["endpoints"])
	}
	endpoint, _ := endpoints[0].(map[string]interface{})
	if endpoint["type"] != "wireguard" || endpoint["tag"] != "proxy" {
		t.Errorf("endpoint = %#v, want the wireguard node", endpoint)
	}

	// Routing still names the same tag, and the proxy must not also appear as
	// an outbound.
	if final := routeSection(t, cfg)["final"]; final != "proxy" {
		t.Errorf("route.final = %v, want proxy", final)
	}
	outbounds, _ := cfg["outbounds"].([]interface{})
	if len(outbounds) != 2 {
		t.Fatalf("outbounds = %#v, want only direct and block", outbounds)
	}
	for _, item := range outbounds {
		ob, _ := item.(map[string]interface{})
		if ob["type"] == "wireguard" {
			t.Error("wireguard node was left in the outbounds section")
		}
	}
}

// GenerateConfig promises not to touch the caller's outbound. For WireGuard the
// server address sits in a nested peer map, which a shallow copy still shares.
func TestGenerateConfigDoesNotMutateWireGuardPeers(t *testing.T) {
	outbound := wireguardOutbound()
	peers, _ := outbound["peers"].([]interface{})
	original, _ := peers[0].(map[string]interface{})
	before := original["address"]

	if _, err := GenerateConfig(outbound, Settings{}, false, "cache.db", "secret"); err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
	}

	if original["address"] != before {
		t.Errorf("caller's peer address changed to %v, want %v", original["address"], before)
	}
}

// XHTTP is an Xray transport with no implementation in sing-box, so a node
// using it has to fail with an explanation instead of being quietly rebuilt as
// HTTP/2 and failing later for reasons that point at the server.
func TestXHTTPIsRejected(t *testing.T) {
	for _, link := range []string{
		"vless://11111111-2222-3333-4444-555555555555@a.example.com:443?type=xhttp&security=tls",
		"vless://11111111-2222-3333-4444-555555555555@a.example.com:443?type=splithttp&security=tls",
		"trojan://pw@a.example.com:443?type=xhttp&security=tls",
	} {
		_, err := ParseProxyLink(link)
		if err == nil {
			t.Errorf("ParseProxyLink(%q) succeeded, want an unsupported-transport error", link)
			continue
		}
		if !strings.Contains(err.Error(), "xhttp") && !strings.Contains(err.Error(), "splithttp") {
			t.Errorf("error does not name the transport: %v", err)
		}
	}

	// The h2 transport is real and must keep working.
	if _, err := ParseProxyLink("vless://11111111-2222-3333-4444-555555555555@a.example.com:443?type=h2&security=tls"); err != nil {
		t.Errorf("h2 transport was rejected: %v", err)
	}
}
