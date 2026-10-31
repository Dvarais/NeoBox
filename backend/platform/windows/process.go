//go:build windows

package windows

import (
	"sort"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ProcessInfo represents a running application instance.
type ProcessInfo struct {
	Name  string `json:"name"`  // executable name, e.g. "chrome.exe"
	Path  string `json:"path"`  // full path if accessible
	Count int    `json:"count"` // number of running instances/workers
}

type ProcessManager struct{}

// systemExes defines Windows kernel, subsystem and driver services that are not user applications.
var systemExes = map[string]bool{
	"system":                      true,
	"[system process]":            true,
	"system idle process":         true,
	"registry":                    true,
	"smss.exe":                    true,
	"csrss.exe":                   true,
	"wininit.exe":                 true,
	"services.exe":                true,
	"lsass.exe":                   true,
	"svchost.exe":                 true,
	"fontdrvhost.exe":             true,
	"winlogon.exe":                true,
	"dwm.exe":                     true,
	"spoolsv.exe":                 true,
	"sihost.exe":                  true,
	"taskhostw.exe":               true,
	"ctfmon.exe":                  true,
	"conhost.exe":                 true,
	"runtimebroker.exe":           true,
	"searchhost.exe":              true,
	"startmenuexperiencehost.exe": true,
	"shellexperiencehost.exe":     true,
	"textinputhost.exe":           true,
	"securityhealthservice.exe":   true,
	"smartscreen.exe":             true,
	"compattelrunner.exe":         true,
}

// GetRunningProcesses scans active processes via Win32 Toolhelp API, consolidates multiple
// worker/child instances, extracts optional binary paths, and filters out kernel services.
func (m *ProcessManager) GetRunningProcesses() ([]ProcessInfo, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))

	err = windows.Process32First(snapshot, &entry)
	if err != nil {
		return nil, err
	}

	counts := make(map[string]int)
	paths := make(map[string]string)

	for {
		name := windows.UTF16ToString(entry.ExeFile[:])
		lowerName := strings.ToLower(strings.TrimSpace(name))

		if lowerName != "" && !systemExes[lowerName] {
			counts[name]++

			// Attempt to resolve executable path for the first instance
			if paths[name] == "" && entry.ProcessID > 4 {
				hProc, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, entry.ProcessID)
				if err == nil {
					var buf [windows.MAX_PATH]uint16
					size := uint32(len(buf))
					if err := windows.QueryFullProcessImageName(hProc, 0, &buf[0], &size); err == nil {
						paths[name] = windows.UTF16ToString(buf[:size])
					}
					windows.CloseHandle(hProc)
				}
			}
		}

		err = windows.Process32Next(snapshot, &entry)
		if err != nil {
			break
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
