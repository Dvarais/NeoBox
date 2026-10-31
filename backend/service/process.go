package service

import (
	"NeoBox/backend/platform"
)

// GetRunningProcesses returns the consolidated list of running user applications
// for split tunneling configuration.
func (s *AppService) GetRunningProcesses() ([]platform.ProcessInfo, error) {
	curr := platform.Current()
	if curr == nil || curr.Processes() == nil {
		return nil, nil
	}
	return curr.Processes().GetRunningProcesses()
}
