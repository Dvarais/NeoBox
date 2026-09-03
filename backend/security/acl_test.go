package security

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestProtectFileWritesProtectedDACL checks the ACL that ProtectFile leaves
// behind, by reading it back rather than by trusting the call to have worked.
//
// icacls is used here as an independent verifier — it is what the implementation
// used to shell out to, and reading with it proves the native path produces the
// same result the shell-out did.
func TestProtectFileWritesProtectedDACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.bin")
	if err := os.WriteFile(path, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ProtectFile(path); err != nil {
		t.Fatalf("ProtectFile: %v", err)
	}

	out := icacls(t, path)

	sid, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	account, _, _, err := sid.LookupAccount("")
	if err != nil {
		t.Fatalf("LookupAccount: %v", err)
	}

	// The current user must be granted full control.
	if !strings.Contains(out, account) {
		t.Errorf("current user %q is not in the ACL:\n%s", account, out)
	}
	if !strings.Contains(out, "(F)") {
		t.Errorf("no full-control ACE in the ACL:\n%s", out)
	}

	// And nobody else may appear. One granted ACE is the whole point: an
	// inherited grant surviving here is exactly the failure a protected DACL
	// exists to prevent.
	if n := countACEs(out); n != 1 {
		t.Errorf("expected exactly 1 ACE, got %d:\n%s", n, out)
	}
}

// TestProtectFileIsNotASubprocess guards the property that made this code worth
// rewriting: it must not spawn a program. Shelling out cost 36 ms a call on a
// path that runs on every atomic write of an encrypted file.
//
// The threshold is deliberately far below what CreateProcess costs and far
// above what the Win32 calls do, so it fails on a regression to icacls without
// being sensitive to a loaded machine.
func TestProtectFileIsNotASubprocess(t *testing.T) {
	dir := t.TempDir()
	paths := make([]string, 20)
	for i := range paths {
		paths[i] = filepath.Join(dir, string(rune('a'+i))+".bin")
		if err := os.WriteFile(paths[i], []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}

	// Warm the one-time SID lookup so it is not charged to the first iteration.
	if _, err := currentUserSID(); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	for _, p := range paths {
		if err := ProtectFile(p); err != nil {
			t.Fatalf("ProtectFile(%s): %v", p, err)
		}
	}
	per := time.Since(start) / time.Duration(len(paths))
	t.Logf("ProtectFile: %v per call", per)

	if per > 5*time.Millisecond {
		t.Errorf("ProtectFile took %v per call — that is subprocess territory, "+
			"not a Win32 call", per)
	}
}

// TestProtectFileOnDirectory covers the other caller: updates.go protects the
// directory it downloads an installer into.
func TestProtectFileOnDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "downloads")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := ProtectFile(dir); err != nil {
		t.Fatalf("ProtectFile on a directory: %v", err)
	}
	if n := countACEs(icacls(t, dir)); n != 1 {
		t.Errorf("expected exactly 1 ACE on the directory, got %d", n)
	}
}

func TestProtectFileReportsMissingPath(t *testing.T) {
	err := ProtectFile(filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Fatal("expected an error for a path that does not exist")
	}
}

// icacls dumps the ACL of path. Verification only — never on a hot path.
func icacls(t *testing.T, path string) string {
	t.Helper()
	cmd := exec.Command("icacls", path)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("icacls %s failed: %v\n%s", path, err, out)
	}
	return string(out)
}

// countACEs counts the granted entries in icacls output.
//
// Each ACE sits on its own line as "<account>:<rights>", so they are found by
// the ":(" that starts the rights group. Deliberately not by matching icacls'
// prose: its summary line is localised — on a Russian Windows it reads
// "Успешно обработано 1 файлов" — and an assertion against the English wording
// fails on the very machine this is meant to protect.
func countACEs(out string) int {
	n := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(strings.TrimSpace(line), ":(") {
			n++
		}
	}
	return n
}
