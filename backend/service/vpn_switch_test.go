package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"NeoBox/backend/core"
)

func TestBuildCandidates(t *testing.T) {
	link1 := "vless://00000000-0000-0000-0000-000000000001@1.1.1.1:443?type=tcp#Node-Alpha"
	link2 := "vless://00000000-0000-0000-0000-000000000002@2.2.2.2:443?type=tcp#Node-Beta"
	link3DuplicateTag := "vless://00000000-0000-0000-0000-000000000003@3.3.3.3:443?type=tcp#Node-Beta"
	linkWG := "wireguard://0000000000000000000000000000000000000000000=@4.4.4.4:51820?address=10.0.0.2%2F32#WG-Node"

	s := serviceWithSubs(t, link1, link2, link3DuplicateTag, linkWG)

	activeOutbound, err := core.ParseProxyLink(link1)
	if err != nil {
		t.Fatalf("ParseProxyLink failed: %v", err)
	}

	candidates, linkToTag := s.buildCandidates(link1, activeOutbound)

	// activeOutbound, link2, link3 (linkWG should be excluded from selector candidates)
	if len(candidates) != 3 {
		t.Fatalf("expected 3 candidates (WG excluded), got %d", len(candidates))
	}

	if linkToTag[link1] != "Node-Alpha" {
		t.Errorf("link1 tag = %q, want %q", linkToTag[link1], "Node-Alpha")
	}

	if linkToTag[link2] != "Node-Beta" {
		t.Errorf("link2 tag = %q, want %q", linkToTag[link2], "Node-Beta")
	}

	// link3 should have deduplicated tag Node-Beta-2
	if linkToTag[link3DuplicateTag] != "Node-Beta-2" {
		t.Errorf("link3DuplicateTag tag = %q, want %q", linkToTag[link3DuplicateTag], "Node-Beta-2")
	}

	// WG should not be in linkToTag
	if _, ok := linkToTag[linkWG]; ok {
		t.Errorf("WireGuard node should not be in selector linkToTag")
	}
}

func TestBuildCandidates_ActiveWireGuard(t *testing.T) {
	linkWG := "wireguard://0000000000000000000000000000000000000000000=@4.4.4.4:51820?address=10.0.0.2%2F32#WG-Node"
	linkOther := "vless://00000000-0000-0000-0000-000000000001@1.1.1.1:443?type=tcp#Node-1"

	s := serviceWithSubs(t, linkWG, linkOther)
	activeOutbound, err := core.ParseProxyLink(linkWG)
	if err != nil {
		t.Fatalf("ParseProxyLink failed: %v", err)
	}

	candidates, linkToTag := s.buildCandidates(linkWG, activeOutbound)

	// When active outbound is WireGuard, no candidates should be pooled in selector
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate when active is WireGuard, got %d", len(candidates))
	}
	if linkToTag[linkWG] != "WG-Node" {
		t.Errorf("linkToTag[linkWG] = %q, want WG-Node", linkToTag[linkWG])
	}
}

