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
//
// FIX #4: Removed incorrect path escaping. exec.Command passes each argument separately
// through the Windows CreateProcess API, so Go handles quoting automatically.
// The previous code doubled backslashes (C:\\Users\\... → invalid path) and used shell-style
// escape sequences that schtasks does not understand.
func SetupAutostart(taskName string, appPath string) error {
	// Sanitize only the task name to prevent argument injection via /tn
	safeName := strings.ReplaceAll(taskName, `"`, `'`)

	// schtasks requires administrative rights to create a task with highest privileges (/rl highest).
	// appPath is passed as a separate argument — Go's exec correctly wraps it in quotes if needed.
	cmd := exec.Command("schtasks", "/create",
		"/tn", safeName,
		"/tr", `"`+appPath+`"`,
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
//
// FIX #5: Added explicit IPv6 block rules to prevent IPv6 traffic from bypassing the VPN tunnel.
// Previously, only IPv4 outbound was blocked, allowing IPv6 to leak outside the tunnel.
func EnableKillSwitch(serverIP string) error {
	// First disable any pre-existing rules to avoid duplication
	_ = DisableKillSwitch()

	// 1. Block all outbound IPv4 traffic by default
	cmd1 := exec.Command("netsh", "advfirewall", "firewall", "add", "rule",
		"name=NeoBox-KillSwitch",
		"dir=out",
		"action=block",
		"profile=any",
		"protocol=any",
		"remoteip=0.0.0.0/0",
	)
	hideWindow(cmd1)
	_ = cmd1.Run()

	// 2. Block all outbound IPv6 traffic to prevent IPv6 leaks
	cmd2 := exec.Command("netsh", "advfirewall", "firewall", "add", "rule",
		"name=NeoBox-KillSwitch-IPv6",
		"dir=out",
		"action=block",
		"profile=any",
		"protocol=any",
		"remoteip=::/0",
	)
	hideWindow(cmd2)
	_ = cmd2.Run()

	// 3. Allow local loopback and LAN subnets (IPv4)
	cmd3 := exec.Command("netsh", "advfirewall", "firewall", "add", "rule",
		"name=NeoBox-KillSwitch-LAN",
		"dir=out",
		"action=allow",
		"remoteip=127.0.0.1,192.168.0.0/16,10.0.0.0/8,172.16.0.0/12",
		"profile=any",
	)
	hideWindow(cmd3)
	_ = cmd3.Run()

	// 4. Allow local IPv6 loopback (::1) so localhost still works
	cmd4 := exec.Command("netsh", "advfirewall", "firewall", "add", "rule",
		"name=NeoBox-KillSwitch-LANv6",
		"dir=out",
		"action=allow",
		"remoteip=::1",
		"profile=any",
	)
	hideWindow(cmd4)
	_ = cmd4.Run()

	// 5. Allow traffic to the specific VPN server IP (if available and is an IPv4)
	if serverIP != "" && serverIP != "127.0.0.1" {
		cmd5 := exec.Command("netsh", "advfirewall", "firewall", "add", "rule",
			"name=NeoBox-KillSwitch-Allow",
			"dir=out",
			"action=allow",
			"remoteip="+serverIP,
			"profile=any",
		)
		hideWindow(cmd5)
		_ = cmd5.Run()
	}

	return nil
}

// DisableKillSwitch removes the NeoBox-related block rules from Windows Firewall.
func DisableKillSwitch() error {
	ruleNames := []string{
		"NeoBox-KillSwitch",
		"NeoBox-KillSwitch-IPv6",
		"NeoBox-KillSwitch-LAN",
		"NeoBox-KillSwitch-LANv6",
		"NeoBox-KillSwitch-Allow",
	}

	for _, name := range ruleNames {
		cmd := exec.Command("netsh", "advfirewall", "firewall", "delete", "rule", "name="+name)
		hideWindow(cmd)
		_ = cmd.Run()
	}

	return nil
}
