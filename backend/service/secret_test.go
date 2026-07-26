package service

import (
	"encoding/hex"
	"testing"
)

func TestGenerateClashSecret(t *testing.T) {
	secret, err := generateClashSecret()
	if err != nil {
		t.Fatalf("generateClashSecret failed: %v", err)
	}

	// 16 random bytes, hex-encoded.
	if len(secret) != 32 {
		t.Errorf("secret length = %d, want 32 hex characters", len(secret))
	}
	if _, err := hex.DecodeString(secret); err != nil {
		t.Errorf("secret is not valid hex: %v", err)
	}
}

// The secret is per-session: reusing one across connections would let a process
// that observed an earlier session keep talking to the Clash API.
func TestGenerateClashSecretIsUniquePerCall(t *testing.T) {
	seen := make(map[string]bool, 64)
	for i := 0; i < 64; i++ {
		secret, err := generateClashSecret()
		if err != nil {
			t.Fatalf("generateClashSecret failed on call %d: %v", i, err)
		}
		if seen[secret] {
			t.Fatalf("generateClashSecret returned a repeated value: %s", secret)
		}
		seen[secret] = true
	}
}

// There must be no constant fallback. An earlier version returned a hardcoded
// string when crypto/rand failed, which was recoverable straight out of the
// binary — a predictable secret guards nothing.
func TestGenerateClashSecretHasNoConstantFallback(t *testing.T) {
	first, err := generateClashSecret()
	if err != nil {
		t.Fatalf("generateClashSecret failed: %v", err)
	}
	second, err := generateClashSecret()
	if err != nil {
		t.Fatalf("generateClashSecret failed: %v", err)
	}
	if first == second {
		t.Error("two calls returned the same secret, which means the value is not random")
	}
}
