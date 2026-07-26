package security

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
)

// PublicKeyHex is the Ed25519 public key for verifying NeoBox release signatures.
// This must match the private key used to sign releases.
// IMPORTANT: Replace this with the actual public key before production release.
// Generate a keypair with: `openssl genpkey -algorithm Ed25519` or Go's crypto/ed25519.
const PublicKeyHex = "46052d855841dbec01d7a771a4f724ce113f6e490bfa3de084a54a7c72488208"

// ValidateSignatureHex checks that sigHex is a well-formed Ed25519 signature
// before it is used. Callers verify this up front so a malformed or absent
// signature is rejected before a multi-megabyte installer is downloaded.
func ValidateSignatureHex(sigHex string) error {
	if sigHex == "" {
		return fmt.Errorf("release signature is missing")
	}
	if len(sigHex) != ed25519.SignatureSize*2 {
		return fmt.Errorf("invalid signature length: got %d hex characters, expected %d",
			len(sigHex), ed25519.SignatureSize*2)
	}
	if _, err := hex.DecodeString(sigHex); err != nil {
		return fmt.Errorf("signature is not valid hex: %w", err)
	}
	return nil
}

// VerifyFileSignature verifies an Ed25519 signature over the SHA-256 hash of a
// file. The signature is expected as a hex-encoded string.
//
// This is what stops a tampered installer from running even when the release
// itself, or the API response describing it, is attacker-controlled.
func VerifyFileSignature(filePath string, signatureHex string) error {
	if PublicKeyHex == "" || PublicKeyHex == "REPLACE_WITH_ACTUAL_PUBLIC_KEY_HEX" {
		return fmt.Errorf("release signature verification is not configured — contact the developer")
	}

	// Decode the public key
	pubKeyBytes, err := hex.DecodeString(PublicKeyHex)
	if err != nil {
		return fmt.Errorf("invalid public key format: %w", err)
	}
	if len(pubKeyBytes) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid public key size: got %d bytes, expected %d", len(pubKeyBytes), ed25519.PublicKeySize)
	}
	pubKey := ed25519.PublicKey(pubKeyBytes)

	// Read the file
	fileData, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read file for verification: %w", err)
	}

	// Decode the signature
	sigBytes, err := hex.DecodeString(signatureHex)
	if err != nil {
		return fmt.Errorf("invalid signature format: %w", err)
	}
	if len(sigBytes) != ed25519.SignatureSize {
		return fmt.Errorf("invalid signature size: got %d bytes, expected %d", len(sigBytes), ed25519.SignatureSize)
	}

	// Compute SHA-256 hash of the file
	hash := sha256.Sum256(fileData)

	// Verify the signature
	if !ed25519.Verify(pubKey, hash[:], sigBytes) {
		return fmt.Errorf("signature verification failed — the file may have been tampered with")
	}

	return nil
}

// GenerateSigningKeys generates a new Ed25519 keypair for signing releases.
// This is a utility function — call it once during release infrastructure setup,
// then embed the public key in PublicKeyHex and keep the private key secure.
func GenerateSigningKeys() (publicKeyHex string, privateKeyHex string, err error) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate Ed25519 keypair: %w", err)
	}
	return hex.EncodeToString(pub), hex.EncodeToString(priv), nil
}
