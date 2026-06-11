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
)

var (
	keyMu       sync.RWMutex
	keyFilePath string
	cachedKey   []byte
)

// InitEncryption must be called once at startup with the user data directory.
// It generates a persistent AES-256 key on first run and caches it for future calls.
// Using a file-based key instead of Windows DPAPI makes encryption session-independent:
// settings survive full shutdowns, privilege changes (admin vs user), and Windows updates.
//
// NOTE: Calling InitEncryption a second time with a different path will be ignored
// if the key is already successfully loaded/generated. The key must not change during a single process run.
// Ensure this is called exactly once at application startup before any Encrypt/Decrypt calls.
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

	data, err := os.ReadFile(keyFilePath)
	if err == nil && len(data) == 32 {
		cachedKey = data
		return cachedKey, nil
	}

	// Generate a new random 32-byte AES-256 key
	newKey := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, newKey); err != nil {
		return nil, fmt.Errorf("failed to generate encryption key: %w", err)
	}
	if err := os.WriteFile(keyFilePath, newKey, 0600); err != nil {
		return nil, fmt.Errorf("failed to save encryption key: %w", err)
	}
	// Best-effort: apply Windows ACL so only the current user can access the key.
	// os.WriteFile with mode 0600 is a no-op on Windows — icacls provides real protection.
	if err := ProtectFile(keyFilePath); err != nil {
		fmt.Printf("[encryption] warning: failed to protect key file with ACL: %v\n", err)
	}
	cachedKey = newKey
	return cachedKey, nil
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
