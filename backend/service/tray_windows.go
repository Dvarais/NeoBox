//go:build windows

package service

import (
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func waitForShellTrayReady() {
	shellTrayPtr, _ := windows.UTF16PtrFromString("Shell_TrayWnd")
	deadline := time.Now().Add(shellTrayWaitTimeout)
	for time.Now().Before(deadline) {
		hwnd, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(shellTrayPtr)), 0)
		if hwnd != 0 {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	fmt.Println("[tray] notification area did not appear within the timeout; adding the icon anyway")
}

var (
	procFindWindowW     = user32.NewProc("FindWindowW")
	procIsWindowVisible = user32.NewProc("IsWindowVisible")
	procIsIconic        = user32.NewProc("IsIconic")
)

const mainWindowClass = "winc_Form"

func FindMainWindow() uintptr {
	class, err := windows.UTF16PtrFromString(mainWindowClass)
	if err != nil {
		return 0
	}
	title, err := windows.UTF16PtrFromString("NeoBox")
	if err != nil {
		return 0
	}
	hwnd, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(title)))
	return hwnd
}

func checkWindowVisibleOS(hwnd uintptr) bool {
	if hwnd == 0 {
		return false
	}
	visible, _, _ := procIsWindowVisible.Call(hwnd)
	if visible == 0 {
		return false
	}
	minimised, _, _ := procIsIconic.Call(hwnd)
	return minimised == 0
}
