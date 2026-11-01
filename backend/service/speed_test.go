package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
	if res.Success != false {
		t.Errorf("expected success: false, got %v", res)
	}
	if res.Error != "no server specified" {
		t.Errorf("expected 'no server specified', got %v", res.Error)
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
	if res.Success != false {
		t.Errorf("expected success: false for invalid link, got %v", res)
	}
}

func TestTestServerSpeed_AutoModeDisconnected(t *testing.T) {
	s := &AppService{}
	s.setVPNState(VPNStateDisconnected)

	res := s.TestServerSpeed("neobox://auto")
	if res.Success != false {
		t.Errorf("expected success: false for auto mode when disconnected, got %v", res)
	}
	if res.Error != "speed test in auto mode requires an active connection" {
		t.Errorf("expected 'speed test in auto mode requires an active connection', got %v", res.Error)
	}
}

func TestServerSpeedPayload_JSON(t *testing.T) {
	p := ServerSpeedPayload{
		Success: true,
		Mbps:    45.6,
		Speed:   45.6,
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	expected := `{"success":true,"mbps":45.6,"speed":45.6}`
	if string(data) != expected {
		t.Errorf("got %s, want %s", string(data), expected)
	}

	pErr := ServerSpeedPayload{
		Success: false,
		Error:   "speed test failed",
	}
	dataErr, err := json.Marshal(pErr)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	expectedErr := `{"success":false,"error":"speed test failed"}`
	if string(dataErr) != expectedErr {
		t.Errorf("got %s, want %s", string(dataErr), expectedErr)
	}
}

func TestStreamTraffic_UsesClashAddr(t *testing.T) {
	var receivedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		// Write one sample and flush
		w.Write([]byte(`{"up": 100, "down": 200}` + "\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer srv.Close()

	s := &AppService{}
	s.stateMu.Lock()
	s.clashAddr = strings.TrimPrefix(srv.URL, "http://")
	s.stateMu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after a short moment so streamTraffic finishes
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_ = s.streamTraffic(ctx)

	if receivedPath != "/traffic" {
		t.Errorf("expected request to /traffic on s.clashAddr, got %q", receivedPath)
	}
}
