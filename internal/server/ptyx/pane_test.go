package ptyx_test

import (
	"bytes"
	"io"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/server/ptyx"
)

// testGrace is for teardown that is not itself the assertion: the three
// spawn tests below, and TestKillIsIdempotent in reap_test.go (same
// package). /bin/sh ignores SIGTERM, so Kill's waitForExit(grace) burns
// the full window every time; at 2s those four cost ~2.05s each while
// asserting nothing about the grace.
//
// The three reap-contract tests in reap_test.go keep their own graces
// instead -- TestKillReapsEscapedGrandchild and
// TestKillReapsSIGTERMIgnoringEscapee because the escapee needs a real
// window, and TestKillEscalatesEvenWhenRootExitsWithinGrace because its
// subject requires a grace longer than the root's own exit.
const testGrace = 100 * time.Millisecond

func TestSpawnRunsCommandAndEchoesOutput(t *testing.T) {
	p, err := ptyx.Spawn([]string{"/bin/sh"}, 40, 10, t.TempDir())
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	// Kill, not Close: Close only drops the master and leaves the
	// child to be reaped by the kernel's HUP, which these tests then
	// depend on. Kill is the teardown path the rest of the program
	// uses, and it reports a tree that survived.
	t.Cleanup(func() {
		if err := p.Kill(testGrace); err != nil {
			t.Errorf("Kill: %v", err)
		}
	})

	if _, err := io.WriteString(p.Master, "echo wideboi-ok\n"); err != nil {
		t.Fatalf("write to pty: %v", err)
	}

	if !readUntil(t, p.Master, "wideboi-ok", 5*time.Second) {
		t.Fatal("never saw command output on the pty")
	}
}

func TestSpawnReportsWindowSizeToChild(t *testing.T) {
	p, err := ptyx.Spawn([]string{"/bin/sh"}, 73, 11, t.TempDir())
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	// Kill, not Close: Close only drops the master and leaves the
	// child to be reaped by the kernel's HUP, which these tests then
	// depend on. Kill is the teardown path the rest of the program
	// uses, and it reports a tree that survived.
	t.Cleanup(func() {
		if err := p.Kill(testGrace); err != nil {
			t.Errorf("Kill: %v", err)
		}
	})

	if _, err := io.WriteString(p.Master, "stty size\n"); err != nil {
		t.Fatalf("write to pty: %v", err)
	}

	// stty size prints "rows cols".
	if !readUntil(t, p.Master, "11 73", 5*time.Second) {
		t.Fatal("child did not see the requested window size")
	}
}

func TestSpawnPutsChildInItsOwnProcessGroup(t *testing.T) {
	p, err := ptyx.Spawn([]string{"/bin/sh"}, 40, 10, t.TempDir())
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	// Kill, not Close: Close only drops the master and leaves the
	// child to be reaped by the kernel's HUP, which these tests then
	// depend on. Kill is the teardown path the rest of the program
	// uses, and it reports a tree that survived.
	t.Cleanup(func() {
		if err := p.Kill(testGrace); err != nil {
			t.Errorf("Kill: %v", err)
		}
	})

	if p.PGID == 0 {
		t.Fatal("PGID is zero")
	}
	if p.PGID != p.Cmd.Process.Pid {
		t.Fatalf("PGID = %d, want it to equal child pid %d", p.PGID, p.Cmd.Process.Pid)
	}
}

func readUntil(t *testing.T, r io.Reader, want string, within time.Duration) bool {
	t.Helper()
	found := make(chan bool, 1)

	go func() {
		var acc bytes.Buffer
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				acc.Write(buf[:n])
				if bytes.Contains(acc.Bytes(), []byte(want)) {
					found <- true
					return
				}
			}
			if err != nil {
				found <- false
				return
			}
		}
	}()

	select {
	case ok := <-found:
		return ok
	case <-time.After(within):
		return false
	}
}
