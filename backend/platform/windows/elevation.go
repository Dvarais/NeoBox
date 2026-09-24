//go:build windows

package windows

import (
	"os"

	"golang.org/x/sys/windows"
)

// IsElevated checks whether the current process has Windows administrator privileges.
func IsElevated() bool {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return false
	}
	defer token.Close()

	return token.IsElevated()
}

// ExitNow terminates the current process immediately without invoking exiting runtime handlers.
func ExitNow(code int) {
	_ = windows.TerminateProcess(windows.CurrentProcess(), uint32(code))
	os.Exit(code)
}
