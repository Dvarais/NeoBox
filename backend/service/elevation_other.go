//go:build !windows

package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"NeoBox/backend/platform"
	"NeoBox/backend/security"
)

func ExitNow(code uint32) {
	os.Exit(int(code))
}

func IsElevated() bool {
	if p := platform.Current(); p != nil && p.Privileges() != nil {
		return p.Privileges().CanRunTUN()
	}
	return os.Geteuid() == 0
}

func (s *AppService) CheckAdmin() bool {
	return IsElevated()
}

func (s *AppService) RequestAdmin() {
	if IsElevated() {
		return
	}
	if err := RelaunchElevated(); err != nil {
		return
	}

	s.SetQuitting(true)
	s.stopWatchdog()
	if s.coreManager != nil {
		_ = s.coreManager.Stop()
	}
	s.SetSystemProxy(false)
	s.disableKillSwitch()
	security.MarkCleanExit("relaunch as administrator")
	ExitNow(0)
}

func RelaunchElevated() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	// Persist root permissions ("сохранение root прав") on the binary via setcap.
	// This requires only a single authentication prompt and permanently grants network
	// capabilities so future runs require zero password prompts.
	cmd := exec.Command("pkexec", "setcap", "cap_net_admin,cap_net_bind_service+eip", exe)
	if err := cmd.Run(); err != nil {
		sudoCmd := exec.Command("sudo", "setcap", "cap_net_admin,cap_net_bind_service+eip", exe)
		if sudoErr := sudoCmd.Run(); sudoErr != nil {
			return fmt.Errorf("failed to grant capabilities: pkexec (%v), sudo (%v)", err, sudoErr)
		}
	}

	return exec.Command(exe, os.Args[1:]...).Start()
}

func (s *AppService) SetMutexHandle(handle uintptr) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.mutexHandle = handle
}

func (s *AppService) MutexHandle() uintptr {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.mutexHandle
}

func AcquireSingleInstanceMutex() (uintptr, bool) {
	return 1, false
}

