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
