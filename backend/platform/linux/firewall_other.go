//go:build !linux

package linux

import (
	"bytes"
	"context"
	"os/exec"
	"time"
)

func runFirewallCmd(name string, args ...string) ([]byte, error) {
	return runFirewallCmdInput(nil, name, args...)
}

func runFirewallCmdInput(input []byte, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	if len(input) > 0 {
		cmd.Stdin = bytes.NewReader(input)
	}
	return cmd.CombinedOutput()
}
