package security

import (
	"fmt"
	"os/exec"
	"os/user"
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
	u, err := user.Current()
	if err != nil {
		return fmt.Errorf("failed to get current user: %w", err)
	}

	// u.Uid contains the Windows Security Identifier (SID) of the current user, e.g. "S-1-5-21-..."
	// Prefixing with "*" tells icacls to treat it as a SID rather than a username.
	principal := "*" + u.Uid

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
