package linux

import "os"

type LifecycleManager struct{}

func (l *LifecycleManager) HideConsoleIfNeeded() {}

func (l *LifecycleManager) TerminateProcess(code int) {
	os.Exit(code)
}
