package ptyx_test

import (
	"bytes"
	"io"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/server/ptyx"
)

func TestSpawnRunsCommandAndEchoesOutput(t *testing.T) {
	p, err := ptyx.Spawn([]string{"/bin/sh"}, 40, 10, t.TempDir())
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })

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
	t.Cleanup(func() { _ = p.Close() })

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
	t.Cleanup(func() { _ = p.Close() })

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
