package core

import (
	"net"
	"strconv"
	"testing"
)

// Go cannot build the address strings from the host/port constants at compile
// time, so the two forms are written out separately. This test is what keeps
// them from drifting apart.
func TestPortConstantsAgree(t *testing.T) {
	tests := []struct {
		name string
		addr string
		port int
	}{
		{"proxy inbound", ProxyListenAddr, ProxyListenPort},
		{"clash api", ClashAPIAddr, ClashAPIPort},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want := net.JoinHostPort(ProxyListenHost, strconv.Itoa(tc.port))
			if tc.addr != want {
				t.Errorf("address constant %q disagrees with host/port constants (%q)", tc.addr, want)
			}
		})
	}
}

// The inbounds must stay on loopback: these ports carry unauthenticated proxy
// traffic and the Clash control API.
func TestPortsAreLoopbackOnly(t *testing.T) {
	ip := net.ParseIP(ProxyListenHost)
	if ip == nil || !ip.IsLoopback() {
		t.Fatalf("ProxyListenHost = %q, which is not a loopback address", ProxyListenHost)
	}
}

// The generated config must use the constants, not literals that drifted.
func TestGenerateConfigUsesPortConstants(t *testing.T) {
	outbound := map[string]interface{}{
		"type":   "vless",
		"tag":    "proxy",
		"server": "203.0.113.10",
	}

	config, err := GenerateConfig(outbound, Settings{}, false, "cache.db", "secret")
	if err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
	}

	inbounds, ok := config["inbounds"].([]map[string]interface{})
	if !ok || len(inbounds) == 0 {
		t.Fatal("config has no inbounds")
	}
	if got := inbounds[0]["listen_port"]; got != ProxyListenPort {
		t.Errorf("listen_port = %v, want %d", got, ProxyListenPort)
	}
	if got := inbounds[0]["listen"]; got != ProxyListenHost {
		t.Errorf("listen = %v, want %q", got, ProxyListenHost)
	}

	experimental, ok := config["experimental"].(map[string]interface{})
	if !ok {
		t.Fatal("config has no experimental section")
	}
	clash, ok := experimental["clash_api"].(map[string]interface{})
	if !ok {
		t.Fatal("config has no clash_api section")
	}
	if got := clash["external_controller"]; got != ClashAPIAddr {
		t.Errorf("external_controller = %v, want %q", got, ClashAPIAddr)
	}
}
