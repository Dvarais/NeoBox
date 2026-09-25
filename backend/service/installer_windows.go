//go:build windows

package service

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

func runInstallerElevated(installerPath string) error {
	verbPtr, _ := windows.UTF16PtrFromString("runas")
	exePtr, _ := windows.UTF16PtrFromString(installerPath)
	dirPtr, _ := windows.UTF16PtrFromString(filepath.Dir(installerPath))
	argsPtr, _ := windows.UTF16PtrFromString("")
	return windows.ShellExecute(0, verbPtr, exePtr, argsPtr, dirPtr, windows.SW_SHOWNORMAL)
}
