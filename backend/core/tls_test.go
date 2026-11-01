package core

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
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
	if cfg.ClientSessionCache == nil {
		t.Fatal("expected ClientSessionCache to be configured")
	}
}

func TestSecureTLSSessionResumption(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer ts.Close()

	cfg1 := SecureTLSConfig("example.com")
	cfg1.RootCAs = x509.NewCertPool()
	cfg1.RootCAs.AddCert(ts.Certificate())

	// First dial: full handshake, read HTTP response so post-handshake NewSessionTicket is consumed
	conn1, err := tls.Dial("tcp", ts.Listener.Addr().String(), cfg1)
	if err != nil {
		t.Fatalf("first dial failed: %v", err)
	}
	_, _ = conn1.Write([]byte("GET / HTTP/1.1\r\nHost: example.com\r\nConnection: close\r\n\r\n"))
	buf := make([]byte, 256)
	_, _ = conn1.Read(buf)
	state1 := conn1.ConnectionState()
	conn1.Close()

	if state1.DidResume {
		t.Errorf("first connection should not be resumed")
	}

	// Give a tiny moment for ticket ingestion into clientSessionCache
	time.Sleep(10 * time.Millisecond)

	// Second dial via fresh SecureTLSConfig to the same serverName: must resume!
	cfg2 := SecureTLSConfig("example.com")
	cfg2.RootCAs = x509.NewCertPool()
	cfg2.RootCAs.AddCert(ts.Certificate())

	conn2, err := tls.Dial("tcp", ts.Listener.Addr().String(), cfg2)
	if err != nil {
		t.Fatalf("second dial failed: %v", err)
	}
	defer conn2.Close()
	state2 := conn2.ConnectionState()

	if !state2.DidResume {
		t.Errorf("second TLS connection should have resumed from ClientSessionCache")
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
