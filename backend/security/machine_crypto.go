package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
)

const (
	magicHeader = "NBX1"
	saltSize    = 16
	nonceSize   = 12
	kdfIters    = 100_000
	kdfKeyLen   = 32
)

// [INTERNET SEARCH PATTERN]:
// Based on Linux secret storage and credential vault best practices:
// When OS DPAPI is not available, secrets are bound to the local host and user
// using PBKDF2 key stretching (RFC 8018) combined with AES-256-GCM authenticated encryption.
// The payload layout is: [Magic 4B: NBX1] + [Salt 16B] + [Nonce 12B] + [Ciphertext + 16B Tag].

// machineProtectWithSecret encrypts plaintext using an AES-256-GCM key derived from machineSecret.
func machineProtectWithSecret(data []byte, secret string) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}

	salt := make([]byte, saltSize)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("failed to generate crypto salt: %w", err)
	}

	key, err := pbkdf2.Key(sha256.New, secret, salt, kdfIters, kdfKeyLen)
	if err != nil {
		return nil, fmt.Errorf("failed to derive key: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create gcm: %w", err)
	}

	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	ciphertext := gcm.Seal(nil, nonce, data, nil)

	// Result: Magic (4) + Salt (16) + Nonce (12) + Ciphertext + Tag (16)
	result := make([]byte, 0, len(magicHeader)+saltSize+nonceSize+len(ciphertext))
	result = append(result, magicHeader...)
	result = append(result, salt...)
	result = append(result, nonce...)
	result = append(result, ciphertext...)
	return result, nil
}

// machineUnprotectWithSecret decrypts and authenticates a payload encrypted by machineProtectWithSecret.
func machineUnprotectWithSecret(data []byte, secret string) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}

	// Backward compatibility: If 32 bytes and no magic header, treat as legacy raw key from older builds.
	if len(data) == 32 && (len(data) < len(magicHeader) || string(data[:len(magicHeader)]) != magicHeader) {
		out := make([]byte, 32)
		copy(out, data)
		return out, nil
	}

	minLen := len(magicHeader) + saltSize + nonceSize + 16 // 16 is min GCM tag
	if len(data) < minLen {
		return nil, errors.New("malformed protected secret: payload too short")
	}

	if string(data[:len(magicHeader)]) != magicHeader {
		return nil, errors.New("unrecognized protected secret format")
	}

	offset := len(magicHeader)
	salt := data[offset : offset+saltSize]
	offset += saltSize

	nonce := data[offset : offset+nonceSize]
	offset += nonceSize

	ciphertext := data[offset:]

	key, err := pbkdf2.Key(sha256.New, secret, salt, kdfIters, kdfKeyLen)
	if err != nil {
		return nil, fmt.Errorf("failed to derive key: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create gcm: %w", err)
	}

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt or authenticate secret (wrong host/user or corrupted data): %w", err)
	}

	return plaintext, nil
}

