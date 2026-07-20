//go:build windows
package security

import (
	"syscall"
	"unsafe"
)

// HideConsoleIfNeeded detaches the process from the console if it is the only
// process attached to it (meaning it was launched directly, not from an
// existing terminal). This prevents a console window from showing up on startup.
func HideConsoleIfNeeded() bool {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleProcessList := kernel32.NewProc("GetConsoleProcessList")
	procFreeConsole := kernel32.NewProc("FreeConsole")

	var processList [2]uint32
	// Get the number of processes attached to the current console.
	// The function returns the number of process IDs copied into the buffer.
	r, _, _ := procGetConsoleProcessList.Call(uintptr(unsafe.Pointer(&processList[0])), 2)
	if r == 1 {
		// Only our process is attached to this console — safe to detach.
		// FreeConsole will detach the process and close the console window if it was allocated by us.
		_, _, _ = procFreeConsole.Call()
		return true
	}
	return false
}
