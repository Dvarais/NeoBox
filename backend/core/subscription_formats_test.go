package core

import (
	"strings"
	"testing"
)

// Every link a converter emits must survive the trip back through
// ParseProxyLink: the two sit at opposite ends of the same pipe, and a field
// that only one of them knows about is a node that imports but cannot connect.
func assertRoundTrip(t *testing.T, link string, want map[string]interface{}) {
	t.Helper()

	if !IsProxyLink(link) {
		t.Fatalf("IsProxyLink(%q) = false, want true", link)
	}
	outbound, err := ParseProxyLink(link)
	if err != nil {
		t.Fatalf("ParseProxyLink(%q) failed: %v", link, err)
	}
	for key, expected := range want {
		if got := outbound[key]; got != expected {
			t.Errorf("link %q: outbound[%q] = %#v, want %#v", link, key, got, expected)
		}
	}
}

func TestParseClashYAML(t *testing.T) {
	payload := []byte(`
port: 7890
proxies:
  - name: "Reality node"
    type: vless
    server: a.example.com
    port: 443
    uuid: 11111111-2222-3333-4444-555555555555
    flow: xtls-rprx-vision
    tls: true
    servername: www.microsoft.com
    client-fingerprint: chrome
    reality-opts:
      public-key: PUBKEY
      short-id: ab12
  - name: "WS node"
    type: trojan
    server: b.example.com
    port: 8443
    password: "tr0jan pass"
    sni: b.example.com
    network: ws
    ws-opts:
      path: /ray
      headers:
        Host: cdn.example.com
  - name: "SS node"
    type: ss
    server: c.example.com
    port: 8388
    cipher: aes-256-gcm
    password: "p@ss/word"
  - name: "Hy2 node"
    type: hysteria2
    server: d.example.com
    port: 443
    password: hy2pass
    obfs: salamander
    obfs-password: obfspw
    skip-cert-verify: true
  - name: "Socks node"
    type: socks5
    server: e.example.com
    port: 1080
    username: user
    password: pw
  - name: "HTTP node"
    type: http
    server: f.example.com
    port: 8080
    tls: true
  - name: "Legacy SSR"
    type: ssr
    server: g.example.com
    port: 443
proxy-groups:
  - name: auto
    type: url-test
`)

	links := parseClashYAML(payload)
	// The ssr entry is dropped: the bundled sing-box cannot run it.
	if len(links) != 6 {
		t.Fatalf("parseClashYAML returned %d links, want 6:\n%s", len(links), strings.Join(links, "\n"))
	}

	assertRoundTrip(t, links[0], map[string]interface{}{
		"type":   "vless",
		"tag":    "Reality node",
		"server": "a.example.com",
		"uuid":   "11111111-2222-3333-4444-555555555555",
		"flow":   "xtls-rprx-vision",
	})
	if !strings.Contains(links[0], "security=reality") || !strings.Contains(links[0], "pbk=PUBKEY") {
		t.Errorf("vless link lost its reality settings: %s", links[0])
	}

	assertRoundTrip(t, links[1], map[string]interface{}{
		"type":     "trojan",
		"tag":      "WS node",
		"password": "tr0jan pass",
	})

	assertRoundTrip(t, links[2], map[string]interface{}{
		"type":     "shadowsocks",
		"tag":      "SS node",
		"method":   "aes-256-gcm",
		"password": "p@ss/word",
	})

	assertRoundTrip(t, links[3], map[string]interface{}{
		"type":     "hysteria2",
		"tag":      "Hy2 node",
		"password": "hy2pass",
	})

	assertRoundTrip(t, links[4], map[string]interface{}{
		"type":     "socks",
		"tag":      "Socks node",
		"username": "user",
		"password": "pw",
		"version":  "5",
	})

	assertRoundTrip(t, links[5], map[string]interface{}{
		"type":        "http",
		"tag":         "HTTP node",
		"server":      "f.example.com",
		"server_port": 8080,
	})
}

