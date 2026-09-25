//go:build windows

package service

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestCreateSingleInstanceMutexDuplicate(t *testing.T) {
	testMutexName := "Local\\NeoBox-Test-Mutex-Duplicate"

	// First acquisition must succeed
	h1, already1 := createSingleInstanceMutex(testMutexName)
	if already1 {
		t.Fatalf("first creation reported already running")
	}
	if h1 == 0 {
		t.Fatalf("first creation returned handle 0")
	}
	defer windows.CloseHandle(h1)

	// Second acquisition with same name must report already running
	h2, already2 := createSingleInstanceMutex(testMutexName)
	if !already2 {
		if h2 != 0 {
			_ = windows.CloseHandle(h2)
		}
		t.Fatalf("second creation failed to detect already running instance")
	}
	if h2 != 0 {
		_ = windows.CloseHandle(h2)
		t.Fatalf("second creation returned non-zero handle: %v", h2)
	}
}

func TestSingleInstanceMutexReleaseAndReacquire(t *testing.T) {
	testMutexName := "Local\\NeoBox-Test-Mutex-Release"

	h1, already := createSingleInstanceMutex(testMutexName)
	if already || h1 == 0 {
		t.Fatalf("initial creation failed: already=%v, h1=%v", already, h1)
	}

	// Close handle
	_ = windows.CloseHandle(h1)

	// After closing, we should be able to acquire again
	h2, alreadyAfter := createSingleInstanceMutex(testMutexName)
	if alreadyAfter {
		t.Fatalf("creation after close reported already running")
	}
	if h2 == 0 {
		t.Fatalf("creation after close returned handle 0")
	}
	_ = windows.CloseHandle(h2)
}

func TestIsMutexExisting(t *testing.T) {
	testMutexName := "Local\\NeoBox-Test-Mutex-Existing"
	if isMutexExisting(testMutexName) {
		t.Fatalf("expected mutex to not exist initially")
	}

	h, already := createSingleInstanceMutex(testMutexName)
	if already || h == 0 {
		t.Fatalf("failed to create test mutex: %v", h)
	}
	defer windows.CloseHandle(h)

	if !isMutexExisting(testMutexName) {
		t.Fatalf("expected isMutexExisting to return true for active mutex")
	}
}
