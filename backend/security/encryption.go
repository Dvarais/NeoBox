package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	keyMu       sync.RWMutex
	keyFilePath string
	cachedKey   []byte
)

// InitEncryption must be called once at startup with the user data directory.
// It generates a persistent AES-256 key on first run and caches it for future calls.
//
// The key is sealed with Windows DPAPI rather than written out in the clear, so
// only the same user on the same machine can unseal it. That is what makes it
// useless to a stealer that simply copies files out of AppData.
func InitEncryption(dataDir string) error {
	keyMu.Lock()
	if keyFilePath == "" {
		keyFilePath = filepath.Join(dataDir, "key.bin")
	}
	keyMu.Unlock()
	_, err := loadOrCreateKey()
	return err
}

// loadOrCreateKey returns the cached key, loading from disk or generating it on first call.
func loadOrCreateKey() ([]byte, error) {
	keyMu.RLock()
	if len(cachedKey) == 32 {
		k := cachedKey
		keyMu.RUnlock()
		return k, nil
	}
	keyMu.RUnlock()

	keyMu.Lock()
	defer keyMu.Unlock()

	// Double-check after acquiring write lock
	if len(cachedKey) == 32 {
		return cachedKey, nil
	}

	if keyFilePath == "" {
		return nil, fmt.Errorf("encryption not initialized: call InitEncryption first")
	}

	// Try to load and decrypt the DPAPI-protected key
	protectedData, err := os.ReadFile(keyFilePath)
	if err == nil && len(protectedData) > 0 {
		decrypted, decErr := dpapiUnprotect(protectedData)
		if decErr == nil && len(decrypted) == 32 {
			cachedKey = decrypted
			return cachedKey, nil
		}
		// The key could not be unsealed. A new one is generated below, and
		// everything sealed with the old one becomes unreadable — subscriptions,
		// the saved server, favourites, profiles, history. So the old file is
		// moved aside first instead of being written over.
		//
		// DPAPI failing is not proof the key is gone: it fails when the user
		// profile is not fully loaded, after some credential changes, and when
		// AppData was copied from another machine or account. All of those are
		// recoverable while key.bin still exists, and none of them are once it
		// has been overwritten. It is the same treatment storage.quarantine
		// already gives the data files — which bought nothing as long as the one
		// file they all depend on was the one being destroyed.
		preserveUnreadableKey(keyFilePath)
		fmt.Printf("[encryption] warning: failed to decrypt existing key (%v), generating a new one\n", decErr)
	}

	// Generate a new random 32-byte AES-256 key
	newKey := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, newKey); err != nil {
		return nil, fmt.Errorf("failed to generate encryption key: %w", err)
	}

	// Protect the key using Windows DPAPI before saving
	protected, protectErr := dpapiProtect(newKey)
	if protectErr != nil {
		return nil, fmt.Errorf("failed to protect encryption key with DPAPI: %w", protectErr)
	}

	if err := os.WriteFile(keyFilePath, protected, 0600); err != nil {
		return nil, fmt.Errorf("failed to save encrypted key: %w", err)
	}

	// Best-effort: apply Windows ACL as defense-in-depth
	if err := ProtectFile(keyFilePath); err != nil {
		fmt.Printf("[encryption] warning: failed to protect key file with ACL: %v\n", err)
	}

	cachedKey = newKey

	// Lock the key in memory to prevent it from being swapped to disk
	if lockErr := LockMemory(cachedKey); lockErr != nil {
		fmt.Printf("[encryption] warning: failed to lock key in memory: %v\n", lockErr)
	}

	return cachedKey, nil
}

// preserveUnreadableKey renames a key file that could not be unsealed, so the
// bytes survive for a later attempt. Failures are reported, not returned: the
// caller is already recovering from a read failure.
func preserveUnreadableKey(path string) {
	dest := fmt.Sprintf("%s.unreadable-%s", path, time.Now().Format("20060102-150405"))
	if err := os.Rename(path, dest); err != nil {
		fmt.Printf("[encryption] warning: could not preserve the unreadable key: %v\n", err)
		return
	}
	fmt.Printf("[encryption] the previous key could not be decrypted; original preserved as %s\n", filepath.Base(dest))
}

// Encrypt encrypts data using AES-256-GCM with a random nonce.
// Output format: [12-byte nonce][ciphertext+tag]
func Encrypt(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("data to encrypt is empty")
	}

	key, err := loadOrCreateKey()
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Seal appends ciphertext to nonce: result = nonce || ciphertext
	ciphertext := gcm.Seal(nonce, nonce, data, nil)
	return ciphertext, nil
}

// Decrypt decrypts AES-256-GCM data previously encrypted by Encrypt.
// Expects format: [12-byte nonce][ciphertext+tag]
func Decrypt(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("encrypted data is empty")
	}

	key, err := loadOrCreateKey()
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short: expected at least %d bytes", nonceSize)
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decryption failed (wrong key or corrupted data): %w", err)
	}

	return plaintext, nil
}


// SecureWipe overwrites the cached key in memory with random data and unlocks it.
// Call this during application shutdown to prevent key extraction from memory dumps.
func SecureWipe() {
	keyMu.Lock()
	defer keyMu.Unlock()
	if len(cachedKey) > 0 {
		// Unlock memory before wiping (allows paging again)
		_ = UnlockMemory(cachedKey)

		// Overwrite with random data multiple times
		for i := 0; i < 3; i++ {
			_, _ = rand.Read(cachedKey)
		}
		// Final zero fill
		for i := range cachedKey {
			cachedKey[i] = 0
		}
		cachedKey = nil
	}
}