// The transport of a Clash proxy has to survive conversion, otherwise the node
// imports and then fails to connect for a reason nothing reports.
func TestClashTransportSurvives(t *testing.T) {
	payload := []byte(`
proxies:
  - name: ws
    type: trojan
    server: b.example.com
    port: 443
    password: pw
    network: ws
    ws-opts:
      path: /ray
      headers:
        Host: cdn.example.com
  - name: grpc
    type: vless
    server: c.example.com
    port: 443
    uuid: 11111111-2222-3333-4444-555555555555
    tls: true
    network: grpc
    grpc-opts:
      grpc-service-name: TunSvc
`)

	links := parseClashYAML(payload)
	if len(links) != 2 {
		t.Fatalf("got %d links, want 2", len(links))
	}

	wsOutbound, err := ParseProxyLink(links[0])
	if err != nil {
		t.Fatalf("ParseProxyLink(%q): %v", links[0], err)
	}
	transport, _ := wsOutbound["transport"].(map[string]interface{})
	if transport == nil || transport["type"] != "ws" || transport["path"] != "/ray" {
		t.Errorf("ws transport lost: %#v (from %s)", transport, links[0])
	}
	if headers, _ := transport["headers"].(map[string]string); headers["Host"] != "cdn.example.com" {
		t.Errorf("ws Host header lost: %#v (from %s)", transport["headers"], links[0])
	}

	grpcOutbound, err := ParseProxyLink(links[1])
	if err != nil {
		t.Fatalf("ParseProxyLink(%q): %v", links[1], err)
	}
	transport, _ = grpcOutbound["transport"].(map[string]interface{})
	if transport == nil || transport["type"] != "grpc" || transport["service_name"] != "TunSvc" {
		t.Errorf("grpc transport lost: %#v (from %s)", transport, links[1])
	}
}

func TestParseSingboxOutbounds(t *testing.T) {
	payload := []byte(`{
	  "log": {"level": "info"},
	  "outbounds": [
	    {
	      "type": "vless",
	      "tag": "vless-reality",
	      "server": "a.example.com",
	      "server_port": 443,
	      "uuid": "11111111-2222-3333-4444-555555555555",
	      "flow": "xtls-rprx-vision",
	      "tls": {
	        "enabled": true,
	        "server_name": "www.microsoft.com",
	        "utls": {"enabled": true, "fingerprint": "chrome"},
	        "reality": {"enabled": true, "public_key": "PUBKEY", "short_id": "ab12"}
	      }
	    },
	    {
	      "type": "shadowsocks",
	      "tag": "ss-node",
	      "server": "c.example.com",
	      "server_port": 8388,
	      "method": "aes-256-gcm",
	      "password": "sspass"
	    },
	    {
	      "type": "hysteria2",
	      "tag": "hy2-node",
	      "server": "d.example.com",
	      "server_port": 443,
	      "password": "hy2pass",
	      "obfs": {"type": "salamander", "password": "obfspw"},
	      "tls": {"enabled": true, "server_name": "d.example.com"}
	    },
	    {
	      "type": "anytls",
	      "tag": "anytls-node",
	      "server": "e.example.com",
	      "server_port": 8443,
	      "password": "anypass",
	      "tls": {"enabled": true, "server_name": "e.example.com", "insecure": true}
	    },
	    {"type": "selector", "tag": "auto", "outbounds": ["vless-reality"]},
	    {"type": "direct", "tag": "direct"}
	  ]
	}`)

	links := parseSingboxOutbounds(payload)
	// selector and direct are not proxies and carry no server.
	if len(links) != 4 {
		t.Fatalf("parseSingboxOutbounds returned %d links, want 4:\n%s", len(links), strings.Join(links, "\n"))
	}

	assertRoundTrip(t, links[0], map[string]interface{}{
		"type": "vless",
		"tag":  "vless-reality",
		"uuid": "11111111-2222-3333-4444-555555555555",
		"flow": "xtls-rprx-vision",
	})
	assertRoundTrip(t, links[1], map[string]interface{}{
		"type":     "shadowsocks",
		"tag":      "ss-node",
		"method":   "aes-256-gcm",
		"password": "sspass",
	})
	assertRoundTrip(t, links[2], map[string]interface{}{
		"type":     "hysteria2",
		"tag":      "hy2-node",
		"password": "hy2pass",
	})
	assertRoundTrip(t, links[3], map[string]interface{}{
		"type":     "anytls",
		"tag":      "anytls-node",
		"password": "anypass",
	})

	hy2, _ := ParseProxyLink(links[2])
	obfs, _ := hy2["obfs"].(map[string]interface{})
	if obfs == nil || obfs["type"] != "salamander" || obfs["password"] != "obfspw" {
		t.Errorf("hysteria2 obfs lost: %#v (from %s)", hy2["obfs"], links[2])
	}
}

// A bare array of sing-box outbounds is served by some panels in place of a
// whole config.
func TestParseSingboxOutboundsArray(t *testing.T) {
	payload := []byte(`[
	  {"type": "trojan", "tag": "t", "server": "b.example.com", "server_port": 443,
	   "password": "pw", "tls": {"enabled": true, "server_name": "b.example.com"}}
	]`)

	links := parseSingboxOutbounds(payload)
	if len(links) != 1 {
		t.Fatalf("got %d links, want 1", len(links))
	}
	assertRoundTrip(t, links[0], map[string]interface{}{
		"type":     "trojan",
		"tag":      "t",
		"password": "pw",
	})
}

