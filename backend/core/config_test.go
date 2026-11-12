package core

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestParseProxyLink_HTTPTransport_VLESS(t *testing.T) {
	// A VLESS link with http transport and method parameter
	link := "vless://96c4d7b2-d3cf-4279-b1d5-2244249a5629@example.com:443?type=http&host=example.com&path=%2Ftesting&method=POST&security=tls#vless-http-test"

	outbound, err := ParseProxyLink(link)
	if err != nil {
		t.Fatalf("failed to parse VLESS proxy link: %v", err)
	}

	if outbound["type"] != "vless" {
		t.Errorf("expected protocol type 'vless', got '%v'", outbound["type"])
	}

	transport, ok := outbound["transport"].(map[string]interface{})
	if !ok {
		t.Fatalf("outbound transport map is missing or invalid")
	}

	if transport["type"] != "http" {
		t.Errorf("expected transport type 'http', got '%v'", transport["type"])
	}

	if transport["path"] != "/testing" {
		t.Errorf("expected transport path '/testing', got '%v'", transport["path"])
	}

	if transport["method"] != "POST" {
		t.Errorf("expected transport method 'POST', got '%v'", transport["method"])
	}

	// Test default method value
	linkDefault := "vless://96c4d7b2-d3cf-4279-b1d5-2244249a5629@example.com:443?type=http&host=example.com&path=%2Ftesting#vless-http-test"
	outboundDefault, err := ParseProxyLink(linkDefault)
	if err != nil {
		t.Fatalf("failed to parse VLESS proxy link with default method: %v", err)
	}
	transportDefault := outboundDefault["transport"].(map[string]interface{})
	if transportDefault["method"] != "GET" {
		t.Errorf("expected default transport method 'GET', got '%v'", transportDefault["method"])
	}
}

func TestParseProxyLink_HTTPTransport_VMess(t *testing.T) {
	vmessConfig := map[string]interface{}{
		"v":      "2",
		"ps":     "vmess-http-test",
		"add":    "example.com",
		"port":   443,
		"id":     "96c4d7b2-d3cf-4279-b1d5-2244249a5629",
		"aid":    0,
		"scy":    "auto",
		"net":    "http",
		"host":   "example.com",
		"path":   "/testing",
		"tls":    "tls",
		"method": "POST",
	}

	jsonBytes, err := json.Marshal(vmessConfig)
	if err != nil {
		t.Fatalf("failed to marshal vmess config: %v", err)
	}

	b64Config := base64.StdEncoding.EncodeToString(jsonBytes)
	link := "vmess://" + b64Config

	outbound, err := ParseProxyLink(link)
	if err != nil {
		t.Fatalf("failed to parse VMess link: %v", err)
	}

	if outbound["type"] != "vmess" {
		t.Errorf("expected protocol type 'vmess', got '%v'", outbound["type"])
	}

	transport, ok := outbound["transport"].(map[string]interface{})
	if !ok {
		t.Fatalf("outbound transport map is missing or invalid")
	}

	if transport["type"] != "http" {
		t.Errorf("expected transport type 'http', got '%v'", transport["type"])
	}

	if transport["path"] != "/testing" {
		t.Errorf("expected transport path '/testing', got '%v'", transport["path"])
	}

	if transport["method"] != "POST" {
		t.Errorf("expected transport method 'POST', got '%v'", transport["method"])
	}

	// Test VMess default method
	delete(vmessConfig, "method")
	jsonBytesDefault, _ := json.Marshal(vmessConfig)
	b64ConfigDefault := base64.StdEncoding.EncodeToString(jsonBytesDefault)
	linkDefault := "vmess://" + b64ConfigDefault

	outboundDefault, err := ParseProxyLink(linkDefault)
	if err != nil {
		t.Fatalf("failed to parse VMess default link: %v", err)
	}
	transportDefault := outboundDefault["transport"].(map[string]interface{})
	if transportDefault["method"] != "GET" {
		t.Errorf("expected default VMess transport method 'GET', got '%v'", transportDefault["method"])
	}
}

