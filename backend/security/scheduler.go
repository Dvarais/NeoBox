package security

import (
	"os/exec"
	"strings"
	"syscall"
)

// hideWindow sets the SysProcAttr on Windows exec.Cmd to prevent flashing console windows.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

// SetupAutostart creates a task in Windows Task Scheduler with the highest execution privileges
// (RL Highest) to allow it to run on logon and bypass UAC prompts for TUN mode setup.
func SetupAutostart(taskName string, appPath string) error {
	// Escape backslashes and double-quotes in the path to prevent schtasks argument injection.
	safePath := strings.ReplaceAll(appPath, `\`, `\\`)
	safePath = strings.ReplaceAll(safePath, `"`, `\"`)

	// schtasks requires administrative rights to create a task with highest privileges (/rl highest)
	cmd := exec.Command("schtasks", "/create",
		"/tn", taskName,
		"/tr", `"`+safePath+`"`,
		"/sc", "onlogon",
		"/rl", "highest",
		"/f",
	)
	hideWindow(cmd)
	return cmd.Run()
}

// RemoveAutostart removes the task from Windows Task Scheduler.
func RemoveAutostart(taskName string) error {
	cmd := exec.Command("schtasks", "/delete", "/tn", taskName, "/f")
	hideWindow(cmd)
	return cmd.Run()
}

// IsAutostartEnabled checks if the high-priority task exists in Windows Task Scheduler.
func IsAutostartEnabled(taskName string) bool {
	cmd := exec.Command("schtasks", "/query", "/tn", taskName)
	hideWindow(cmd)
	err := cmd.Run()
	return err == nil
}

// EnableKillSwitch sets up Windows Firewall rules to block all WAN traffic except to local LAN and the VPN server IP.
func EnableKillSwitch(serverIP string) error {
	// First disable any pre-existing rules to avoid duplication
	_ = DisableKillSwitch()

	// 1. Block all outbound traffic by default
	cmd1 := exec.Command("netsh", "advfirewall", "firewall", "add", "rule",
		"name=NeoBox-KillSwitch",
		"dir=out",
		"action=block",
		"profile=any",
	)
	hideWindow(cmd1)
	_ = cmd1.Run()

	// 2. Allow local loopback and LAN subnets
	cmd2 := exec.Command("netsh", "advfirewall", "firewall", "add", "rule",
		"name=NeoBox-KillSwitch-LAN",
		"dir=out",
		"action=allow",
		"remoteip=127.0.0.1,192.168.0.0/16,10.0.0.0/8,172.16.0.0/12",
		"profile=any",
	)
	hideWindow(cmd2)
	_ = cmd2.Run()

	// 3. Allow traffic to the specific VPN server IP (if available)
	if serverIP != "" && serverIP != "127.0.0.1" {
		cmd3 := exec.Command("netsh", "advfirewall", "firewall", "add", "rule",
			"name=NeoBox-KillSwitch-Allow",
			"dir=out",
			"action=allow",
			"remoteip="+serverIP,
			"profile=any",
		)
		hideWindow(cmd3)
		_ = cmd3.Run()
	}

	return nil
}

// DisableKillSwitch removes the NeoBox-related block rules from Windows Firewall.
func DisableKillSwitch() error {
	cmd1 := exec.Command("netsh", "advfirewall", "firewall", "delete", "rule", "name=NeoBox-KillSwitch")
	hideWindow(cmd1)
	_ = cmd1.Run()

	cmd2 := exec.Command("netsh", "advfirewall", "firewall", "delete", "rule", "name=NeoBox-KillSwitch-LAN")
	hideWindow(cmd2)
	_ = cmd2.Run()

	cmd3 := exec.Command("netsh", "advfirewall", "firewall", "delete", "rule", "name=NeoBox-KillSwitch-Allow")
	hideWindow(cmd3)
	_ = cmd3.Run()

	return nil
}
