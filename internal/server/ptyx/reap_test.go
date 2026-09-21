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

func TestKillReapsEscapedGrandchild(t *testing.T) {
	p, err := ptyx.Spawn([]string{"/bin/sh"}, 40, 10, t.TempDir())
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// A sleeper that escapes the pane's process group entirely. A plain
	// killpg would not reach it.
	tag := fmt.Sprintf("wideboi-escapee-%d", time.Now().UnixNano())
	cmd := fmt.Sprintf("%s && ./%s 300 &\n", linkSleepAs(tag), tag)
	if _, err := io.WriteString(p.Master, cmd); err != nil {
		t.Fatalf("write to pty: %v", err)
	}

	if !waitForProcess(t, tag, true, 5*time.Second) {
		t.Fatal("escapee never started")
	}

	if err := p.Kill(2 * time.Second); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	if !waitForProcess(t, tag, false, 5*time.Second) {
		t.Fatal("escapee survived Kill — the pane leaked a process")
	}
}

// TestKillReapsSIGTERMIgnoringEscapee covers a descendant that both
// escaped the process group and ignores SIGTERM. The interactive root
// shell here also ignores SIGTERM (see TestKillIsIdempotent's timing),
// so this exercises the ordinary grace-timeout escalation path: it does
// not, by itself, distinguish that path from the unconditional one
// TestKillEscalatesEvenWhenRootExitsWithinGrace covers below.
func TestKillReapsSIGTERMIgnoringEscapee(t *testing.T) {
	p, err := ptyx.Spawn([]string{"/bin/sh"}, 40, 10, t.TempDir())
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// An escapee that both leaves the pane's process group and ignores
	// SIGTERM. SIG_IGN dispositions survive exec, so the renamed sleep
	// keeps ignoring TERM after the trap'd subshell execs into it.
	tag := fmt.Sprintf("wideboi-escapee-%d", time.Now().UnixNano())
	cmd := fmt.Sprintf("%s && sh -c 'trap \"\" TERM; exec ./%s 300' &\n", linkSleepAs(tag), tag)
	if _, err := io.WriteString(p.Master, cmd); err != nil {
		t.Fatalf("write to pty: %v", err)
	}

	if !waitForProcess(t, tag, true, 5*time.Second) {
		t.Fatal("escapee never started")
	}

	if err := p.Kill(500 * time.Millisecond); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	if !waitForProcess(t, tag, false, 5*time.Second) {
		t.Fatal("SIGTERM-ignoring escapee survived Kill — SIGKILL escalation never reached it")
	}
}

// TestKillEscalatesEvenWhenRootExitsWithinGrace covers Finding 1's actual
// discriminating precondition: the root itself exits within grace. Under
// the pre-fix logic, Kill returned as soon as the root exited, so SIGKILL
// was never sent to anything at all — a descendant that ignored SIGTERM
// (and, since it stayed in the pane's own process group here, also the
// SIGHUP the kernel delivers to the foreground group when a session
// leader exits) would survive.
//
// The root is spawned non-interactively (sh -c ...) because it must die
// promptly on SIGTERM — unlike an interactive /bin/sh on a pty, which
// does not (see TestKillIsIdempotent's ~2s runtime). The escapee traps
// both TERM and HUP so that neither our own SIGTERM pass nor the
// kernel's automatic post-session-exit SIGHUP kills it "for free": the
// only thing that can still reach it is the unconditional SIGKILL pass
// against the pre-signal snapshot.
func TestKillEscalatesEvenWhenRootExitsWithinGrace(t *testing.T) {
	tag := fmt.Sprintf("wideboi-escapee-%d", time.Now().UnixNano())
	script := fmt.Sprintf(`sh -c 'trap "" TERM HUP; exec -a %s sleep 300' & sleep 60`, tag)

	p, err := ptyx.Spawn([]string{"/bin/sh", "-c", script}, 40, 10, t.TempDir())
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	if !waitForProcess(t, tag, true, 5*time.Second) {
		t.Fatal("escapee never started")
	}

	if err := p.Kill(2 * time.Second); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	if !waitForProcess(t, tag, false, 5*time.Second) {
		t.Fatal("escapee survived Kill even though the root exited within grace — " +
			"SIGKILL escalation is wrongly gated on the root's own exit")
	}
}

func TestKillIsIdempotent(t *testing.T) {
	p, err := ptyx.Spawn([]string{"/bin/sh"}, 40, 10, t.TempDir())
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if err := p.Kill(testGrace); err != nil {
		t.Fatalf("first Kill: %v", err)
	}
	if err := p.Kill(testGrace); err != nil {
		t.Fatalf("second Kill: %v", err)
	}
}

// waitForProcess polls the real process table until a process matching
// tag is present (want=true) or absent (want=false).
// linkSleepAs returns a shell command that symlinks sleep into the
// pane's cwd under a unique name, so the escapee is findable in ps by
// that name.
//
// The obvious spelling is `exec -a TAG sleep 300`, and that is what
// this used to do. `exec -a` is a bash builtin, not POSIX: on Ubuntu
// /bin/sh is dash, which answers `exec: -a: not found`, so the
// escapee never started and these tests failed on Linux during setup
// -- before reaching any of the descendant-walking they exist to
// cover. macOS /bin/sh is bash, which is why it survived until CI ran
// on Linux.
//
// A symlink rather than a copy: copying a system binary on macOS
// breaks its code signature and the kernel SIGKILLs it immediately
// ("Killed: 9"), which trades a Linux failure for a macOS one. A
// symlink keeps the signature, works in any POSIX shell, and still
// gives ps a distinct argv[0].
func linkSleepAs(tag string) string {
	return fmt.Sprintf("ln -s \"$(command -v sleep)\" ./%s", tag)
}

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
