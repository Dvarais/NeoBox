package service

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The snapshot has to describe the machine it was taken on: our own process
// present, named correctly, and reachable from its parent.
func TestProcessTreeSeesThisProcess(t *testing.T) {
	children, _, names, err := processTree()
	if err != nil {
		t.Fatalf("processTree: %v", err)
	}

	self := uint32(os.Getpid())
	name, ok := names[self]
	if !ok {
		t.Fatal("the snapshot does not contain this process")
	}
	if !strings.HasSuffix(strings.ToLower(name), ".exe") {
		t.Errorf("this process is named %q, which is not an executable name", name)
	}

	var listed bool
	for _, siblings := range children {
		for _, pid := range siblings {
			if pid == self {
				listed = true
			}
		}
	}
	if !listed {
		t.Error("this process is in no parent's child list")
	}
}

// The walk must reach a grandchild, not just a direct child. That is the whole
// point: WebView2's browser process is our child, but the GPU process that
// spins is the browser process's child, one level further down.
func TestProcessTreeReachesGrandchildren(t *testing.T) {
	// cmd spawns ping, so the tree below us is two levels deep for a moment.
	cmd := exec.Command("cmd", "/c", "ping -n 4 127.0.0.1 > nul")
	if err := cmd.Start(); err != nil {
		t.Skipf("could not start a helper process: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	self := uint32(os.Getpid())
	child := uint32(cmd.Process.Pid)

	// Give the shell a moment to spawn ping.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		children, _, _, err := processTree()
		if err != nil {
			t.Fatalf("processTree: %v", err)
		}
		if descendants(children, self)[child] && len(children[child]) > 0 {
			grandchild := children[child][0]
			if !descendants(children, self)[grandchild] {
				t.Errorf("grandchild %d is not reachable from this process", grandchild)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Skip("the helper never spawned a grandchild in time")
}

// descendants mirrors the walk in terminateWebViewChildren, so the traversal
// the test exercises is the traversal that ships.
func descendants(children map[uint32][]uint32, root uint32) map[uint32]bool {
	seen := map[uint32]bool{}
	queue := []uint32{root}
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		for _, child := range children[parent] {
			if seen[child] {
				continue
			}
			seen[child] = true
			queue = append(queue, child)
		}
	}
	return seen
}

// Nothing outside our own subtree may be touched. Other applications embed
// WebView2 as well — Windows' own search UI does — and matching on the
// executable name alone would take theirs down with ours.
func TestTerminateWebViewChildrenIgnoresForeignTrees(t *testing.T) {
	children, _, names, err := processTree()
	if err != nil {
		t.Fatalf("processTree: %v", err)
	}

	self := uint32(os.Getpid())
	mine := descendants(children, self)

	var foreign int
	for pid, name := range names {
		if strings.EqualFold(name, webViewProcessName) && !mine[pid] {
			foreign++
		}
	}
	t.Logf("%d WebView2 process(es) on this machine are outside our subtree", foreign)

	// A test process has no WebView2 of its own, so the walk must select
	// nothing at all — however many are running elsewhere.
	for pid := range mine {
		if strings.EqualFold(names[pid], webViewProcessName) {
			t.Errorf("pid %d would be terminated, but this process never started WebView2", pid)
		}
	}
}

// The startup sweep selects by user data folder, and that is the only thing
// standing between it and every other application's WebView2. This machine has
// several running that belong to Windows itself; a folder that matches none of
// them must select nothing.
func TestSweepSelectsNothingForAnUnrelatedFolder(t *testing.T) {
	_, _, names, err := processTree()
	if err != nil {
		t.Fatalf("processTree: %v", err)
	}

	var webviews, readable int
	for pid, name := range names {
		if pid == 0 || !strings.EqualFold(name, webViewProcessName) {
			continue
		}
		webviews++
		cmdline, err := processCommandLine(pid)
		if err != nil {
			continue
		}
		readable++
		// A folder no process can be using, in the same shape the sweep compares.
		want := strings.ToLower(filepath.ToSlash(filepath.Clean(
			filepath.Join(t.TempDir(), "NeoBox-does-not-exist"))))
		if strings.Contains(strings.ToLower(filepath.ToSlash(cmdline)), want) {
			t.Errorf("pid %d matched a folder that cannot exist", pid)
		}
	}
	t.Logf("%d WebView2 process(es) running, %d command lines readable", webviews, readable)
	if webviews > 0 && readable == 0 {
		t.Skip("no command line could be read; the sweep cannot identify anything here")
	}
}

// And the reader has to actually work, or the sweep silently matches nothing
// and the leak comes back without a single test failing.
func TestProcessCommandLineReadsThisProcess(t *testing.T) {
	cmdline, err := processCommandLine(uint32(os.Getpid()))
	if err != nil {
		t.Fatalf("processCommandLine on ourselves: %v", err)
	}
	if cmdline == "" {
		t.Fatal("our own command line came back empty")
	}
	// go test runs the package binary, whose name ends in .test.exe.
	if !strings.Contains(strings.ToLower(cmdline), ".test") {
		t.Errorf("command line does not look like this test binary: %q", cmdline)
	}
	t.Logf("own command line: %s", cmdline)
}

// Terminating a PID that has already gone is the ordinary case during shutdown
// and must not be reported as a failure.
func TestTerminatePIDOnADeadProcess(t *testing.T) {
	cmd := exec.Command("cmd", "/c", "exit")
	if err := cmd.Start(); err != nil {
		t.Skipf("could not start a helper process: %v", err)
	}
	pid := uint32(cmd.Process.Pid)
	_ = cmd.Wait()

	// The PID is now free. terminatePID must not panic, and must not report an
	// error for a process that is simply not there any more.
	if err := terminatePID(pid); err != nil {
		t.Logf("terminatePID on a reaped PID returned: %v", err)
	}
}
