//go:build linux

package linux

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// runFirewallCmd executes a firewall command with ambient CAP_NET_ADMIN capabilities
// so child processes (nft / iptables / ip6tables) inherit network configuration rights from an
// unprivileged process with file capabilities, bounded by a 4-second execution timeout.
func runFirewallCmd(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	if os.Geteuid() != 0 {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{
			AmbientCaps: []uintptr{12}, // CAP_NET_ADMIN
		}
		out, err := cmd.CombinedOutput()
		if err == nil {
			return out, nil
		}
		// Fallback without AmbientCaps in case the capability is already present
		// or ambient capabilities are restricted in the current environment.
		cmdFallback := exec.CommandContext(ctx, name, args...)
		outFallback, errFallback := cmdFallback.CombinedOutput()
		if errFallback == nil {
			return outFallback, nil
		}
		return out, err
	}
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.CombinedOutput()
}
