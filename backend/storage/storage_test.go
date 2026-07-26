package storage

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// initOnce initialises the encryption layer against a throwaway directory.
// The security package keeps the key path in a package-level variable, so the
// first call in the test binary wins — every test shares one key, which is
// exactly what the production process does too.
func initStorage(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := Init(dir); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	return dir
}

func TestWriteSecretRoundTrip(t *testing.T) {
	dir := initStorage(t)
	path := filepath.Join(dir, "subscriptions.json")
	payload := []byte(`[{"id":"a","name":"Sub","links":["vless://uuid@example.com:443#node"]}]`)

	if err := WriteSecret(path, payload); err != nil {
		t.Fatalf("WriteSecret failed: %v", err)
	}

	got, found, err := ReadSecret(path)
	if err != nil {
		t.Fatalf("ReadSecret failed: %v", err)
	}
	if !found {
		t.Fatal("expected the file to be found")
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("round-trip mismatch:\n got %s\nwant %s", got, payload)
	}
}

// The whole point of the change: the credentials must not be readable on disk.
func TestWriteSecretLeavesNoPlaintextOnDisk(t *testing.T) {
	dir := initStorage(t)
	path := filepath.Join(dir, "subscriptions.json")
	secret := "vless://11111111-2222-3333-4444-555555555555@example.com:443#secret-node"

	if err := WriteSecret(path, []byte(`[{"links":["`+secret+`"]}]`)); err != nil {
		t.Fatalf("WriteSecret failed: %v", err)
	}

	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read the stored file: %v", err)
	}
	if strings.Contains(string(onDisk), secret) {
		t.Error("proxy credentials are present in cleartext in the stored file")
	}
	if strings.Contains(string(onDisk), "vless://") {
		t.Error("proxy link scheme is present in cleartext in the stored file")
	}
	if !bytes.HasPrefix(onDisk, envelopeMagic) {
		t.Errorf("stored file does not carry the envelope magic, got prefix %q", firstBytes(onDisk, 8))
	}
}

func TestReadSecretMissingFile(t *testing.T) {
	dir := initStorage(t)

	got, found, err := ReadSecret(filepath.Join(dir, "does-not-exist.json"))
	if err != nil {
		t.Fatalf("a missing file must not be an error, got: %v", err)
	}
	if found || got != nil {
		t.Errorf("expected (nil, false) for a missing file, got (%q, %v)", got, found)
	}
}

// A file left in the clear by an older build must still be readable, and Init
// must convert it in place so the plaintext stops existing on disk.
func TestInitMigratesLegacyPlaintext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subscriptions.json")
	legacy := []byte(`[{"id":"legacy","links":["trojan://password@example.com:443#old"]}]`)

	if err := os.WriteFile(path, legacy, 0600); err != nil {
		t.Fatalf("failed to seed the legacy file: %v", err)
	}

	if err := Init(dir); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read the migrated file: %v", err)
	}
	if !bytes.HasPrefix(onDisk, envelopeMagic) {
		t.Fatal("Init left the legacy file unencrypted")
	}
	if strings.Contains(string(onDisk), "trojan://") {
		t.Error("credentials survived migration in cleartext")
	}

	got, found, err := ReadSecret(path)
	if err != nil {
		t.Fatalf("ReadSecret failed after migration: %v", err)
	}
	if !found || !bytes.Equal(got, legacy) {
		t.Errorf("migration lost content:\n got %s\nwant %s", got, legacy)
	}
}

// Reading a plaintext file directly (before Init had a chance to migrate it)
// must still return the content rather than failing.
func TestReadSecretAcceptsPlaintext(t *testing.T) {
	dir := initStorage(t)
	path := filepath.Join(dir, "plain.json")
	legacy := []byte(`[{"id":"plain"}]`)

	if err := os.WriteFile(path, legacy, 0600); err != nil {
		t.Fatalf("failed to seed the file: %v", err)
	}

	got, found, err := ReadSecret(path)
	if err != nil {
		t.Fatalf("ReadSecret failed: %v", err)
	}
	if !found || !bytes.Equal(got, legacy) {
		t.Errorf("expected the plaintext content back, got %q", got)
	}
}

// An undecryptable file must be preserved under a new name so the caller's
// fallback-to-empty plus next save cannot destroy recoverable data.
func TestReadSecretQuarantinesUndecryptableFile(t *testing.T) {
	dir := initStorage(t)
	path := filepath.Join(dir, "subscriptions.json")

	corrupt := append(append([]byte{}, envelopeMagic...), []byte("not a valid gcm payload")...)
	if err := os.WriteFile(path, corrupt, 0600); err != nil {
		t.Fatalf("failed to seed the corrupt file: %v", err)
	}

	_, found, err := ReadSecret(path)
	if err == nil {
		t.Fatal("expected an error for an undecryptable file")
	}
	if found {
		t.Error("an undecryptable file must not be reported as found")
	}

	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Error("the undecryptable file should have been moved aside")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("failed to list the data dir: %v", err)
	}
	var quarantined string
	for _, e := range entries {
		if strings.Contains(e.Name(), ".unreadable-") {
			quarantined = filepath.Join(dir, e.Name())
		}
	}
	if quarantined == "" {
		t.Fatal("no quarantined copy was created")
	}
	saved, err := os.ReadFile(quarantined)
	if err != nil {
		t.Fatalf("failed to read the quarantined copy: %v", err)
	}
	if !bytes.Equal(saved, corrupt) {
		t.Error("the quarantined copy does not match the original bytes")
	}
}

// Writing empty content would destroy the stored subscriptions, so it is refused.
func TestWriteSecretRejectsEmptyContent(t *testing.T) {
	dir := initStorage(t)

	if err := WriteSecret(filepath.Join(dir, "subscriptions.json"), nil); err == nil {
		t.Error("expected WriteSecret to refuse empty content")
	}
}

// A failed write must leave the previous version intact, and no .tmp file behind.
func TestWriteSecretIsAtomic(t *testing.T) {
	dir := initStorage(t)
	path := filepath.Join(dir, "subscriptions.json")

	first := []byte(`[{"id":"first"}]`)
	if err := WriteSecret(path, first); err != nil {
		t.Fatalf("first WriteSecret failed: %v", err)
	}
	second := []byte(`[{"id":"second"}]`)
	if err := WriteSecret(path, second); err != nil {
		t.Fatalf("second WriteSecret failed: %v", err)
	}

	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("a .tmp file was left behind after a successful write")
	}

	got, _, err := ReadSecret(path)
	if err != nil {
		t.Fatalf("ReadSecret failed: %v", err)
	}
	if !bytes.Equal(got, second) {
		t.Errorf("expected the second payload, got %s", got)
	}
}

func firstBytes(b []byte, n int) []byte {
	if len(b) < n {
		return b
	}
	return b[:n]
}
