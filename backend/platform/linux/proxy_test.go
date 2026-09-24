package linux_test

import (
	"os"
	"path/filepath"
	"testing"

	"NeoBox/backend/platform/linux"
)

func TestProxyBackupLifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	pm := &linux.ProxyManager{}
	pm.SetUserDataDir(tmpDir)

	backupPath := filepath.Join(tmpDir, "proxy-backup.json")
	if err := os.WriteFile(backupPath, []byte(`{"server":"127.0.0.1:20809"}`), 0644); err != nil {
		t.Fatalf("failed to write test backup: %v", err)
	}

	pm.RecoverSystemProxy(tmpDir)
	if _, err := os.Stat(backupPath); !os.IsNotExist(err) {
		t.Fatalf("expected loopback backup to be discarded during recovery")
	}
}