func TestSwitchSelectorTag_SuccessAndFailure(t *testing.T) {
	var receivedMethod, receivedPath, receivedAuth string
	var receivedBody map[string]string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedPath = r.URL.Path
		receivedAuth = r.Header.Get("Authorization")

		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &receivedBody)

		if receivedBody["name"] == "error-tag" {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"message":"selector error"}`))
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	addr := strings.TrimPrefix(srv.URL, "http://")
	s := &AppService{}

	// Test success
	err := s.switchSelectorTag(context.Background(), addr, "test-secret", "Node-Beta")
	if err != nil {
		t.Fatalf("switchSelectorTag failed: %v", err)
	}
	if receivedMethod != http.MethodPut {
		t.Errorf("method = %s, want PUT", receivedMethod)
	}
	if receivedPath != "/proxies/"+core.OutboundSelectorTag {
		t.Errorf("path = %s, want /proxies/%s", receivedPath, core.OutboundSelectorTag)
	}
	if receivedAuth != "Bearer test-secret" {
		t.Errorf("auth = %s, want Bearer test-secret", receivedAuth)
	}
	if receivedBody["name"] != "Node-Beta" {
		t.Errorf("name = %s, want Node-Beta", receivedBody["name"])
	}

	// Test failure
	err = s.switchSelectorTag(context.Background(), addr, "test-secret", "error-tag")
	if err == nil {
		t.Fatal("expected error for error-tag, got nil")
	}
}

func TestSwitchServer_LifecycleAndGuards(t *testing.T) {
	link1 := "vless://00000000-0000-0000-0000-000000000001@1.1.1.1:443?type=tcp#Node-Alpha"
	link2 := "vless://00000000-0000-0000-0000-000000000002@2.2.2.2:443?type=tcp#Node-Beta"
	linkNotInSelector := "vless://00000000-0000-0000-0000-000000000003@3.3.3.3:443?type=tcp#Node-Gamma"

	s := serviceWithSubs(t, link1, link2)

	// 1. Not connected: should fail gracefully and report fallbackRequired
	res := s.SwitchServer(link2)
	if res["success"] == true || res["hotSwitched"] == true {
		t.Fatalf("SwitchServer while disconnected should not succeed: %v", res)
	}
	if res["fallbackRequired"] != true {
		t.Errorf("expected fallbackRequired: true, got %v", res["fallbackRequired"])
	}

	// 2. Set connected state, but link not in selector
	s.setVPNState(VPNStateConnected)
	s.stateMu.Lock()
	s.serverLinkToTag = map[string]string{link1: "Node-Alpha"}
	s.activeServerLink = link1
	s.stateMu.Unlock()

	res = s.SwitchServer(linkNotInSelector)
	if res["success"] == true || res["hotSwitched"] == true {
		t.Fatalf("SwitchServer for unknown link should not succeed: %v", res)
	}
	if res["fallbackRequired"] != true {
		t.Errorf("expected fallbackRequired: true, got %v", res["fallbackRequired"])
	}

	// 3. Already on the active server
	res = s.SwitchServer(link1)
	if res["success"] != true || res["hotSwitched"] != true {
		t.Fatalf("SwitchServer for current link should be immediate success: %v", res)
	}
}

func TestSwitchServer_EndToEndHotSwitch(t *testing.T) {
	link1 := "vless://00000000-0000-0000-0000-000000000001@1.1.1.1:443?type=tcp#Node-Alpha"
	link2 := "vless://00000000-0000-0000-0000-000000000002@2.2.2.2:443?type=tcp#Node-Beta"

	var receivedPath string
	var receivedBody map[string]string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &receivedBody)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	addr := strings.TrimPrefix(srv.URL, "http://")
	s := serviceWithSubs(t, link1, link2)

	s.setVPNState(VPNStateConnected)
	s.stateMu.Lock()
	s.clashSecret = "secret-abc"
	s.clashAddr = addr
	s.serverLinkToTag = map[string]string{
		link1: "Node-Alpha",
		link2: "Node-Beta",
	}
	s.activeServerLink = link1
	s.stateMu.Unlock()

	res := s.SwitchServer(link2)
	if res["success"] != true || res["hotSwitched"] != true {
		t.Fatalf("SwitchServer failed: %v", res)
	}

	if receivedPath != "/proxies/"+core.OutboundSelectorTag {
		t.Errorf("receivedPath = %s, want /proxies/%s", receivedPath, core.OutboundSelectorTag)
	}
	if receivedBody["name"] != "Node-Beta" {
		t.Errorf("receivedBody name = %s, want Node-Beta", receivedBody["name"])
	}

	s.stateMu.Lock()
	active := s.activeServerLink
	s.stateMu.Unlock()
	if active != link2 {
		t.Errorf("activeServerLink = %s, want %s", active, link2)
	}

	s.watchdogMu.Lock()
	wdLink := s.watchdogLink
	s.watchdogMu.Unlock()
	if wdLink != link2 {
		t.Errorf("watchdogLink = %s, want %s", wdLink, link2)
	}

	s.fileMu.Lock()
	secrets := s.readSecretSettingsLocked()
	s.fileMu.Unlock()
	if secrets["lastSelectedServer"] != link2 {
		t.Errorf("lastSelectedServer = %v, want %s", secrets["lastSelectedServer"], link2)
	}
}
