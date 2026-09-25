//go:build !windows

package service

import (
	"os"

	"NeoBox/backend/platform"
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

func (s *AppService) RequestAdmin() {}

func RelaunchElevated() error {
	return nil
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
