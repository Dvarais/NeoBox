package service

import (
	"net"
	"strings"
	"testing"
)

func TestIsPrivateIPv4(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		{"192.168.1.1", true},
		{"192.168.0.254", true},
		{"10.0.0.5", true},
		{"10.255.255.1", true},
		{"172.16.0.1", true},
		{"172.31.255.254", true},
		{"172.32.0.1", false}, // outside 172.16.0.0/12
		{"8.8.8.8", false},
		{"127.0.0.1", false},
		{"1.1.1.1", false},
		{"::1", false},
	}

	for _, tc := range tests {
		ip := net.ParseIP(tc.ip)
		if got := isPrivateIPv4(ip); got != tc.want {
			t.Errorf("isPrivateIPv4(%q) = %v, want %v", tc.ip, got, tc.want)
		}
	}
}

func TestGetLanIPs(t *testing.T) {
	svc := &AppService{}
	ips, err := svc.GetLanIPs()
	if err != nil {
		t.Fatalf("GetLanIPs returned error: %v", err)
	}

	for _, info := range ips {
		parsed := net.ParseIP(info.IP)
		if parsed == nil {
			t.Errorf("GetLanIPs returned invalid IP: %q", info.IP)
		}
		if parsed.IsLoopback() {
			t.Errorf("GetLanIPs returned loopback IP: %q", info.IP)
		}
		if info.Name == "" {
			t.Errorf("GetLanIPs returned empty interface name for IP %q", info.IP)
		}
	}
}

func TestGenerateQRCodePNG(t *testing.T) {
	svc := &AppService{}

	// Test valid text
	dataURI, err := svc.GenerateQRCodePNG("socks5://192.168.1.100:20809#NeoBox-LAN")
	if err != nil {
		t.Fatalf("GenerateQRCodePNG failed: %v", err)
	}
	if !strings.HasPrefix(dataURI, "data:image/png;base64,") {
		t.Errorf("GenerateQRCodePNG data URI does not have expected prefix: %q", dataURI[:30])
	}

	// Test empty text
	_, err = svc.GenerateQRCodePNG("   ")
	if err == nil {
		t.Errorf("GenerateQRCodePNG expected error for empty string, got nil")
	}
}
