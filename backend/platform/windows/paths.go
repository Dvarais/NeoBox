//go:build windows

package windows

import (
	"os"
	"path/filepath"
)

type PathManager struct{}

func (m *PathManager) UserDataDir() string {
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, "AppData", "Roaming", "NeoBox")
}

func (m *PathManager) ConfigDir() string {
	return m.UserDataDir()
}

func (m *PathManager) LogDir() string {
	return m.UserDataDir()
}

func (m *PathManager) ResolvePath(elem ...string) string {
	parts := append([]string{m.UserDataDir()}, elem...)
	return filepath.Join(parts...)
}
