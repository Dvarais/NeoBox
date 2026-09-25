//go:build windows

package service

import (
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// TestWaitForShellTrayReadyReturnsPromptly pins down the cost of the wait that
// runs before the tray icon can appear.
//
// It regressed once in the way that is easy to miss: FindWindowW takes
// (lpClassName, lpWindowName), and "Shell_TrayWnd" — the taskbar's class — was
// passed as the window name. Nothing has that caption, so the lookup never
// matched, the loop ran its full 30-second timeout on every launch, and the icon
// showed up half a minute after the app did. The failure is entirely silent:
// waiting is what the function is for, so a wait that never ends early looks
// like the shell being slow rather than the call being wrong.
//
// Any interactive session has a taskbar, so the wait must return effectively at
// once. A second is far above the real cost and far below one poll interval of
// a loop that is not finding anything.
func TestWaitForShellTrayReadyReturnsPromptly(t *testing.T) {
	if !shellTrayExists(t) {
		t.Skip("no Explorer taskbar in this session — nothing to wait for")
	}

	start := time.Now()
	waitForShellTrayReady()
	elapsed := time.Since(start)

	if elapsed > time.Second {
		t.Errorf("waitForShellTrayReady took %v with the taskbar already up; "+
			"it is not matching the shell window and is burning its timeout", elapsed)
	}
}

// shellTrayExists looks the taskbar up independently of the code under test, so
// a skip means "no shell here" rather than "the lookup is broken".
func shellTrayExists(t *testing.T) bool {
	t.Helper()
	user32 := windows.NewLazySystemDLL("user32.dll")
	find := user32.NewProc("FindWindowW")
	class, err := windows.UTF16PtrFromString("Shell_TrayWnd")
	if err != nil {
		t.Fatalf("UTF16PtrFromString: %v", err)
	}
	hwnd, _, _ := find.Call(uintptr(unsafe.Pointer(class)), 0)
	return hwnd != 0
}
