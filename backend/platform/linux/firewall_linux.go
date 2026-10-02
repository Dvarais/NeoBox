//go:build linux

package linux

import (
	"os"
	"os/exec"
	"syscall"
)

// runFirewallCmd executes a firewall command with ambient CAP_NET_ADMIN capabilities
// so child processes (nft / iptables) inherit network configuration rights from an
// unprivileged process with file capabilities.
func runFirewallCmd(name string, args ...string) ([]byte, error) {
	if os.Geteuid() != 0 {
		cmd := exec.Command(name, args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{
			AmbientCaps: []uintptr{12}, // CAP_NET_ADMIN
		}
		out, err := cmd.CombinedOutput()
		if err == nil {
			return out, nil
		}
		// Fallback without AmbientCaps in case the capability is already present
		// or ambient capabilities are restricted in the current environment.
		cmdFallback := exec.Command(name, args...)
		outFallback, errFallback := cmdFallback.CombinedOutput()
		if errFallback == nil {
			return outFallback, nil
		}
		return out, err
	}
	cmd := exec.Command(name, args...)
	return cmd.CombinedOutput()
}

