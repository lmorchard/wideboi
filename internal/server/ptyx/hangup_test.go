package ptyx_test

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/server/ptyx"
)

// The flipped test: teardown is the hangup, and a job that opted out
// of it survives by design. If this fails, something is reaping
// again -- read docs/LESSONS.md's Teardown section before "fixing" it.
func TestHangupLeavesANohupJobRunning(t *testing.T) {
	p, err := ptyx.Spawn([]string{"/bin/sh"}, 40, 10, t.TempDir())
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	tag := fmt.Sprintf("wideboi-nohup-%d", time.Now().UnixNano())
	cmd := fmt.Sprintf("%s && nohup ./%s 300 >/dev/null 2>&1 &\n", linkSleepAs(tag), tag)
	if _, err := io.WriteString(p.Master, cmd); err != nil {
		t.Fatalf("write to pty: %v", err)
	}
	if !waitForProcess(t, tag, true, 5*time.Second) {
		t.Fatal("nohup'd job never started")
	}
	t.Cleanup(func() { _ = exec.Command("pkill", "-f", tag).Run() })

	p.Hangup(2 * time.Second)

	select {
	case <-p.Done():
	default:
		t.Error("the shell outlived its terminal's hangup")
	}
	if !processExists(tag) {
		t.Fatal("a nohup'd job did not survive the hangup -- something is reaping again")
	}
}

func TestHangupEndsAnInteractiveShell(t *testing.T) {
	p, err := ptyx.Spawn([]string{"/bin/sh"}, 40, 10, t.TempDir())
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	p.Hangup(2 * time.Second)
	select {
	case <-p.Done():
	default:
		t.Fatal("an interactive shell outlived its terminal's hangup")
	}
}

func TestHangupIsIdempotent(t *testing.T) {
	p, err := ptyx.Spawn([]string{"/bin/sh"}, 40, 10, t.TempDir())
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	p.Hangup(testGrace)
	start := time.Now()
	p.Hangup(2 * time.Second)
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("second Hangup took %v; want an immediate return", d)
	}
}

// linkSleepAs returns a shell command that symlinks sleep into the
// pane's cwd under a unique name, so the planted job is findable in ps
// by that name.
//
// The obvious spelling is `exec -a TAG sleep 300`. `exec -a` is a bash
// builtin, not POSIX: on Ubuntu /bin/sh is dash, which answers
// `exec: -a: not found`. macOS /bin/sh is bash, which is why that
// survived until CI ran on Linux.
//
// A symlink rather than a copy: copying a system binary on macOS
// breaks its code signature and the kernel SIGKILLs it immediately
// ("Killed: 9"). A symlink keeps the signature, works in any POSIX
// shell, and still gives ps a distinct argv[0].
func linkSleepAs(tag string) string {
	return fmt.Sprintf("ln -s \"$(command -v sleep)\" ./%s", tag)
}

// waitForProcess polls the real process table until a process matching
// tag is present (want=true) or absent (want=false).
func waitForProcess(t *testing.T, tag string, want bool, within time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if processExists(tag) == want {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func processExists(tag string) bool {
	out, err := exec.Command("ps", "-axo", "command").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, tag) && !strings.Contains(line, "ps -axo") {
			return true
		}
	}
	return false
}
