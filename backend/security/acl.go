package security

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// ProtectFile restricts access to a file so that only the current Windows user
// can read or write it. On Windows, os.WriteFile with Unix mode bits (0600) is
// silently ignored — this function applies a proper DACL via icacls.
//
// It removes inherited ACEs (/inheritance:r) and grants only the current user
// full control (:F), preventing other local users and most malware from reading
// sensitive files like key.bin.
func ProtectFile(path string) error {
	username := os.Getenv("USERNAME")
	if username == "" {
		return fmt.Errorf("cannot determine current username (USERNAME env not set)")
	}

	// Build the principal in DOMAIN\User format if USERDOMAIN is set and differs from username.
	domain := os.Getenv("USERDOMAIN")
	var principal string
	if domain != "" && domain != username {
		principal = domain + `\` + username
	} else {
		principal = username
	}

	// /inheritance:r — disable inherited ACEs from parent directory
	// /grant:r       — replace (not add) an explicit ACE with Full Control (F)
	cmd := exec.Command("icacls", path,
		"/inheritance:r",
		"/grant:r", principal+":F",
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("icacls failed: %w (output: %s)", err, string(out))
	}
	return nil
}
