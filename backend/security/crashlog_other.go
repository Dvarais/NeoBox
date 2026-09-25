//go:build !windows

package security

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

var crashLogFile *os.File

func InitCrashLog(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	crashLogFile = f
	os.Stderr = f
	os.Stdout = f
	fmt.Fprintf(f, "\n=== NeoBox session started %s (pid %d) ===\n",
		time.Now().Format("2006-01-02 15:04:05"), os.Getpid())
	return nil
}

func MarkCleanExit(reason string) {
	if crashLogFile == nil {
		return
	}
	fmt.Fprintf(crashLogFile, "=== NeoBox exited cleanly (%s) %s ===\n",
		reason, time.Now().Format("2006-01-02 15:04:05"))
	_ = crashLogFile.Sync()
}

func CrashLogPath(userDataDir string) string {
	return filepath.Join(userDataDir, "logs", "crash.log")
}
