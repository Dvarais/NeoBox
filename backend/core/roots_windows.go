//go:build windows

package core

import (
	"bytes"
	"crypto/x509"
	"unsafe"

	"golang.org/x/crypto/x509roots/fallback/bundle"
	"golang.org/x/sys/windows"
)

// Certificate verification in pure Go instead of crypt32.
//
// With the system pool, crypto/x509 verifies every chain through
// CertGetCertificateChain, and that call has crashed NeoBox outright: an access
// violation inside CertFreeCertificateChain, which Go cannot recover from. The
// crash log from 2026-09-08 has the trace. Handing RootCAs a pool from
// x509.SystemCertPool() does not avoid it — on Windows that pool is a marker
// which still routes Verify into crypt32.
//
// So the roots are loaded once, here, and installed as fallback roots. main.go
// sets x509usefallbackroots=1, which makes them replace the system pool for the
// whole process: our own HTTP clients and the sing-box core alike.
//
// Two sources, because neither is complete on its own:
//   - the Windows ROOT store keeps roots the user or an antivirus with HTTPS
//     scanning installed, which Mozilla's list does not have;
//   - Mozilla's bundle covers public CAs that Windows has not downloaded yet —
//     Windows fetches missing roots on demand, and only crypt32 knows how.
//
// ponytail: Windows roots are trusted for every purpose, ignoring per-root EKU
// restrictions set in the store; filter on CERT_ENHKEY_USAGE_PROP_ID if that matters.
func init() {
	pool := x509.NewCertPool()
	for root := range bundle.Roots() {
		cert, err := x509.ParseCertificate(root.Certificate)
		if err != nil {
			continue
		}
		if root.Constraint == nil {
			pool.AddCert(cert)
		} else {
			pool.AddCertWithConstraint(cert, root.Constraint)
		}
	}
	addWindowsRootStore(pool)
	x509.SetFallbackRoots(pool)
}

// addWindowsRootStore copies the current user's ROOT store into pool. The
// user's view of ROOT includes the machine-wide and group-policy roots too.
func addWindowsRootStore(pool *x509.CertPool) {
	store, err := windows.CertOpenSystemStore(0, windows.StringToUTF16Ptr("ROOT"))
	if err != nil {
		return
	}
	defer windows.CertCloseStore(store, 0)

	var ctx *windows.CertContext
	for {
		// Passing the previous context frees it, and the last call frees the
		// final one — so the DER bytes must be copied before the next call.
		ctx, err = windows.CertEnumCertificatesInStore(store, ctx)
		if err != nil || ctx == nil {
			return
		}
		der := bytes.Clone(unsafe.Slice(ctx.EncodedCert, ctx.Length))
		if cert, err := x509.ParseCertificate(der); err == nil {
			pool.AddCert(cert)
		}
	}
}
