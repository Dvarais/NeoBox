package core

import (
	"crypto/tls"
	"net/http"
	"testing"
	"time"
)

func TestSystemCertPool(t *testing.T) {
	pool := SystemCertPool()
	// System roots may or may not be available in minimal CI environments,
	// but calling SystemCertPool must never panic or return an error.
	_ = pool
}

func TestSecureTLSConfig(t *testing.T) {
	cfg := SecureTLSConfig("cloudflare-dns.com")
	if cfg == nil {
		t.Fatal("SecureTLSConfig returned nil")
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("expected MinVersion TLS 1.2, got %v", cfg.MinVersion)
	}
	if cfg.ServerName != "cloudflare-dns.com" {
		t.Errorf("expected ServerName cloudflare-dns.com, got %s", cfg.ServerName)
	}
}

func TestDefaultHTTPClient(t *testing.T) {
	client := DefaultHTTPClient(5 * time.Second)
	if client == nil {
		t.Fatal("DefaultHTTPClient returned nil")
	}
	if client.Timeout != 5*time.Second {
		t.Errorf("expected timeout 5s, got %v", client.Timeout)
	}
	if client.Transport == nil {
		t.Error("client.Transport is nil")
	}
	tr, ok := client.Transport.(*http.Transport)
	if !ok || tr == nil {
		t.Errorf("expected *http.Transport, got %T", client.Transport)
	}
}
