//go:build !linux

package linux

import "os/exec"

func runFirewallCmd(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	return cmd.CombinedOutput()
}