// VMess links carry their payload as base64, which must not be cut short by a
// "/" in the standard alphabet — the reason the emitter uses the URL-safe one.
func TestVmessRoundTrip(t *testing.T) {
	n := node{
		protocol: "vmess",
		name:     "vmess node",
		server:   "a.example.com",
		port:     443,
		uuid:     "11111111-2222-3333-4444-555555555555",
		tls:      true,
		sni:      "a.example.com",
		network:  "ws",
		path:     "/ray",
		host:     "cdn.example.com",
	}

	assertRoundTrip(t, n.link(), map[string]interface{}{
		"type":        "vmess",
		"tag":         "vmess node",
		"server":      "a.example.com",
		"server_port": 443,
		"uuid":        "11111111-2222-3333-4444-555555555555",
	})
}

// A Shadowsocks plugin is what the server expects on the wire, so losing it in
// conversion produces a node that imports cleanly and then cannot connect.
func TestShadowsocksPlugins(t *testing.T) {
	payload := []byte(`
proxies:
  - name: obfs node
    type: ss
    server: a.example.com
    port: 8388
    cipher: aes-256-gcm
    password: pw
    plugin: obfs
    plugin-opts:
      mode: http
      host: cdn.example.com
  - name: v2ray node
    type: ss
    server: b.example.com
    port: 8388
    cipher: aes-256-gcm
    password: pw
    plugin: v2ray-plugin
    plugin-opts:
      mode: websocket
      host: h.example.com
      path: /ray
      tls: true
  - name: shadow-tls node
    type: ss
    server: c.example.com
    port: 8388
    cipher: aes-256-gcm
    password: pw
    plugin: shadow-tls
    plugin-opts:
      host: cover.example.com
`)

	links := parseClashYAML(payload)
	// shadow-tls needs a chained outbound, which the core cannot load from a
	// single node, so that entry is dropped instead of imported broken.
	if len(links) != 2 {
		t.Fatalf("got %d links, want 2:\n%s", len(links), strings.Join(links, "\n"))
	}

	assertRoundTrip(t, links[0], map[string]interface{}{
		"type":        "shadowsocks",
		"plugin":      "obfs-local",
		"plugin_opts": "obfs=http;obfs-host=cdn.example.com",
	})
	assertRoundTrip(t, links[1], map[string]interface{}{
		"type":        "shadowsocks",
		"plugin":      "v2ray-plugin",
		"plugin_opts": "mode=websocket;host=h.example.com;path=/ray;tls",
	})
}

// Links arriving from other clients carry the plugin in SIP002 form.
func TestShadowsocksPluginFromLink(t *testing.T) {
	link := "ss://YWVzLTI1Ni1nY206cHc@a.example.com:8388" +
		"?plugin=obfs-local%3Bobfs%3Dhttp%3Bobfs-host%3Dcdn.example.com#node"

	assertRoundTrip(t, link, map[string]interface{}{
		"plugin":      "obfs-local",
		"plugin_opts": "obfs=http;obfs-host=cdn.example.com",
	})

	// An alias for the same plugin, and a plugin the core does not have.
	aliased, err := ParseProxyLink("ss://YWVzLTI1Ni1nY206cHc@a.example.com:8388?plugin=simple-obfs%3Bobfs%3Dtls")
	if err != nil {
		t.Fatalf("ParseProxyLink: %v", err)
	}
	if aliased["plugin"] != "obfs-local" {
		t.Errorf("simple-obfs alias = %v, want obfs-local", aliased["plugin"])
	}

	unknown, err := ParseProxyLink("ss://YWVzLTI1Ni1nY206cHc@a.example.com:8388?plugin=shadow-tls%3Bhost%3Dx")
	if err != nil {
		t.Fatalf("ParseProxyLink: %v", err)
	}
	if _, present := unknown["plugin"]; present {
		t.Errorf("unsupported plugin was kept: %v", unknown["plugin"])
	}
}

// A node without a server or port cannot be connected to and must not reach the
// server list.
func TestIncompleteNodesAreDropped(t *testing.T) {
	for name, n := range map[string]node{
		"no server":   {protocol: "vless", port: 443, uuid: "x"},
		"no port":     {protocol: "vless", server: "a.example.com", uuid: "x"},
		"no uuid":     {protocol: "vless", server: "a.example.com", port: 443},
		"no cipher":   {protocol: "ss", server: "a.example.com", port: 443, password: "x"},
		"unsupported": {protocol: "ssr", server: "a.example.com", port: 443},
	} {
		if link := n.link(); link != "" {
			t.Errorf("%s: got link %q, want it dropped", name, link)
		}
	}
}
