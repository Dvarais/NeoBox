//go:build windows

package windows

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetRunningProcesses(t *testing.T) {
	mgr := &ProcessManager{}
	procs, err := mgr.GetRunningProcesses()
	if err != nil {
		t.Fatalf("GetRunningProcesses failed: %v", err)
	}

	if len(procs) == 0 {
		t.Fatal("expected at least one running process, got 0")
	}

	// We should find our own current running test executable or "go.exe"
	currentExe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable failed: %v", err)
	}
	baseName := strings.ToLower(filepath.Base(currentExe))

	foundSelf := false
	for _, p := range procs {
		if strings.ToLower(p.Name) == baseName || strings.HasPrefix(strings.ToLower(p.Name), "go") {
			foundSelf = true
			if p.Count <= 0 {
				t.Errorf("expected count > 0 for %s, got %d", p.Name, p.Count)
			}
			break
		}
	}

	if !foundSelf {
		t.Logf("Note: current executable %s was not specifically matched, but %d processes were returned", baseName, len(procs))
	}

	// Verify no system services leaked through
	for _, p := range procs {
		lower := strings.ToLower(p.Name)
		if lower == "svchost.exe" || lower == "smss.exe" || lower == "csrss.exe" {
			t.Errorf("system process %s should have been filtered out", p.Name)
		}
	}
}
