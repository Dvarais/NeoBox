//go:build !windows

package service

import (
	"os/exec"
)

func runInstallerElevated(installerPath string) error {
	cmd := exec.Command("xdg-open", installerPath)
	return cmd.Start()
}
