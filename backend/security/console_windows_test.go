//go:build windows

package security

import (
	"testing"
)

func TestHideConsoleIfNeeded_InTestEnvironment(t *testing.T) {
	// In the test environment, the console (if any) is shared with the test runner and parent processes.
	// Therefore, HideConsoleIfNeeded must return false and NOT detach.
	detached := HideConsoleIfNeeded()
	if detached {
		t.Errorf("Expected HideConsoleIfNeeded to return false in test environment, but it returned true")
	}
}
