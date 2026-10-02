//go:build !windows

package main

import _ "embed"

//go:embed build/tray/tray-on.png
var trayIcon []byte

// The same icon, desaturated. Which of the two is in the notification area is
// the only thing the tray says about the connection without being opened.
//
//go:embed build/tray/tray-off.png
var trayIconOff []byte

