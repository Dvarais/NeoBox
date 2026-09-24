package linux

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

type ProxyManager struct {
	userDataDir string
}

func (m *ProxyManager) SetUserDataDir(dir string) {
	m.userDataDir = dir
}

func (m *ProxyManager) backupPath() string {
	return filepath.Join(m.userDataDir, "proxy-backup.json")
}

func (m *ProxyManager) SetSystemProxy(addr string) error {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		host = "127.0.0.1"
		portStr = "20809"
	}
	port, _ := strconv.Atoi(portStr)

	// GNOME desktop proxy
	if _, err := exec.LookPath("gsettings"); err == nil {
		_ = exec.Command("gsettings", "set", "org.gnome.system.proxy", "mode", "manual").Run()
		_ = exec.Command("gsettings", "set", "org.gnome.system.proxy.http", "host", host).Run()
		_ = exec.Command("gsettings", "set", "org.gnome.system.proxy.http", "port", strconv.Itoa(port)).Run()
		_ = exec.Command("gsettings", "set", "org.gnome.system.proxy.https", "host", host).Run()
		_ = exec.Command("gsettings", "set", "org.gnome.system.proxy.https", "port", strconv.Itoa(port)).Run()
	}

	// KDE Plasma desktop proxy
	if _, err := exec.LookPath("kwriteconfig5"); err == nil {
		_ = exec.Command("kwriteconfig5", "--file", "kioslaverc", "--group", "Proxy Settings", "--key", "ProxyType", "1").Run()
		_ = exec.Command("kwriteconfig5", "--file", "kioslaverc", "--group", "Proxy Settings", "--key", "httpProxy", "http://"+addr).Run()
	} else if _, err := exec.LookPath("kwriteconfig6"); err == nil {
		_ = exec.Command("kwriteconfig6", "--file", "kioslaverc", "--group", "Proxy Settings", "--key", "ProxyType", "1").Run()
		_ = exec.Command("kwriteconfig6", "--file", "kioslaverc", "--group", "Proxy Settings", "--key", "httpProxy", "http://"+addr).Run()
	}

	return nil
}

func (m *ProxyManager) RestoreSystemProxy() error {
	if _, err := exec.LookPath("gsettings"); err == nil {
		_ = exec.Command("gsettings", "set", "org.gnome.system.proxy", "mode", "none").Run()
	}
	if _, err := exec.LookPath("kwriteconfig5"); err == nil {
		_ = exec.Command("kwriteconfig5", "--file", "kioslaverc", "--group", "Proxy Settings", "--key", "ProxyType", "0").Run()
	} else if _, err := exec.LookPath("kwriteconfig6"); err == nil {
		_ = exec.Command("kwriteconfig6", "--file", "kioslaverc", "--group", "Proxy Settings", "--key", "ProxyType", "0").Run()
	}
	return nil
}

func (m *ProxyManager) RecoverSystemProxy(userDataDir string) {
	bp := filepath.Join(userDataDir, "proxy-backup.json")
	data, err := os.ReadFile(bp)
	if err != nil {
		return
	}
	var b struct {
		Server string `json:"server"`
	}
	if err := json.Unmarshal(data, &b); err == nil && (b.Server == "127.0.0.1:20809" || b.Server == "") {
		_ = os.Remove(bp)
		_ = m.RestoreSystemProxy()
	}
}
