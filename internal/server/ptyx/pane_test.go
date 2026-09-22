package ptyx_test

import (
	"bytes"
	"errors"
	"io"
	"os"
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

func TestWriteBoundedDeadline(t *testing.T) {
	// Spawn a command that does not read stdin.
	p, err := ptyx.Spawn([]string{"/bin/sleep", "100"}, 40, 10, t.TempDir())
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	t.Cleanup(func() { _ = p.Kill(testGrace) })

	buf := make([]byte, 1024)
	for i := range buf {
		buf[i] = 'Z'
	}
	buf[len(buf)-1] = '\n'

	// Fill the tty input buffer completely. Kernel queue sizes are OS-dependent
	// (1024 on macOS, 4096+ on Linux). On Linux, the asynchronous flush_to_ldisc
	// workqueue can drain an additional buffer after the first timeout, so wait
	// for multiple consecutive write timeouts to ensure all internal queues are
	// completely saturated and can accept 0 bytes.
	timeouts := 0
	for i := 0; i < 64 && timeouts < 3; i++ {
		n, err := p.WriteBounded(buf, 10*time.Millisecond)
		if errors.Is(err, os.ErrDeadlineExceeded) && n == 0 {
			timeouts++
		} else {
			timeouts = 0
		}
		if err != nil && !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("fill write %d: %v", i, err)
		}
	}
	if timeouts < 3 {
		t.Fatal("failed to saturate tty input buffer within 64KB")
	}

	// Once saturated, a write with 50ms deadline must time out rather than
	// blocking indefinitely.
	start := time.Now()
	n, werr := p.WriteBounded(buf, 50*time.Millisecond)
	elapsed := time.Since(start)

	if !errors.Is(werr, os.ErrDeadlineExceeded) {
		t.Fatalf("expected os.ErrDeadlineExceeded, got err=%v n=%d (elapsed=%v)", werr, n, elapsed)
	}
	if elapsed < 35*time.Millisecond || elapsed > 1*time.Second {
		t.Fatalf("write returned in %v, expected ~50ms", elapsed)
	}
}
