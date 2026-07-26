package service

import (
	"os"
	"path/filepath"

	"NeoBox/backend/security"

	"golang.org/x/sys/windows"
)

// Windows process concerns: elevation and the single-instance mutex.

// CheckAdmin checks if the application runs with administrative/elevated privileges.
func (s *AppService) CheckAdmin() bool {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return false
	}
	defer token.Close()
	return token.IsElevated()
}

// RequestAdmin triggers self-relaunch with administrative privileges.
func (s *AppService) RequestAdmin() {
	exePath, err := os.Executable()
	if err != nil {
		return
	}

	verbPtr, _ := windows.UTF16PtrFromString("runas")
	exePtr, _ := windows.UTF16PtrFromString(exePath)
	dirPtr, _ := windows.UTF16PtrFromString(filepath.Dir(exePath))
	argsPtr, _ := windows.UTF16PtrFromString("")

	// Release single-instance mutex BEFORE launching the elevated process.
	// This prevents a race condition where the elevated process starts up,
	// finds the mutex is still held by this process, and immediately exits.
	s.stateMu.Lock()
	hasMutex := s.mutexHandle != 0
	oldMutex := s.mutexHandle
	if hasMutex {
		_ = windows.CloseHandle(oldMutex)
		s.mutexHandle = 0
	}
	s.stateMu.Unlock()

	if err := windows.ShellExecute(0, verbPtr, exePtr, argsPtr, dirPtr, windows.SW_SHOWNORMAL); err != nil {
		// ShellExecute failed (e.g. user declined UAC). Since we already closed the mutex,
		// we must re-acquire it so this instance remains protected as the single instance.
		if hasMutex {
			s.stateMu.Lock()
			s.mutexHandle, _ = AcquireSingleInstanceMutex()
			s.stateMu.Unlock()
		}
		return
	}

	// The elevated instance is launching. We must release the single-instance
	// mutex IMMEDIATELY so the elevated process can acquire it, and we cannot
	// afford to block: systray.Quit() / full Quit() enter message-loop teardown
	// that can hang (and previously left a zombie process holding the mutex,
	// which blocked the elevated relaunch entirely). So we do only the fast,
	// synchronous cleanup of persistent OS state here, then exit at once.
	s.SetQuitting(true)
	s.stopWatchdog()
	if s.coreManager != nil {
		_ = s.coreManager.Stop()
	}
	s.SetSystemProxy(false)
	s.disableKillSwitch()
	security.MarkCleanExit("relaunch as administrator")
	os.Exit(0)
}

// SetMutexHandle sets the single instance mutex handle so it can be released on relaunch.
func (s *AppService) SetMutexHandle(handle windows.Handle) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.mutexHandle = handle
}

// AcquireSingleInstanceMutex creates a Windows named mutex to ensure only one
// instance of NeoBox runs at a time.
func AcquireSingleInstanceMutex() (windows.Handle, bool) {
	if handle, already := createSingleInstanceMutex("Global\\NeoBox-SingleInstance-Mutex"); handle != 0 || already {
		return handle, already
	}
	// Global\ failed (e.g. access denied) — fall back to Local\.
	handle, already := createSingleInstanceMutex("Local\\NeoBox-SingleInstance-Mutex")
	return handle, already
}

func createSingleInstanceMutex(name string) (windows.Handle, bool) {
	mutexName, _ := windows.UTF16PtrFromString(name)
	handle, err := windows.CreateMutex(nil, false, mutexName)
	if err != nil {
		if err == windows.ERROR_ALREADY_EXISTS {
			if handle != 0 {
				_ = windows.CloseHandle(handle)
			}
			return 0, true // Another instance holds the mutex
		}
		return 0, false
	}
	return handle, false
}
