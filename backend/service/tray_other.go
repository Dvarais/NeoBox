//go:build !windows

package service

func waitForShellTrayReady() {}

func FindMainWindow() uintptr {
	return 0
}

func checkWindowVisibleOS(hwnd uintptr) bool {
	return true
}
