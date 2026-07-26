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
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	keyMu       sync.RWMutex
	keyFilePath string
	cachedKey   []byte
)

// dpapiProtect encrypts data using Windows DPAPI (CryptProtectData).
// Only the same user account on the same machine can decrypt it.
func dpapiProtect(data []byte) ([]byte, error) {
	var dataIn windows.DataBlob
	dataIn.Size = uint32(len(data))
	dataIn.Data = &data[0]

	var dataOut windows.DataBlob
	// The freed pointer must be read when the deferred call RUNS, not when it is
	// declared: arguments to a plain `defer f(x)` are evaluated immediately, so
	// the previous form always passed the still-nil dataOut.Data and leaked the
	// buffer CryptProtectData allocates.
	defer func() {
		if dataOut.Data != nil {
			_, _ = windows.LocalFree(windows.Handle(unsafe.Pointer(dataOut.Data)))
		}
	}()

	err := windows.CryptProtectData(&dataIn, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &dataOut)
	if err != nil {
		return nil, fmt.Errorf("CryptProtectData failed: %w", err)
	}

	protected := make([]byte, dataOut.Size)
	copy(protected, unsafe.Slice(dataOut.Data, dataOut.Size))
	return protected, nil
}

// dpapiUnprotect decrypts data using Windows DPAPI (CryptUnprotectData).
func dpapiUnprotect(data []byte) ([]byte, error) {
	var dataIn windows.DataBlob
	dataIn.Size = uint32(len(data))
	dataIn.Data = &data[0]

	var dataOut windows.DataBlob
	// See dpapiProtect: the pointer must be read at call time, not at declaration.
	defer func() {
		if dataOut.Data != nil {
			_, _ = windows.LocalFree(windows.Handle(unsafe.Pointer(dataOut.Data)))
		}
	}()

	err := windows.CryptUnprotectData(&dataIn, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &dataOut)
	if err != nil {
		return nil, fmt.Errorf("CryptUnprotectData failed: %w", err)
	}

	unprotected := make([]byte, dataOut.Size)
	copy(unprotected, unsafe.Slice(dataOut.Data, dataOut.Size))
	return unprotected, nil
}

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
		// If decryption fails, fall through to generate new key
		fmt.Printf("[encryption] warning: failed to decrypt existing key (%v), generating new one\n", decErr)
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

// LockMemory pins the encryption key in physical memory so Windows cannot page
// it out to pagefile.sys, where it would survive on disk and be recoverable
// long after the process exited.
func LockMemory(data []byte) error {
	if len(data) == 0 {
		return nil
	}

	addr := uintptr(unsafe.Pointer(&data[0]))
	size := uintptr(len(data))

	if err := windows.VirtualLock(addr, size); err != nil {
		return fmt.Errorf("VirtualLock failed: %w", err)
	}
	return nil
}

// UnlockMemory releases a region pinned by LockMemory. It is called before the
// key is wiped, so the pages can be reclaimed normally afterwards.
func UnlockMemory(data []byte) error {
	if len(data) == 0 {
		return nil
	}

	addr := uintptr(unsafe.Pointer(&data[0]))
	size := uintptr(len(data))

	if err := windows.VirtualUnlock(addr, size); err != nil {
		return fmt.Errorf("VirtualUnlock failed: %w", err)
	}
	return nil
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
