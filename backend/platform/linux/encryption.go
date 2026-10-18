package linux

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// SecurityManager manages encryption on Linux using AES-256-GCM with a per-user
// key stored in userDataDir with restricted (0600) permissions.
type SecurityManager struct {
	mu        sync.RWMutex
	cachedKey []byte
}

func (s *SecurityManager) Init(userDataDir string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(userDataDir, 0700); err != nil {
		return fmt.Errorf("failed to create secure data directory: %w", err)
	}
	keyPath := filepath.Join(userDataDir, "key.dat")

	rawKey, err := os.ReadFile(keyPath)
	if err == nil && len(rawKey) >= 32 {
		_ = os.Chmod(keyPath, 0600)
		s.cachedKey = rawKey[:32]
		return nil
	}

	newKey := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, newKey); err != nil {
		return fmt.Errorf("failed to generate encryption key: %w", err)
	}

	if err := os.WriteFile(keyPath, newKey, 0600); err != nil {
		return fmt.Errorf("failed to save encryption key: %w", err)
	}
	_ = os.Chmod(keyPath, 0600)
	s.cachedKey = newKey
	return nil
}

func (s *SecurityManager) Encrypt(plain []byte) ([]byte, error) {
	s.mu.RLock()
	key := s.cachedKey
	s.mu.RUnlock()

	if len(key) != 32 {
		return nil, errors.New("security manager not initialized")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	sealed := gcm.Seal(nil, nonce, plain, nil)
	return append(nonce, sealed...), nil
}

func (s *SecurityManager) Decrypt(cipherText []byte) ([]byte, error) {
	s.mu.RLock()
	key := s.cachedKey
	s.mu.RUnlock()

	if len(key) != 32 {
		return nil, errors.New("security manager not initialized")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonceSize := gcm.NonceSize()
	if len(cipherText) < nonceSize {
		return nil, errors.New("ciphertext too short")
	}

	nonce := cipherText[:nonceSize]
	encData := cipherText[nonceSize:]
	return gcm.Open(nil, nonce, encData, nil)
}
