package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func newTestService(t *testing.T) *AppService {
	t.Helper()
	return &AppService{userDataDir: t.TempDir()}
}

func writeBackupFile(t *testing.T, s *AppService, content string) {
	t.Helper()
	if err := os.WriteFile(s.proxyBackupPath(), []byte(content), 0600); err != nil {
		t.Fatalf("failed to seed the backup file: %v", err)
	}
}

// The whole point of persisting the backup: a run that crashed while holding the
// system proxy must still be recoverable on the next start.
func TestLoadProxyBackupRecoversPersistedConfig(t *testing.T) {
	s := newTestService(t)
	writeBackupFile(t, s, `{"server":"proxy.corp.local:3128","enable":1}`)

	s.loadProxyBackup()

	if !s.hasProxyBackup {
		t.Fatal("expected the persisted backup to be loaded")
	}
	if s.backupProxyServer != "proxy.corp.local:3128" {
		t.Errorf("backupProxyServer = %q, want %q", s.backupProxyServer, "proxy.corp.local:3128")
	}
	if s.backupProxyEnable != 1 {
		t.Errorf("backupProxyEnable = %d, want 1", s.backupProxyEnable)
	}
}

func TestLoadProxyBackupWithNoFile(t *testing.T) {
	s := newTestService(t)

	s.loadProxyBackup()

	if s.hasProxyBackup {
		t.Error("no backup file exists, so no backup should be reported")
	}
}

// A corrupt backup must be discarded rather than half-applied, and must not
// linger to be retried on every subsequent start.
func TestLoadProxyBackupDiscardsCorruptFile(t *testing.T) {
	s := newTestService(t)
	writeBackupFile(t, s, `{"server": not json`)

	s.loadProxyBackup()

	if s.hasProxyBackup {
		t.Error("a corrupt backup must not be reported as usable")
	}
	if _, err := os.Stat(s.proxyBackupPath()); !os.IsNotExist(err) {
		t.Error("a corrupt backup file should have been removed")
	}
}

// Restoring our own loopback address would point the user's browsers at a port
// with nothing behind it — worse than doing nothing at all.
func TestLoadProxyBackupRejectsSelfReference(t *testing.T) {
	s := newTestService(t)
	writeBackupFile(t, s, `{"server":"`+systemProxyAddr+`","enable":1}`)

	s.loadProxyBackup()

	if s.hasProxyBackup {
		t.Errorf("a backup pointing at %s must be rejected", systemProxyAddr)
	}
	if _, err := os.Stat(s.proxyBackupPath()); !os.IsNotExist(err) {
		t.Error("the self-referential backup file should have been removed")
	}
}

func TestLoadProxyBackupRejectsEmptyServer(t *testing.T) {
	s := newTestService(t)
	writeBackupFile(t, s, `{"server":"","enable":0}`)

	s.loadProxyBackup()

	if s.hasProxyBackup {
		t.Error("a backup with no server must be rejected")
	}
}

// NewAppService must pick the backup up, since main.go relies on it being in
// memory before it calls SetSystemProxy(false).
func TestNewAppServiceLoadsPersistedBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proxy-backup.json")
	payload, err := json.Marshal(proxyBackup{Server: "10.0.0.1:8080", Enable: 1})
	if err != nil {
		t.Fatalf("failed to marshal the backup: %v", err)
	}
	if err := os.WriteFile(path, payload, 0600); err != nil {
		t.Fatalf("failed to seed the backup file: %v", err)
	}

	s := NewAppService(nil, dir)

	if !s.hasProxyBackup {
		t.Fatal("NewAppService did not load the persisted backup")
	}
	if s.backupProxyServer != "10.0.0.1:8080" {
		t.Errorf("backupProxyServer = %q, want %q", s.backupProxyServer, "10.0.0.1:8080")
	}
}

// The persisted format must survive a round trip through the same struct the
// restore path reads, including a disabled-but-configured proxy.
func TestProxyBackupRoundTrip(t *testing.T) {
	for _, want := range []proxyBackup{
		{Server: "proxy.corp.local:3128", Enable: 1},
		{Server: "10.0.0.1:8080", Enable: 0},
	} {
		data, err := json.Marshal(want)
		if err != nil {
			t.Fatalf("marshal failed: %v", err)
		}
		var got proxyBackup
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if got != want {
			t.Errorf("round trip changed the backup: got %+v, want %+v", got, want)
		}
	}
}
