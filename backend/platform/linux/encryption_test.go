package linux_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"NeoBox/backend/platform/linux"
)

func TestLinuxEncryptionRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	sec := &linux.SecurityManager{}
	if err := sec.Init(tmpDir); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	keyFile := filepath.Join(tmpDir, "key.dat")
	info, err := os.Stat(keyFile)
	if err != nil {
		t.Fatalf("key file was not created: %v", err)
	}
	if info.Size() != 32 {
		t.Fatalf("expected 32-byte key file, got size %d", info.Size())
	}

	plainText := []byte("vless://test-uuid@example.com:443?security=reality")
	cipherText, err := sec.Encrypt(plainText)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}
	if bytes.Equal(cipherText, plainText) {
		t.Fatalf("cipher text must not equal plain text")
	}

	decrypted, err := sec.Decrypt(cipherText)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}
	if !bytes.Equal(decrypted, plainText) {
		t.Fatalf("expected decrypted %s, got %s", plainText, decrypted)
	}
}

func TestLinuxPathsCreation(t *testing.T) {
	paths := &linux.PathManager{}
	userData := paths.UserDataDir()
	if userData == "" {
		t.Fatalf("UserDataDir must not be empty")
	}
	resolved := paths.ResolvePath("test.json")
	if filepath.Base(resolved) != "test.json" {
		t.Fatalf("unexpected resolved path: %s", resolved)
	}
}
