package core

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIsPublicIP(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		// Public IPv4 & IPv6
		{"8.8.8.8", true},
		{"1.1.1.1", true},
		{"142.250.190.46", true},
		{"2606:4700:4700::1111", true},
		{"2001:4860:4860::8888", true},

		// Loopback
		{"127.0.0.1", false},
		{"127.0.0.2", false},
		{"127.255.255.255", false},
		{"::1", false},

		// Cloud Metadata & Link-Local (AWS/GCP/Azure/DigitalOcean 169.254.169.254)
		{"169.254.169.254", false},
		{"169.254.0.1", false},
		{"169.254.255.254", false},
		{"fe80::1", false},

		// RFC 1918 Private IPv4
		{"10.0.0.1", false},
		{"10.255.255.254", false},
		{"172.16.0.1", false},
		{"172.31.255.254", false},
		{"192.168.0.1", false},
		{"192.168.1.1", false},
		{"192.168.254.254", false},

		// RFC 4193 Unique Local IPv6 (ULA)
		{"fc00::1", false},
		{"fd12:3456:789a::1", false},

		// CGNAT / Carrier-Grade NAT (RFC 6598)
		{"100.64.0.1", false},
		{"100.127.255.254", false},

		// Benchmarking & FakeIP (RFC 2544 / sing-box fakeip)
		{"198.18.0.1", false},
		{"198.19.255.254", false},

		// Broadcast & Current network
		{"0.0.0.0", false},
		{"0.1.2.3", false},
		{"255.255.255.255", false},

		// Documentation & Test
		{"192.0.2.1", false},
		{"198.51.100.1", false},
		{"203.0.113.1", false},

		// Multicast & Class E
		{"224.0.0.1", false},
		{"240.0.0.1", false},

		// Unspecified IPv6
		{"::", false},

		// IPv4-mapped IPv6 (bypass attempt)
		{"::ffff:127.0.0.1", false},
		{"::ffff:169.254.169.254", false},
		{"::ffff:192.168.1.1", false},
		{"::ffff:10.0.0.1", false},
		{"::ffff:8.8.8.8", true},
	}

	for _, tc := range cases {
		ip := net.ParseIP(tc.ip)
		if ip == nil {
			t.Fatalf("failed to parse IP %q in test cases", tc.ip)
		}
		got := IsPublicIP(ip)
		if got != tc.want {
			t.Errorf("IsPublicIP(%s) = %v; want %v", tc.ip, got, tc.want)
		}
	}
}

func TestFetchSubscriptionRejectsSSRF(t *testing.T) {
	ssrfURLs := []string{
		"https://127.0.0.1/sub",
		"https://127.0.0.1:8443/sub",
		"https://localhost:8443/sub",
		"https://169.254.169.254/latest/meta-data/",
		"https://192.168.1.1:8443/sub",
		"https://10.0.0.1/sub",
		"https://172.16.0.1/sub",
		"https://[::1]/sub",
		"https://[::ffff:127.0.0.1]/sub",
		"https://[fc00::1]/sub",
		"https://[fe80::1]/sub",
		"https://router.local/sub",
		"https://internal.service.localhost/sub",
	}

	ctx := context.Background()
	for _, rawURL := range ssrfURLs {
		_, err := FetchSubscriptionContext(ctx, rawURL)
		if err == nil {
			t.Errorf("FetchSubscriptionContext(%q) succeeded, want error rejecting SSRF/private target", rawURL)
		}
	}
}

func TestFetchSubscriptionContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := FetchSubscriptionContext(ctx, "https://example.com/sub")
	if err == nil {
		t.Fatal("expected error with pre-canceled context, got nil")
	}
	if !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "canceled") {
		t.Errorf("expected context.Canceled error, got %v", err)
	}
}

func TestFetchSubscriptionRejectsRedirectToPrivateIP(t *testing.T) {
	// Start an HTTPS test server that redirects to a private link-local address
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://127.0.0.1:8443/secret", http.StatusFound)
	}))
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := FetchSubscriptionContext(ctx, ts.URL)
	if err == nil {
		t.Fatalf("expected redirect to 127.0.0.1 to be rejected, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "private") &&
		!strings.Contains(strings.ToLower(err.Error()), "restricted") &&
		!strings.Contains(strings.ToLower(err.Error()), "ssrf") &&
		!strings.Contains(strings.ToLower(err.Error()), "blocked") {
		t.Logf("got error on redirect (expected): %v", err)
	}
}

