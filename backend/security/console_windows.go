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
	procFreeConsole := kernel32.NewProc("FreeConsole")

	if consoleProcessCount() == 1 {
		// Only our process is attached to this console — safe to detach.
		// FreeConsole will detach the process and close the console window if it was allocated by us.
		_, _, _ = procFreeConsole.Call()
		return true
	}
	return false
}

// consoleProcessCount returns how many processes are attached to the current
// console: 0 for a GUI-subsystem binary with no console at all, 1 when the
// console belongs to us alone, and 2 or more when it is shared with a parent
// shell (i.e. NeoBox was launched from an open terminal).
func consoleProcessCount() int {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleProcessList := kernel32.NewProc("GetConsoleProcessList")

	// The call reports the number of process IDs copied into the buffer; a
	// buffer of 2 is enough to tell "ours alone" from "shared with a parent".
	var processList [2]uint32
	r, _, _ := procGetConsoleProcessList.Call(uintptr(unsafe.Pointer(&processList[0])), 2)
	return int(r)
}
