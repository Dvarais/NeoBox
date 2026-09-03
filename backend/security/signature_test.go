package security

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateSignatureHex(t *testing.T) {
	valid := strings.Repeat("ab", ed25519.SignatureSize) // 128 hex chars

	tests := []struct {
		name    string
		sig     string
		wantErr bool
	}{
		{"well formed", valid, false},
		{"empty is rejected", "", true},
		{"too short", strings.Repeat("ab", 10), true},
		{"too long", valid + "ab", true},
		{"not hex", strings.Repeat("zz", ed25519.SignatureSize), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSignatureHex(tc.sig)
			if tc.wantErr && err == nil {
				t.Errorf("ValidateSignatureHex accepted an invalid signature (%d chars)", len(tc.sig))
			}
			if !tc.wantErr && err != nil {
				t.Errorf("ValidateSignatureHex rejected a well-formed signature: %v", err)
			}
		})
	}
}

// A signature made with the wrong key must not verify against the release key
// embedded in PublicKeyHex.
func TestVerifyFileSignatureRejectsForeignKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "installer.exe")
	payload := []byte("pretend this is an installer")
	if err := os.WriteFile(path, payload, 0600); err != nil {
		t.Fatalf("failed to write the test file: %v", err)
	}

	// Sign with a freshly generated key — i.e. not the NeoBox release key.
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("failed to generate a key: %v", err)
	}
	hash := sha256.Sum256(payload)
	foreignSig := hex.EncodeToString(ed25519.Sign(priv, hash[:]))

	if err := VerifyFileSignature(path, foreignSig); err == nil {
		t.Error("a signature from an unrelated key was accepted")
	}
}

func TestVerifyFileSignatureRejectsMalformedInput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "installer.exe")
	if err := os.WriteFile(path, []byte("data"), 0600); err != nil {
		t.Fatalf("failed to write the test file: %v", err)
	}

	if err := VerifyFileSignature(path, "not-hex"); err == nil {
		t.Error("a non-hex signature was accepted")
	}
	if err := VerifyFileSignature(path, ""); err == nil {
		t.Error("an empty signature was accepted")
	}
	if err := VerifyFileSignature(filepath.Join(dir, "missing.exe"),
		strings.Repeat("ab", ed25519.SignatureSize)); err == nil {
		t.Error("a missing file was accepted")
	}
}

// Ключ, который не расшифровался, нельзя просто перезаписать: всё, что им
// запечатано — подписки, выбранный сервер, избранное, профили, история, — после
// этого не читается уже никогда. А DPAPI отказывает и по причинам, которые
// проходят: профиль не догрузился, AppData принесли с другой машины. Файл
// обязан пережить неудачу.
func TestPreserveUnreadableKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "key.bin")
	if err := os.WriteFile(path, []byte("not a DPAPI blob"), 0600); err != nil {
		t.Fatalf("подготовка файла: %v", err)
	}

	preserveUnreadableKey(path)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("исходный key.bin остался на месте — следующая запись затрёт его")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("чтение каталога: %v", err)
	}
	var saved string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "key.bin.unreadable-") {
			saved = e.Name()
		}
	}
	if saved == "" {
		t.Fatalf("копия не найдена, в каталоге: %v", entries)
	}
	data, err := os.ReadFile(filepath.Join(dir, saved))
	if err != nil || string(data) != "not a DPAPI blob" {
		t.Errorf("содержимое копии повреждено: %q, %v", data, err)
	}
}
