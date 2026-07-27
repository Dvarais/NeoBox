// Package storage persists NeoBox user data, transparently encrypting the files
// that hold credentials.
//
// Only files listed in secretFiles are encrypted. settings.json is deliberately
// left as plain JSON: it carries no secrets (routing rules, DNS, toggles) and
// users are expected to be able to open it in a text editor. The settings fields
// that DO carry credentials live in state.json, which is written through
// WriteSecret — see backend/service/settings.go.
package storage

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"NeoBox/backend/security"
)

// envelopeMagic prefixes every encrypted file NeoBox writes. It tells an
// encrypted payload apart from a legacy plaintext file without guessing at the
// content: files written by builds before encryption was wired up start with
// '[' or '{', never with this marker, so they are detected and migrated on the
// first read instead of being fed to the AES-GCM decryptor.
var envelopeMagic = []byte("NBOX\x01")

// secretFiles are the files inside the user data directory that contain
// credentials and must never be stored in the clear. subscriptions.json holds
// full proxy links — vless:// UUIDs, trojan:// and ss:// passwords — which is
// exactly what an infostealer sweeping AppData is looking for.
var secretFiles = []string{"subscriptions.json"}

// Init prepares the encrypted-storage layer and must be called once at startup,
// before any ReadSecret/WriteSecret call.
//
// It also migrates any secret file still stored in the clear by a previous
// build. Migration is eager rather than lazy on purpose: waiting for the user to
// happen to save would leave plaintext credentials on disk indefinitely.
func Init(dataDir string) error {
	if err := security.InitEncryption(dataDir); err != nil {
		return fmt.Errorf("failed to initialise encryption: %w", err)
	}

	for _, name := range secretFiles {
		path := filepath.Join(dataDir, name)
		if err := migrateToEncrypted(path); err != nil {
			// A failed migration leaves the plaintext file untouched and readable,
			// so the app still works — but the credentials stay exposed. Report it
			// loudly (stdout is redirected into the crash log) rather than aborting
			// startup; the next successful save encrypts the file anyway.
			fmt.Printf("[storage] warning: could not encrypt existing %s: %v\n", name, err)
		}
	}
	return nil
}

// ReadSecret returns the decrypted contents of path. A missing file yields
// (nil, false, nil) so callers can tell "absent" from "empty".
//
// If the file carries the envelope but cannot be decrypted — the encryption key
// was regenerated, or the file is corrupt — the unreadable bytes are moved aside
// to <path>.unreadable-<timestamp> before the error is returned. Callers fall
// back to an empty value and will overwrite path on the next save; quarantining
// first guarantees that overwrite cannot destroy data the user might otherwise
// have recovered.
func ReadSecret(path string) ([]byte, bool, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("failed to read %s: %w", filepath.Base(path), err)
	}
	if len(raw) == 0 {
		return nil, false, nil
	}

	if !bytes.HasPrefix(raw, envelopeMagic) {
		// Legacy plaintext file written before encryption was wired up. Return it
		// as-is; Init has already tried to migrate it, and any save re-writes it
		// encrypted.
		return raw, true, nil
	}

	plain, err := security.Decrypt(raw[len(envelopeMagic):])
	if err != nil {
		quarantine(path)
		return nil, false, fmt.Errorf("failed to decrypt %s: %w", filepath.Base(path), err)
	}
	return plain, true, nil
}

// WriteSecret encrypts plain and writes it to path atomically.
func WriteSecret(path string, plain []byte) error {
	if len(plain) == 0 {
		return fmt.Errorf("refusing to write empty content to %s", filepath.Base(path))
	}

	sealed, err := security.Encrypt(plain)
	if err != nil {
		return fmt.Errorf("failed to encrypt %s: %w", filepath.Base(path), err)
	}

	blob := make([]byte, 0, len(envelopeMagic)+len(sealed))
	blob = append(blob, envelopeMagic...)
	blob = append(blob, sealed...)

	return writeAtomic(path, blob, true)
}

// WriteFile writes data to path atomically, without encrypting it. Use it for
// state that carries no secrets but must survive a crash intact — a half-written
// file that fails to parse is indistinguishable from no file at all, and for
// recovery state that means the recovery silently never happens.
func WriteFile(path string, data []byte) error {
	return writeAtomic(path, data, false)
}

// migrateToEncrypted re-writes a plaintext secret file in encrypted form.
// It is a no-op when the file is absent, empty, or already encrypted.
func migrateToEncrypted(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil // absent — nothing to migrate
	}
	if len(raw) == 0 || bytes.HasPrefix(raw, envelopeMagic) {
		return nil
	}
	if err := WriteSecret(path, raw); err != nil {
		return err
	}
	fmt.Printf("[storage] migrated %s to encrypted storage\n", filepath.Base(path))
	return nil
}

// writeAtomic writes data to a temporary file in the same directory, flushes it
// to stable storage, and renames it over path. A crash or power loss can then
// only leave the old file or the new one — never a half-written blob, which for
// an encrypted file would be indistinguishable from corruption and would cost
// the user every stored subscription.
func writeAtomic(path string, data []byte, restrictACL bool) error {
	tmp := path + ".tmp"

	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("failed to flush temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	// Restrict the DACL before the rename, not after: os.Rename replaces the
	// destination with this file, so an ACL applied to the old path would be
	// discarded on the very next save.
	if restrictACL {
		if err := security.ProtectFile(tmp); err != nil {
			fmt.Printf("[storage] warning: failed to restrict ACL on %s: %v\n", filepath.Base(path), err)
		}
	}

	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("failed to replace %s: %w", filepath.Base(path), err)
	}
	return nil
}

// quarantine moves an undecryptable file aside so a later save cannot overwrite
// it. Failures are reported but not returned: the caller is already handling a
// read error, and a failed rename must not mask it.
func quarantine(path string) {
	dest := fmt.Sprintf("%s.unreadable-%s", path, time.Now().Format("20060102-150405"))
	if err := os.Rename(path, dest); err != nil {
		fmt.Printf("[storage] warning: could not quarantine %s: %v\n", filepath.Base(path), err)
		return
	}
	fmt.Printf("[storage] %s could not be decrypted; original preserved as %s\n",
		filepath.Base(path), filepath.Base(dest))
}
