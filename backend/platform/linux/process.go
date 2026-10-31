//go:build linux

package linux

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ProcessInfo represents a running application instance.
type ProcessInfo struct {
	Name  string `json:"name"`  // executable name, e.g. "firefox"
	Path  string `json:"path"`  // full path if accessible
	Count int    `json:"count"` // number of running instances/workers
}

type ProcessManager struct{}

// GetRunningProcesses scans the /proc filesystem for running user processes.
func (m *ProcessManager) GetRunningProcesses() ([]ProcessInfo, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}

	counts := make(map[string]int)
	paths := make(map[string]string)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// check if directory name is a PID (number)
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}

		pidDir := filepath.Join("/proc", entry.Name())
		// Try to read symlink /proc/[pid]/exe
		exePath, err := os.Readlink(filepath.Join(pidDir, "exe"))
		var name string
		if err == nil && exePath != "" {
			name = filepath.Base(exePath)
		} else {
			// Fallback to /proc/[pid]/comm
			commBytes, err := os.ReadFile(filepath.Join(pidDir, "comm"))
			if err == nil {
				name = strings.TrimSpace(string(commBytes))
			}
		}

		if name != "" && !strings.HasPrefix(name, "kworker") && name != "systemd" {
			counts[name]++
			if paths[name] == "" && exePath != "" {
				paths[name] = exePath
			}
		}
	}

	res := make([]ProcessInfo, 0, len(counts))
	for name, count := range counts {
		res = append(res, ProcessInfo{
			Name:  name,
			Path:  paths[name],
			Count: count,
		})
	}

	sort.Slice(res, func(i, j int) bool {
		return strings.ToLower(res[i].Name) < strings.ToLower(res[j].Name)
	})

	return res, nil
}
