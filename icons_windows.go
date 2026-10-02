//go:build windows

package main

import _ "embed"

//go:embed build/windows/icon.ico
var trayIcon []byte

// The same icon, desaturated. Which of the two is in the notification area is
// the only thing the tray says about the connection without being opened.
//
//go:embed build/windows/icon-off.ico
var trayIconOff []byte