func TestParseProxyLink_ALPN_Hysteria2(t *testing.T) {
	// Default Hysteria2 ALPN: hy2
	linkDefault := "hysteria2://password@example.com:443#hy2-test"
	outboundDefault, err := ParseProxyLink(linkDefault)
	if err != nil {
		t.Fatalf("failed to parse Hysteria2 link: %v", err)
	}

	if outboundDefault["type"] != "hysteria2" {
		t.Errorf("expected protocol type 'hysteria2', got '%v'", outboundDefault["type"])
	}

	tls, ok := outboundDefault["tls"].(map[string]interface{})
	if !ok {
		t.Fatalf("outbound tls map is missing or invalid")
	}

	alpn, ok := tls["alpn"].([]string)
	if !ok {
		t.Fatalf("outbound tls alpn is missing or invalid")
	}

	if len(alpn) != 1 || alpn[0] != "hy2" {
		t.Errorf("expected default Hysteria2 ALPN 'hy2', got '%v'", alpn)
	}

	// Custom Hysteria2 ALPN: h3,hy2
	linkCustom := "hysteria2://password@example.com:443?alpn=h3,hy2#hy2-test"
	outboundCustom, err := ParseProxyLink(linkCustom)
	if err != nil {
		t.Fatalf("failed to parse Hysteria2 custom link: %v", err)
	}

	tlsCustom := outboundCustom["tls"].(map[string]interface{})
	alpnCustom := tlsCustom["alpn"].([]string)
	if len(alpnCustom) != 2 || alpnCustom[0] != "h3" || alpnCustom[1] != "hy2" {
		t.Errorf("expected custom Hysteria2 ALPN ['h3', 'hy2'], got '%v'", alpnCustom)
	}
}

func TestBlockTorrentRuleGeneration(t *testing.T) {
	node := map[string]interface{}{
		"type":        "vless",
		"tag":         "test-node",
		"server":      "1.1.1.1",
		"server_port": 443,
		"uuid":        "a1b2c3d4-e5f6-7a8b-9c0d-1e2f3a4b5c6d",
	}

	hasTorrentRejectRule := func(cfg map[string]interface{}) bool {
		route, ok := cfg["route"].(map[string]interface{})
		if !ok {
			return false
		}
		rules, ok := route["rules"].([]map[string]interface{})
		if !ok {
			return false
		}
		for _, r := range rules {
			if r["action"] == "reject" {
				if proto, ok := r["protocol"].([]string); ok {
					for _, p := range proto {
						if p == "bittorrent" {
							return true
						}
					}
				}
			}
		}
		return false
	}

	// 1. blockTorrent: true -> rule present
	cfgBlocked, err := GenerateConfigWithCandidates(nil, node, Settings{BlockTorrent: true}, false, "cache.db", "secret")
	if err != nil {
		t.Fatalf("GenerateConfigWithCandidates failed with BlockTorrent=true: %v", err)
	}
	if !hasTorrentRejectRule(cfgBlocked) {
		t.Errorf("expected bittorrent reject rule when BlockTorrent is true, but not found")
	}

	// 2. blockTorrent: false -> rule absent
	cfgUnblocked, err := GenerateConfigWithCandidates(nil, node, Settings{BlockTorrent: false}, false, "cache.db", "secret")
	if err != nil {
		t.Fatalf("GenerateConfigWithCandidates failed with BlockTorrent=false: %v", err)
	}
	if hasTorrentRejectRule(cfgUnblocked) {
		t.Errorf("unexpected bittorrent reject rule when BlockTorrent is false")
	}
}

