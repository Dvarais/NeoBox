//go:build windows

package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// WebView2 leaves its process tree behind when the host goes away, and nothing
// ever collects it.
//
// The renderer does not run inside NeoBox. WebView2 spawns msedgewebview2.exe as
// a child, and that browser process spawns its own: a GPU process, a renderer,
// utilities, a crash handler. They are only torn down when the host closes the
// WebView2 controller — which does not happen when the host exits, and cannot
// happen at all on the paths that end the process outright: the elevated
// relaunch, and ExitNow.
//
// What is left is an orphan tree that outlives every session that created it.
// On the machine this was diagnosed on, one such tree — parented to a NeoBox
// process that had exited an hour and a half earlier for the elevation relaunch
// — was still running, and its GPU process had burned 1,771 seconds of CPU.
//
// # Why the next launch inherits it
//
// WebView2 keeps one browser process per user data folder. A new host that
// names the same folder does not get a fresh one: it attaches to the browser
// process that is already there. So the orphan is not merely leftover load —
// the next NeoBox renders its entire interface inside it. A session that starts
// after one that wedged is drawn by the wedged renderer from the previous one,
// from its first frame, which is what a spinning cursor over a window that
// never answers actually is.
//
// That is also why the sweep below runs at startup and not only at exit. Exit
// cleanup cannot be relied on alone: a NeoBox process can end up unkillable —
// two on the diagnosed machine survived TerminateProcess, their last thread
// stuck in an uninterruptible kernel wait — and such a process never runs any
// exit path at all. Its WebView2 tree is orphaned no matter what this file
// does, so the following launch has to be the one that clears it.

// webViewProcessName is the executable WebView2 runs its browser process as.
//
// Never matched on alone. Other applications embed WebView2 too — Windows' own
// search UI does, and 23 such processes were running on the diagnosed machine
// outside NeoBox entirely — so every caller here pairs the name with a second
// test that ties the process to this application: descent from our own process,
// or the user data folder on its command line.
const webViewProcessName = "msedgewebview2.exe"

// terminateWebViewChildren ends the WebView2 processes this process spawned.
//
// It walks descendants rather than direct children: the browser process is our
// child, but the GPU and renderer processes are its children, and it is those
// that spin. Failures are reported and skipped — this runs while the process is
// on its way out, and a corpse we could not reap is not a reason to stay alive.
func terminateWebViewChildren() {
	self := uint32(os.Getpid())

	children, _, names, err := processTree()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[webview] could not enumerate processes: %v\n", err)
		return
	}

	// Breadth-first from ourselves. Only processes named msedgewebview2.exe are
	// terminated, but the walk descends through every descendant so a browser
	// process that is somehow reparented under another child is still reached.
	var killed int
	queue := []uint32{self}
	seen := map[uint32]bool{self: true}
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		for _, child := range children[parent] {
			if seen[child] {
				// A PID cycle cannot happen on a consistent snapshot, but the
				// snapshot is not guaranteed to be one.
				continue
			}
			seen[child] = true
			queue = append(queue, child)

			if !strings.EqualFold(names[child], webViewProcessName) {
				continue
			}
			if err := terminatePID(child); err != nil {
				fmt.Fprintf(os.Stderr, "[webview] could not terminate %d: %v\n", child, err)
				continue
			}
			killed++
		}
	}
	if killed > 0 {
		fmt.Printf("[webview] terminated %d leftover WebView2 process(es)\n", killed)
	}
}

// SweepOrphanedWebViews terminates WebView2 processes left over from a previous
// NeoBox session, before this one can attach to them.
//
// It must run before the window is created — see the note at the top of this
// file: WebView2 shares one browser process per user data folder, so a leftover
// is not passive, it is what the next session will render inside.
//
// Two conditions have to hold before anything is terminated, and they are what
// keeps this from reaching into another application:
//
//   - the process is msedgewebview2.exe, and
//   - its command line names our user data folder.
//
// The single-instance mutex is taken before this runs, so no other NeoBox can
// legitimately own such a process. Anything matching is therefore a corpse.
func SweepOrphanedWebViews(userDataDir string) {
	if userDataDir == "" {
		return
	}
	children, parents, names, err := processTree()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[webview] could not enumerate processes: %v\n", err)
		return
	}

	self := uint32(os.Getpid())
	selfExe := filepath.Base(os.Args[0])

	// Compare case-insensitively on a cleaned path: the folder reaches the
	// command line through WebView2 and comes back spelled its way, not ours.
	want := strings.ToLower(filepath.Clean(userDataDir))

	var roots []uint32
	for pid, name := range names {
		if pid == 0 || !strings.EqualFold(name, webViewProcessName) {
			continue
		}
		// If the parent process is alive and is a running NeoBox process other than self,
		// do not terminate its WebView2 (which would kill the active NeoBox session).
		if parentPid, hasParent := parents[pid]; hasParent && parentPid != self {
			if parentName, parentAlive := names[parentPid]; parentAlive {
				if strings.EqualFold(parentName, "NeoBox.exe") || strings.EqualFold(parentName, selfExe) {
					continue
				}
			}
		}

		cmdline, err := processCommandLine(pid)
		if err != nil {
			// Access denied on another user's process is the normal case here,
			// and it is also the answer: not ours, leave it alone.
			continue
		}
		if strings.Contains(strings.ToLower(filepath.ToSlash(cmdline)), filepath.ToSlash(want)) {
			roots = append(roots, pid)
		}
	}
	if len(roots) == 0 {
		return
	}

	// Descendants first, so a browser process cannot respawn a helper while its
	// own children are still being walked.
	killed := 0
	for _, root := range roots {
		for _, pid := range descendantsOf(children, root) {
			if terminatePID(pid) == nil {
				killed++
			}
		}
		if terminatePID(root) == nil {
			killed++
		}
	}
	fmt.Printf("[webview] cleared %d leftover WebView2 process(es) from a previous session\n", killed)
}

