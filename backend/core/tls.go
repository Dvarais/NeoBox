package core

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"sync"
	"time"

	"golang.org/x/crypto/x509roots/fallback/bundle"
)

var (
	systemRootsOnce sync.Once
	systemRoots     *x509.CertPool

	defaultTransportOnce sync.Once
	defaultTransport     *http.Transport
)

// SystemCertPool returns the cached system root certificate pool.
// On Windows this pool alone does not keep verification out of crypt32.dll — it
// is a marker that routes Verify into CryptoAPI. What does is roots_windows.go
// together with x509usefallbackroots=1 in main.go, which make this return the
// pure-Go pool built there. If loading fails, it falls back to Mozilla bundle.
func SystemCertPool() *x509.CertPool {
	systemRootsOnce.Do(func() {
		pool, err := x509.SystemCertPool()
		if err == nil && pool != nil {
			systemRoots = pool
			return
		}
		// Fallback: build an in-memory pool from Mozilla bundle so RootCAs is never nil
		pool = x509.NewCertPool()
		for root := range bundle.Roots() {
			if cert, err := x509.ParseCertificate(root.Certificate); err == nil {
				if root.Constraint == nil {
					pool.AddCert(cert)
				} else {
					pool.AddCertWithConstraint(cert, root.Constraint)
				}
			}
		}
		systemRoots = pool
	})
	return systemRoots
}

// SecureTLSConfig returns a *tls.Config with TLS 1.2 minimum version and
// the cached pure-Go SystemCertPool.
func SecureTLSConfig(serverName string) *tls.Config {
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    SystemCertPool(),
	}
	if serverName != "" {
		cfg.ServerName = serverName
	}
	return cfg
}

// DefaultTransport returns a shared http.Transport configured with pure-Go TLS verification
// and sensible timeouts, reusing connections across calls.
func DefaultTransport() *http.Transport {
	defaultTransportOnce.Do(func() {
		defaultTransport = &http.Transport{
			TLSClientConfig:     SecureTLSConfig(""),
			MaxIdleConns:        50,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     30 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
		}
	})
	return defaultTransport
}

// DefaultHTTPClient returns an http.Client using the shared DefaultTransport.
func DefaultHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: DefaultTransport(),
	}
}

