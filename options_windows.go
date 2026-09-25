//go:build windows

package main

import (
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

func getWindowsOptions(userDataDir string) *windows.Options {
	return &windows.Options{
		WebviewIsTransparent: false,
		WindowIsTranslucent:  false,
		BackdropType:         windows.None,
		Theme:                windows.Dark,
		WebviewUserDataPath:  userDataDir,
		WebviewGpuIsDisabled: true,
	}
}