// descendantsOf returns every process below root, deepest last.
func descendantsOf(children map[uint32][]uint32, root uint32) []uint32 {
	var out []uint32
	seen := map[uint32]bool{root: true}
	queue := []uint32{root}
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		for _, child := range children[parent] {
			if seen[child] {
				continue
			}
			seen[child] = true
			out = append(out, child)
			queue = append(queue, child)
		}
	}
	return out
}

// processCommandLine reads another process's command line.
//
// There is no Win32 call for this; the documented route is
// NtQueryInformationProcess with ProcessCommandLineInformation, which Windows
// 8.1 added for exactly this purpose. It needs only QUERY_LIMITED_INFORMATION,
// so it works against processes this one cannot open for anything else.
func processCommandLine(pid uint32) (string, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)

	// Ask for the size first: the call returns the length in the out parameter
	// when the buffer is too small.
	var needed uint32
	status, _, _ := procNtQueryInformationProcess.Call(
		uintptr(handle), processCommandLineInformation, 0, 0, uintptr(unsafe.Pointer(&needed)))
	if needed == 0 {
		return "", fmt.Errorf("NtQueryInformationProcess reported no command line (status %#x)", status)
	}

	buf := make([]byte, needed)
	status, _, _ = procNtQueryInformationProcess.Call(
		uintptr(handle), processCommandLineInformation,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(needed), uintptr(unsafe.Pointer(&needed)))
	if status != 0 {
		return "", fmt.Errorf("NtQueryInformationProcess failed: status %#x", status)
	}

	// The buffer holds a UNICODE_STRING whose Buffer points into it.
	us := (*windows.NTUnicodeString)(unsafe.Pointer(&buf[0]))
	return windows.UTF16PtrToString(us.Buffer), nil
}

var (
	modNtdll                      = windows.NewLazySystemDLL("ntdll.dll")
	procNtQueryInformationProcess = modNtdll.NewProc("NtQueryInformationProcess")
)

// processCommandLineInformation is the ProcessInformationClass that returns a
// process's command line as a UNICODE_STRING.
const processCommandLineInformation = 60

// processTree snapshots every process on the machine, returning the children of
// each PID, the parent PID of each, and the executable name of each.
func processTree() (children map[uint32][]uint32, parents map[uint32]uint32, names map[uint32]string, err error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, nil, nil, err
	}
	defer windows.CloseHandle(snapshot)

	children = make(map[uint32][]uint32)
	parents = make(map[uint32]uint32)
	names = make(map[uint32]string)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return nil, nil, nil, err
	}
	for {
		pid := entry.ProcessID
		parentPid := entry.ParentProcessID
		children[parentPid] = append(children[parentPid], pid)
		parents[pid] = parentPid
		names[pid] = windows.UTF16ToString(entry.ExeFile[:])

		if err := windows.Process32Next(snapshot, &entry); err != nil {
			if err == windows.ERROR_NO_MORE_FILES {
				break
			}
			return nil, nil, nil, err
		}
	}
	return children, parents, names, nil
}

// terminatePID opens a process for termination and terminates it.
//
// A process that has already exited yields an error from OpenProcess, which is
// the ordinary case during shutdown and not worth reporting: the caller only
// wanted it gone.
func terminatePID(pid uint32) error {
	handle, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		if err == windows.ERROR_INVALID_PARAMETER {
			return nil // already gone
		}
		return err
	}
	defer windows.CloseHandle(handle)
	return windows.TerminateProcess(handle, 0)
}
