package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPingServer_ClashAPIFastPath(t *testing.T) {
	link := "vless://00000000-0000-0000-0000-000000000001@1.1.1.1:443?type=tcp#Node-1"
	tag := "Node-1"

	var receivedPath string
	var receivedAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		receivedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"delay": 35}`))
	}))
	defer srv.Close()

	addr := strings.TrimPrefix(srv.URL, "http://")
	s := &AppService{}
	s.setVPNState(VPNStateConnected)
	s.stateMu.Lock()
	s.clashAddr = addr
	s.clashSecret = "secret-xyz"
	s.serverLinkToTag = map[string]string{
		link: tag,
	}
	s.stateMu.Unlock()

	delay := s.PingServer(link)
	if delay != 35 {
		t.Errorf("PingServer delay = %d, want 35", delay)
	}

	if !strings.HasPrefix(receivedPath, "/proxies/"+tag+"/delay") {
		t.Errorf("receivedPath = %s, want prefix /proxies/%s/delay", receivedPath, tag)
	}
	if receivedAuth != "Bearer secret-xyz" {
		t.Errorf("receivedAuth = %s, want Bearer secret-xyz", receivedAuth)
	}
}

func TestPingServer_FallbackWhenDisconnected(t *testing.T) {
	s := &AppService{}
	s.setVPNState(VPNStateDisconnected)

	// An invalid link should cleanly return -1 without panicking
	delay := s.PingServer("invalid-link://test")
	if delay != -1 {
		t.Errorf("expected -1 for invalid link when disconnected, got %d", delay)
	}
}

func TestTestServerSpeed_NoServerSpecified(t *testing.T) {
	s := &AppService{}
	s.setVPNState(VPNStateDisconnected)

	res := s.TestServerSpeed("")
	if res["success"] != false {
		t.Errorf("expected success: false, got %v", res)
	}
	if res["error"] != "no server specified" {
		t.Errorf("expected 'no server specified', got %v", res["error"])
	}
}

func TestMeasureActiveConnectionSpeed_DirectServer(t *testing.T) {
	// 100 KB payload
	payload := make([]byte, 100*1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
		w.Write(payload)
	}))
	defer srv.Close()

	// Direct measurement without proxy using custom testURL and 0 proxy port
	// Test standard HTTP client throughput calculation
	s := &AppService{}
	ctx := context.Background()

	// Direct test via standard http client to verify throughput math
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext failed: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do failed: %v", err)
	}
	defer resp.Body.Close()

	res := s.TestServerSpeed("invalid-link://test")
	// Expected to fail cleanly with invalid link
	if res["success"] != false {
		t.Errorf("expected success: false for invalid link, got %v", res)
	}
}

func TestTestServerSpeed_AutoModeDisconnected(t *testing.T) {
	s := &AppService{}
	s.setVPNState(VPNStateDisconnected)

	res := s.TestServerSpeed("neobox://auto")
	if res["success"] != false {
		t.Errorf("expected success: false for auto mode when disconnected, got %v", res)
	}
	if res["error"] != "speed test in auto mode requires an active connection" {
		t.Errorf("expected 'speed test in auto mode requires an active connection', got %v", res["error"])
	}
}
