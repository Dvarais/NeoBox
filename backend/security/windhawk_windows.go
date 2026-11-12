//go:build windows

package security

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

var (
	kernel32             = syscall.NewLazyDLL("kernel32.dll")
	procGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
)

// WindhawkSettingsKey is the registry subkey where Windhawk stores global engine settings.
const WindhawkSettingsKey = `SOFTWARE\Windhawk\Engine\Settings`

// defaultProtectedProcesses lists the executables that must be excluded from Windhawk injection.
// - NeoBox.exe / NeoBox-Go.exe: Go 1.25+ binaries crash with 0xC0000005 on mod unload / stack unwinding
//   due to runtime.mcall zeroing RBP while SEH marks RBP as frame register (golang/go#81885, ramensoftware/windhawk#1175).
// - msedgewebview2.exe: WebView2 child processes can crash or hang if foreign hooks are injected into renderers.
// - NeoBox_Setup.exe: The updater installer executable.
var defaultProtectedProcesses = []string{
	"NeoBox.exe",
	"NeoBox-Go.exe",
	"msedgewebview2.exe",
	"NeoBox_Setup.exe",
}

// IsWindhawkInjected returns true if windhawk.dll is currently loaded into the calling process.
func IsWindhawkInjected() bool {
	pName, err := syscall.UTF16PtrFromString("windhawk.dll")
	if err != nil {
		return false
	}
	r, _, _ := procGetModuleHandleW.Call(uintptr(unsafe.Pointer(pName)))
	return r != 0
}

// IsWindhawkInstalled checks whether Windhawk is installed or configured on the system.
func IsWindhawkInstalled() bool {
	// Check HKLM
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Windhawk`, registry.QUERY_VALUE)
	if err == nil {
		_ = k.Close()
		return true
	}

	// Check HKCU
	k, err = registry.OpenKey(registry.CURRENT_USER, `SOFTWARE\Windhawk`, registry.QUERY_VALUE)
	if err == nil {
		_ = k.Close()
		return true
	}

	// Check default installation directory
	programFiles := os.Getenv("ProgramFiles")
	if programFiles != "" && dirExists(filepath.Join(programFiles, "Windhawk")) {
		return true
	}

	// Check ProgramData
	programData := os.Getenv("ProgramData")
	if programData != "" && dirExists(filepath.Join(programData, "Windhawk")) {
		return true
	}

	return false
}

func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// GetProtectedProcesses returns the list of process names that must be excluded from Windhawk.
func GetProtectedProcesses() []string {
	targets := make([]string, 0, len(defaultProtectedProcesses)+1)
	targets = append(targets, defaultProtectedProcesses...)

	if exePath, err := os.Executable(); err == nil && exePath != "" {
		base := filepath.Base(exePath)
		alreadyListed := false
		for _, p := range targets {
			if strings.EqualFold(p, base) {
				alreadyListed = true
				break
			}
		}
		if !alreadyListed {
			targets = append(targets, base)
		}
	}
	return targets
}

// MergeExclusions combines an existing pipe-separated Windhawk Exclude pattern with the target processes.
// It preserves all existing entries and appends any missing targets.
func MergeExclusions(existing string, targets []string) (string, bool) {
	rawParts := strings.Split(existing, "|")
	entries := make([]string, 0, len(rawParts)+len(targets))
	seen := make(map[string]bool)

	for _, raw := range rawParts {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)
		if !seen[lower] {
			seen[lower] = true
			entries = append(entries, trimmed)
		}
	}

	modified := false
	for _, target := range targets {
		target = strings.TrimSpace(target)
		if target == "" {
			continue
		}
		lower := strings.ToLower(target)
		if !seen[lower] {
			seen[lower] = true
			entries = append(entries, target)
			modified = true
		}
	}

	return strings.Join(entries, "|"), modified
}

// RemoveFromInclude removes any target processes from an existing pipe-separated Windhawk Include pattern.
func RemoveFromInclude(existing string, targets []string) (string, bool) {
	if strings.TrimSpace(existing) == "" {
		return "", false
	}

	targetMap := make(map[string]bool)
	for _, target := range targets {
		trimmed := strings.TrimSpace(target)
		if trimmed != "" {
			targetMap[strings.ToLower(trimmed)] = true
		}
	}

	rawParts := strings.Split(existing, "|")
	entries := make([]string, 0, len(rawParts))
	modified := false

	for _, raw := range rawParts {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		if targetMap[strings.ToLower(trimmed)] {
			modified = true
			continue
		}
		entries = append(entries, trimmed)
	}

	return strings.Join(entries, "|"), modified
}

// EnsureWindhawkCompatibility inspects the Windhawk configuration in the registry and guarantees
// that NeoBox and its WebView2 child processes are added to the process exclusion list.
// If Windhawk is not installed, it does nothing and returns nil.
func EnsureWindhawkCompatibility() error {
	if !IsWindhawkInstalled() {
		return nil
	}

	if IsWindhawkInjected() {
		fmt.Println("[windhawk] warning: windhawk.dll is currently loaded in this process; exclusion list is being synchronized")
	}

	targets := GetProtectedProcesses()

	rootKeys := []struct {
		name string
		root registry.Key
	}{
		{"HKLM", registry.LOCAL_MACHINE},
		{"HKCU", registry.CURRENT_USER},
	}

	// Also check 64-bit and 32-bit registry views to ensure thoroughness.
	accessFlags := []uint32{
		registry.QUERY_VALUE | registry.SET_VALUE,
		registry.QUERY_VALUE | registry.SET_VALUE | registry.WOW64_64KEY,
	}

	var lastErr error
	for _, rk := range rootKeys {
		for _, access := range accessFlags {
			k, err := registry.OpenKey(rk.root, WindhawkSettingsKey, access)
			if err != nil {
				// If subkey doesn't exist yet, try creating it only if the parent Windhawk key exists.
				parent, pErr := registry.OpenKey(rk.root, `SOFTWARE\Windhawk`, registry.QUERY_VALUE)
				if pErr == nil {
					_ = parent.Close()
					k, _, err = registry.CreateKey(rk.root, WindhawkSettingsKey, access)
				}
			}

			if err != nil {
				// Expected if non-elevated on HKLM; ignore and proceed
				lastErr = err
				continue
			}

			// 1. Process Exclude
			currentExclude, _, valErr := k.GetStringValue("Exclude")
			if valErr != nil && valErr != registry.ErrNotExist {
				_ = k.Close()
				continue
			}

			newExclude, excModified := MergeExclusions(currentExclude, targets)
			if excModified {
				if writeErr := k.SetStringValue("Exclude", newExclude); writeErr == nil {
					fmt.Printf("[windhawk] Added NeoBox to Windhawk Exclude list (%s): %s\n", rk.name, newExclude)
				} else {
					lastErr = writeErr
				}
			}

			// 2. Process Include (ensure protected processes are not explicitly included)
			currentInclude, _, incErr := k.GetStringValue("Include")
			if incErr == nil && currentInclude != "" {
				newInclude, incModified := RemoveFromInclude(currentInclude, targets)
				if incModified {
					_ = k.SetStringValue("Include", newInclude)
					fmt.Printf("[windhawk] Removed protected processes from Windhawk Include list (%s)\n", rk.name)
				}
			}

			_ = k.Close()
		}
	}

	// Returning nil even if non-admin write to HKLM failed, as elevated relaunch will retry.
	_ = lastErr
	return nil
}