func TestBlockQuicRuleGeneration(t *testing.T) {
	node := map[string]interface{}{
		"type":        "vless",
		"tag":         "test-node",
		"server":      "1.1.1.1",
		"server_port": 443,
		"uuid":        "a1b2c3d4-e5f6-7a8b-9c0d-1e2f3a4b5c6d",
	}

	hasQuicRejectRule := func(cfg map[string]interface{}) bool {
		route, ok := cfg["route"].(map[string]interface{})
		if !ok {
			return false
		}
		rules, ok := route["rules"].([]map[string]interface{})
		if !ok {
			return false
		}
		for _, r := range rules {
			if r["action"] == "reject" && r["network"] == "udp" {
				if ports, ok := r["port"].([]int); ok {
					for _, p := range ports {
						if p == 443 {
							return true
						}
					}
				}
			}
		}
		return false
	}

	// 1. blockQuic: true -> rule present
	cfgBlocked, err := GenerateConfigWithCandidates(nil, node, Settings{BlockQuic: true}, false, "cache.db", "secret")
	if err != nil {
		t.Fatalf("GenerateConfigWithCandidates failed with BlockQuic=true: %v", err)
	}
	if !hasQuicRejectRule(cfgBlocked) {
		t.Errorf("expected UDP 443 reject rule when BlockQuic is true, but not found")
	}

	// 2. blockQuic: false -> rule absent
	cfgUnblocked, err := GenerateConfigWithCandidates(nil, node, Settings{BlockQuic: false}, false, "cache.db", "secret")
	if err != nil {
		t.Fatalf("GenerateConfigWithCandidates failed with BlockQuic=false: %v", err)
	}
	if hasQuicRejectRule(cfgUnblocked) {
		t.Errorf("unexpected UDP 443 reject rule when BlockQuic is false")
	}
}

func TestUserVLESSLink_AndSelectorInterrupt(t *testing.T) {
	link := "vless://a4f1995f-d7f4-4dab-a8b3-92928935b965@89.125.124.164:443?encryption=none&flow=xtls-rprx-vision&fp=chrome&pbk=lyTiDvHAVE3O7m7iFg-qqy0W9DIqfQfQLjAKUzyYbQ0&security=reality&sid=e64fb149f92adb&sni=www.pgroup.com&spx=%2Fd116a777793e115&support-x25519mlkem768=true&type=tcp#test-n8944ohj"

	outbound, err := ParseProxyLink(link)
	if err != nil {
		t.Fatalf("failed to parse user VLESS link: %v", err)
	}

	if outbound["type"] != "vless" {
		t.Errorf("type = %v, want vless", outbound["type"])
	}
	if outbound["flow"] != "xtls-rprx-vision" {
		t.Errorf("flow = %v, want xtls-rprx-vision", outbound["flow"])
	}
	if outbound["server"] != "89.125.124.164" {
		t.Errorf("server = %v, want 89.125.124.164", outbound["server"])
	}
	tls, ok := outbound["tls"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing tls section in parsed outbound")
	}
	reality, ok := tls["reality"].(map[string]interface{})
	if !ok || reality["public_key"] != "lyTiDvHAVE3O7m7iFg-qqy0W9DIqfQfQLjAKUzyYbQ0" {
		t.Errorf("invalid reality public_key: %v", reality)
	}

	// Now verify that selector and urltest outbounds have interrupt_exist_connections = true
	cand2 := map[string]interface{}{
		"type":        "vless",
		"tag":         "node-2",
		"server":      "1.1.1.1",
		"server_port": 443,
		"uuid":        "00000000-0000-0000-0000-000000000002",
	}
	cfg, err := GenerateConfigWithCandidates([]map[string]interface{}{outbound, cand2}, outbound, Settings{}, false, "cache.db", "secret")
	if err != nil {
		t.Fatalf("GenerateConfigWithCandidates failed: %v", err)
	}

	outbounds, ok := cfg["outbounds"].([]interface{})
	if !ok {
		t.Fatalf("cfg missing outbounds")
	}

	var foundSelector, foundURLTest bool
	for _, ob := range outbounds {
		obMap, ok := ob.(map[string]interface{})
		if !ok {
			continue
		}
		if obMap["type"] == "selector" && obMap["tag"] == OutboundSelectorTag {
			foundSelector = true
			if obMap["interrupt_exist_connections"] != true {
				t.Errorf("selector interrupt_exist_connections = %v, want true", obMap["interrupt_exist_connections"])
			}
		}
		if obMap["type"] == "urltest" && obMap["tag"] == OutboundAutoTag {
			foundURLTest = true
			if obMap["interrupt_exist_connections"] != true {
				t.Errorf("urltest interrupt_exist_connections = %v, want true", obMap["interrupt_exist_connections"])
			}
		}
	}
	if !foundSelector {
		t.Errorf("selector outbound not found")
	}
	if !foundURLTest {
		t.Errorf("urltest outbound not found")
	}
}


