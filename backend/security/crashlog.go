//go:build windows

package security

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

// crashLogFile keeps the crash log open for the lifetime of the process.
// It must stay reachable: *os.File carries a finalizer that closes the
// underlying Windows handle once the value becomes unreachable, which would
// silently break the redirection installed by InitCrashLog.
var crashLogFile *os.File

// maxCrashLogSize caps the log at 1 MiB. On overflow the current file is rotated
// to <name>.old, so a crash loop can never fill up the user's disk while the
// previous session's trace still survives one rotation.
const maxCrashLogSize = 1 << 20

// InitCrashLog redirects the process's standard error and standard output into
// path, so that a Go runtime panic leaves a stack trace on disk.
//
// Why this is needed: NeoBox runs the sing-box core in-process (see
// core.CoreManager), so a panic in ANY core goroutine terminates the whole
// application — recover() cannot cross goroutines, so such a panic can never be
// caught, only recorded. On top of that the console is detached at startup
// (HideConsoleIfNeeded), which leaves the runtime writing its trace to a handle
// that points nowhere. The window simply vanishes, and the Windows event log
// stays empty too because Go crashes do not raise WER reports. This function is
// what turns those invisible deaths into a readable file.
//
// The redirection is installed at the Win32 level rather than only on the os
// package: the Go runtime resolves GetStdHandle(STD_ERROR_HANDLE) on every write
// to fd 2 (runtime.write1), so replacing that handle also captures the panic
// output emitted by the runtime itself during a crash — the output we cannot
// obtain any other way.
//
// If the process shares a console with a parent shell (NeoBox started from a
// terminal for debugging), nothing is redirected and output stays visible there.
func InitCrashLog(path string) error {
	if consoleProcessCount() >= 2 {
		// Launched from an existing terminal — keep the output in front of the
		// developer instead of hiding it in a file.
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create crash log directory: %w", err)
	}

	if info, err := os.Stat(path); err == nil && info.Size() > maxCrashLogSize {
		_ = os.Remove(path + ".old")
		_ = os.Rename(path, path+".old")
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("failed to open crash log: %w", err)
	}

	handle := windows.Handle(f.Fd())
	if err := windows.SetStdHandle(windows.STD_ERROR_HANDLE, handle); err != nil {
		_ = f.Close()
		return fmt.Errorf("failed to redirect stderr: %w", err)
	}
	// Best-effort for stdout: diagnostics printed with fmt.Print* are useful
	// context around a crash, but losing them is not worth failing startup over.
	_ = windows.SetStdHandle(windows.STD_OUTPUT_HANDLE, handle)

	os.Stderr = f
	os.Stdout = f
	crashLogFile = f

	fmt.Fprintf(f, "\n=== NeoBox session started %s (pid %d) ===\n",
		time.Now().Format("2006-01-02 15:04:05"), os.Getpid())
	return nil
}

// MarkCleanExit records that the process is terminating on purpose. Its absence
// at the end of a session block is precisely what identifies a crash: a session
// that starts and never reports an exit died from a panic (or was killed), and
// any stack trace between the two markers belongs to that session.
func MarkCleanExit(reason string) {
	if crashLogFile == nil {
		return
	}
	fmt.Fprintf(crashLogFile, "=== NeoBox exited cleanly (%s) %s ===\n",
		reason, time.Now().Format("2006-01-02 15:04:05"))
	_ = crashLogFile.Sync()
}

// CrashLogPath returns the standard location of the crash log inside the user
// data directory, so callers do not have to agree on the layout separately.
func CrashLogPath(userDataDir string) string {
	return filepath.Join(userDataDir, "logs", "crash.log")
}
