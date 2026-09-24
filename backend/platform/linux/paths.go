package linux

import (
	"os"
	"path/filepath"
)

type PathManager struct{}

func (m *PathManager) UserDataDir() string {
	return m.ConfigDir()
}

func (m *PathManager) ConfigDir() string {
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home, _ := os.UserHomeDir()
		configHome = filepath.Join(home, ".config")
	}
	dir := filepath.Join(configHome, "neobox")
	_ = os.MkdirAll(dir, 0700)
	return dir
}

func (m *PathManager) LogDir() string {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		home, _ := os.UserHomeDir()
		dataHome = filepath.Join(home, ".local", "share")
	}
	dir := filepath.Join(dataHome, "neobox")
	_ = os.MkdirAll(dir, 0700)
	return dir
}

func (m *PathManager) ResolvePath(elem ...string) string {
	parts := append([]string{m.UserDataDir()}, elem...)
	return filepath.Join(parts...)
}
