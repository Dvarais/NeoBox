//go:debug x509usefallbackroots=1

package core

import (
	"crypto/x509"
	"testing"
)

// On Windows the real system pool is an empty marker, and a marker is what makes
// Certificate.Verify hand the chain to crypt32. A pool that holds certificates
// means the roots from roots_windows.go replaced it, so verification — including
// sing-box's own, which never sees our tls.Config — runs in pure Go.
func TestSystemRootsBypassCryptoAPI(t *testing.T) {
	pool, err := x509.SystemCertPool()
	if err != nil {
		t.Fatalf("SystemCertPool: %v", err)
	}
	//lint:ignore SA1019 Subjects is exactly the signal: empty only for the marker pool.
	if len(pool.Subjects()) == 0 {
		t.Fatal("system roots are still the crypt32 marker pool; TLS verification goes through CryptoAPI")
	}
}
