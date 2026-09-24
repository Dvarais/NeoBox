package linux_test

import (
	"os"
	"path/filepath"
	"testing"

	"NeoBox/backend/platform/linux"
)

func TestFirewallMarkerRecovery(t *testing.T) {
	tmpDir := t.TempDir()
	marker := filepath.Join(tmpDir, "killswitch.active")
	if err := os.WriteFile(marker, []byte("active"), 0644); err != nil {
		t.Fatalf("failed to create marker: %v", err)
	}

	fw := &linux.FirewallManager{}
	fw.RecoverKillSwitch(tmpDir)

	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("expected marker to be removed during recovery")
	}
}
