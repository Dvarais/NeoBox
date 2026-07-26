package security

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateSignatureHex(t *testing.T) {
	valid := strings.Repeat("ab", ed25519.SignatureSize) // 128 hex chars

	tests := []struct {
		name    string
		sig     string
		wantErr bool
	}{
		{"well formed", valid, false},
		{"empty is rejected", "", true},
		{"too short", strings.Repeat("ab", 10), true},
		{"too long", valid + "ab", true},
		{"not hex", strings.Repeat("zz", ed25519.SignatureSize), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSignatureHex(tc.sig)
			if tc.wantErr && err == nil {
				t.Errorf("ValidateSignatureHex accepted an invalid signature (%d chars)", len(tc.sig))
			}
			if !tc.wantErr && err != nil {
				t.Errorf("ValidateSignatureHex rejected a well-formed signature: %v", err)
			}
		})
	}
}

// A signature made with the wrong key must not verify against the release key
// embedded in PublicKeyHex.
func TestVerifyFileSignatureRejectsForeignKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "installer.exe")
	payload := []byte("pretend this is an installer")
	if err := os.WriteFile(path, payload, 0600); err != nil {
		t.Fatalf("failed to write the test file: %v", err)
	}

	// Sign with a freshly generated key — i.e. not the NeoBox release key.
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("failed to generate a key: %v", err)
	}
	hash := sha256.Sum256(payload)
	foreignSig := hex.EncodeToString(ed25519.Sign(priv, hash[:]))

	if err := VerifyFileSignature(path, foreignSig); err == nil {
		t.Error("a signature from an unrelated key was accepted")
	}
}

func TestVerifyFileSignatureRejectsMalformedInput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "installer.exe")
	if err := os.WriteFile(path, []byte("data"), 0600); err != nil {
		t.Fatalf("failed to write the test file: %v", err)
	}

	if err := VerifyFileSignature(path, "not-hex"); err == nil {
		t.Error("a non-hex signature was accepted")
	}
	if err := VerifyFileSignature(path, ""); err == nil {
		t.Error("an empty signature was accepted")
	}
	if err := VerifyFileSignature(filepath.Join(dir, "missing.exe"),
		strings.Repeat("ab", ed25519.SignatureSize)); err == nil {
		t.Error("a missing file was accepted")
	}
}
