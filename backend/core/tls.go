package core

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"sync"
	"time"
)

var (
	systemRootsOnce sync.Once
	systemRoots     *x509.CertPool
)

// SystemCertPool returns the cached system root certificate pool.
// On Windows this pool alone does not keep verification out of crypt32.dll — it
// is a marker that routes Verify into CryptoAPI. What does is roots_windows.go
// together with x509usefallbackroots=1 in main.go, which make this return the
// pure-Go pool built there.
func SystemCertPool() *x509.CertPool {
	systemRootsOnce.Do(func() {
		pool, err := x509.SystemCertPool()
		if err == nil && pool != nil {
			systemRoots = pool
		}
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

// DefaultTransport returns an http.Transport configured with pure-Go TLS verification
// and sensible timeouts.
func DefaultTransport() *http.Transport {
	return &http.Transport{
		TLSClientConfig:     SecureTLSConfig(""),
		MaxIdleConns:        10,
		IdleConnTimeout:     30 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
}

// DefaultHTTPClient returns an http.Client using DefaultTransport.
func DefaultHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: DefaultTransport(),
	}
}
