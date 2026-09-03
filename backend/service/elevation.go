package service

import (
	"os"
	"path/filepath"

	"NeoBox/backend/security"

	"golang.org/x/sys/windows"
)

// Windows process concerns: elevation and the single-instance mutex.

// ExitNow ends this process without going through ExitProcess.
//
// Returning from main, or calling os.Exit, ends up in runtime.exit →
// ExitProcess — and on the way out Windows calls back into this process.
// WebView2 and the systray each register callbacks through syscall.NewCallback,
// and ExitProcess runs window destruction and DLL detach handlers that reach
// them. By then the Go runtime has torn down the state those callbacks need, so
// the call lands in a runtime that cannot service it:
//
//	fatal error: exitsyscall: syscall frame is no longer valid
//	runtime.exitsyscall() / runtime.cgocallbackg() / runtime.stdcall() / runtime.exit()
//
// The throw happens inside a runtime that is already half gone, so it does not
// end the process either — it wedges it. What is left is a process with no
// windows and a single thread, alive indefinitely, holding its handles and its
// slot in the notification area. Every shutdown in the user's crash log ended
// this way, five out of five, and one of those corpses was still resident an
// hour later.
//
// TerminateProcess takes none of that path: no detach handlers, no window
// destruction, nothing calls back into Go. Callers are responsible for having
// already done everything that has to outlive the process — clearing the system
// proxy, removing the Kill Switch, wiping the keys — because none of it will
// happen after this returns. Nothing does.
func ExitNow(code uint32) {
	// WebView2's process tree does not go away on its own, and least of all on
	// this path: TerminateProcess runs no cleanup at all, so the browser process
	// and the GPU process under it would be orphaned every single time. They
	// have to go first, while there is still a process to walk down from.
	terminateWebViewChildren()

	_ = windows.TerminateProcess(windows.CurrentProcess(), code)
	// Not reached: TerminateProcess does not return for the calling process.
	// Present so the compiler sees a terminating path, and as a last resort if
	// the call somehow fails.
	os.Exit(int(code))
}

// IsElevated reports whether this process runs with administrative privileges.
func IsElevated() bool {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return false
	}
	defer token.Close()
	return token.IsElevated()
}

// CheckAdmin checks if the application runs with administrative/elevated privileges.
func (s *AppService) CheckAdmin() bool {
	return IsElevated()
}

// RelaunchElevated re-launches this executable through the UAC prompt.
//
// The caller must have released the single-instance mutex first: the elevated
// process starts while this one is still alive, and would find the mutex held
// and exit immediately.
//
// A returned error means the elevated process was not started — a declined UAC
// prompt is the ordinary case — and the caller stays responsible for continuing
// unelevated. On success the caller must exit, promptly: two instances are
// running until it does.
func RelaunchElevated() error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}

	verbPtr, _ := windows.UTF16PtrFromString("runas")
	exePtr, _ := windows.UTF16PtrFromString(exePath)
	dirPtr, _ := windows.UTF16PtrFromString(filepath.Dir(exePath))
	argsPtr, _ := windows.UTF16PtrFromString("")

	return windows.ShellExecute(0, verbPtr, exePtr, argsPtr, dirPtr, windows.SW_SHOWNORMAL)
}

// RequestAdmin triggers self-relaunch with administrative privileges.
//
// This is the runtime path — the user ticking TUN mode in a session that is not
// elevated. The launch-time path lives in main, which decides the same question
// from settings.json before any of this exists; see relaunchElevatedIfNeeded.
func (s *AppService) RequestAdmin() {
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

	if err := RelaunchElevated(); err != nil {
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
	// Everything that outlives the process is done above; see ExitNow for why
	// this must not be os.Exit with the UI and the tray already up.
	ExitNow(0)
}

// SetMutexHandle sets the single instance mutex handle so it can be released on relaunch.
func (s *AppService) SetMutexHandle(handle windows.Handle) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.mutexHandle = handle
}

// MutexHandle returns the single-instance mutex this process currently holds, or
// zero when it holds none. RequestAdmin can replace it, so this is the only
// reliable way to name the live handle at shutdown.
func (s *AppService) MutexHandle() windows.Handle {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.mutexHandle
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
