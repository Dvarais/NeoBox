//go:build windows

package windows_test

import (
	"strings"
	"testing"

	"NeoBox/backend/platform"
	_ "NeoBox/backend/platform"
)

func TestWindowsPlatformInitialized(t *testing.T) {
	p := platform.Current()
	if p == nil {
		t.Fatalf("expected platform.Current() to be initialized on Windows")
	}
	userData := p.Paths().UserDataDir()
	if !strings.Contains(userData, "AppData") || !strings.Contains(userData, "NeoBox") {
		t.Fatalf("unexpected Windows UserDataDir: %s", userData)
	}
}
