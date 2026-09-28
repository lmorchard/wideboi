package ptyx_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/server/ptyx"
)

// testGrace bounds teardown that is not itself the assertion. A shell
// exits promptly on hangup, so this rarely burns the full window.
const testGrace = 100 * time.Millisecond

func TestSpawnRunsCommandAndEchoesOutput(t *testing.T) {
	p, err := ptyx.Spawn([]string{"/bin/sh"}, 40, 10, t.TempDir())
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	t.Cleanup(func() { p.Hangup(testGrace) })

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
	t.Cleanup(func() { p.Hangup(testGrace) })

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
	t.Cleanup(func() { p.Hangup(testGrace) })

	// Setsid makes the child a session and group leader, which is what
	// gives it the pty as its controlling terminal -- and so what the
	// hangup reaches.
	pid := p.Cmd.Process.Pid
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		t.Fatalf("Getpgid: %v", err)
	}
	if pgid != pid {
		t.Fatalf("pgid = %d, want it to equal child pid %d", pgid, pid)
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
	t.Cleanup(func() { p.Hangup(testGrace) })

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

func TestWriteBoundedConcurrent(t *testing.T) {
	p, err := ptyx.Spawn([]string{"/bin/cat"}, 40, 10, t.TempDir())
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	t.Cleanup(func() { p.Hangup(testGrace) })

	const lines = 20
	errs := make(chan error, lines)

	// Reader goroutine drains p.Master and records complete lines until all are seen.
	readDone := make(chan struct{}, 1)
	seen := make(map[string]int)
	var seenMu sync.Mutex

	go func() {
		buf := make([]byte, 1024)
		var cur []byte
		for {
			n, err := p.Master.Read(buf)
			if n > 0 {
				seenMu.Lock()
				for _, b := range buf[:n] {
					if b == '\n' || b == '\r' {
						if len(cur) > 0 {
							seen[string(cur)]++
							cur = cur[:0]
						}
					} else {
						cur = append(cur, b)
					}
				}
				if len(seen) >= lines {
					seenMu.Unlock()
					readDone <- struct{}{}
					return
				}
				seenMu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()

	var wg sync.WaitGroup
	for i := 0; i < lines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			data := []byte(fmt.Sprintf("line-%02d\n", idx))
			n, err := p.WriteBounded(data, 2*time.Second)
			if err != nil {
				errs <- fmt.Errorf("write %d failed: %w", idx, err)
			} else if n != len(data) {
				errs <- fmt.Errorf("write %d short: %d != %d", idx, n, len(data))
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent write: %v", err)
	}

	select {
	case <-readDone:
		seenMu.Lock()
		defer seenMu.Unlock()
		for i := 0; i < lines; i++ {
			want := fmt.Sprintf("line-%02d", i)
			if count := seen[want]; count == 0 {
				t.Errorf("line %q was corrupted or missing (seen=%v)", want, seen)
			}
		}
	case <-time.After(5 * time.Second):
		seenMu.Lock()
		defer seenMu.Unlock()
		t.Fatalf("timed out waiting for pty output drain (seen %d/%d lines: %v)", len(seen), lines, seen)
	}
}

// TestExitCode pins the status a shell would report: the child's exit
// code, 128+signal for a signal death, and nothing before the reap.
func TestExitCode(t *testing.T) {
	cases := []struct {
		script string
		want   int
	}{
		{"exit 0", 0},
		{"exit 3", 3},
		{"kill -TERM $$", 128 + int(syscall.SIGTERM)},
	}
	for _, c := range cases {
		p, err := ptyx.Spawn([]string{"/bin/sh", "-c", c.script}, 40, 10, t.TempDir())
		if err != nil {
			t.Fatalf("Spawn(%q): %v", c.script, err)
		}
		select {
		case <-p.Done():
		case <-time.After(5 * time.Second):
			t.Fatalf("%q: child never reaped", c.script)
		}
		code, reaped := p.ExitCode()
		if !reaped || code != c.want {
			t.Errorf("%q: ExitCode() = (%d, %v), want (%d, true)", c.script, code, reaped, c.want)
		}
		p.Hangup(testGrace)
	}

	p, err := ptyx.Spawn([]string{"/bin/sh", "-c", "read x"}, 40, 10, t.TempDir())
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if _, reaped := p.ExitCode(); reaped {
		t.Error("ExitCode() reported reaped for a child still blocked in read")
	}
	p.Hangup(2 * time.Second)
}

// The reaped status in words, for logs: a signal death names the signal
// rather than hiding it in a number.
func TestExitDescription(t *testing.T) {
	cases := []struct{ script, want string }{
		{"exit 3", "exit status 3"},
		{"kill -KILL $$", "signal: killed"},
	}
	for _, c := range cases {
		p, err := ptyx.Spawn([]string{"/bin/sh", "-c", c.script}, 40, 10, t.TempDir())
		if err != nil {
			t.Fatalf("Spawn(%q): %v", c.script, err)
		}
		select {
		case <-p.Done():
		case <-time.After(5 * time.Second):
			t.Fatalf("%q: child never reaped", c.script)
		}
		got, reaped := p.ExitDescription()
		if !reaped || got != c.want {
			t.Errorf("%q: ExitDescription() = (%q, %v), want (%q, true)", c.script, got, reaped, c.want)
		}
		p.Hangup(testGrace)
	}
}

func TestPaneResize(t *testing.T) {
	p, err := ptyx.Spawn([]string{"/bin/sh"}, 40, 10, t.TempDir())
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	t.Cleanup(func() { p.Hangup(testGrace) })

	if err := p.Resize(85, 23); err != nil {
		t.Fatalf("Resize: %v", err)
	}

	if _, err := io.WriteString(p.Master, "stty size\n"); err != nil {
		t.Fatalf("write to pty: %v", err)
	}

	if !readUntil(t, p.Master, "23 85", 5*time.Second) {
		t.Fatal("child did not see resized window size 23 85")
	}
}

func TestPaneClose(t *testing.T) {
	p, err := ptyx.Spawn([]string{"/bin/sh"}, 40, 10, t.TempDir())
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	t.Cleanup(func() { p.Hangup(testGrace) })

	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := p.Resize(50, 15); err == nil {
		t.Error("Resize on closed pane master succeeded, want error")
	}
}

func TestPanePID(t *testing.T) {
	p, err := ptyx.Spawn([]string{"/bin/sh"}, 40, 10, t.TempDir())
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	t.Cleanup(func() { p.Hangup(testGrace) })

	if p.PID() <= 0 {
		t.Errorf("PID() = %d, want > 0", p.PID())
	}
}

func TestAdoptAlreadyExited(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	p, err := ptyx.Adopt(os.Getpid(), int(w.Fd()), "test-pty", true, 42)
	if err != nil {
		w.Close()
		t.Fatalf("Adopt: %v", err)
	}
	defer p.Close()

	select {
	case <-p.Done():
	default:
		t.Fatal("Done() not closed for alreadyExited pane")
	}

	code, reaped := p.ExitCode()
	if !reaped || code != 42 {
		t.Errorf("ExitCode() = (%d, %v), want (42, true)", code, reaped)
	}

	desc, reaped := p.ExitDescription()
	if !reaped || !strings.Contains(desc, "42") {
		t.Errorf("ExitDescription() = (%q, %v), want contains 42", desc, reaped)
	}
}

func TestAdoptRunning(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "sleep 0.05; exit 9")
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	pAdopted, err := ptyx.Adopt(cmd.Process.Pid, int(w.Fd()), "adopted-pty", false, 0)
	if err != nil {
		w.Close()
		t.Fatalf("Adopt running: %v", err)
	}
	defer pAdopted.Close()

	select {
	case <-pAdopted.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for adopted process exit")
	}

	code, reaped := pAdopted.ExitCode()
	if !reaped || code != 9 {
		t.Errorf("ExitCode() = (%d, %v), want (9, true)", code, reaped)
	}
}
