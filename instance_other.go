//go:build !windows

package main

func closeInstanceHandle(h uintptr) {}

func relaunchElevatedIfNeeded(userDataDir string, mutexHandle uintptr) (uintptr, bool) {
	return mutexHandle, false
}

func bringExistingInstanceToForeground() {}
