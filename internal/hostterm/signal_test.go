package hostterm_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/hostterm"
)

// When run with WIDEBOI_SIGNAL_CHILD=1 this test body becomes the child
// process: it arms a guard, writes a marker on shutdown, and waits to be
// signalled.
func TestGuardArmRestoresAndReRaises(t *testing.T) {
	if os.Getenv("WIDEBOI_SIGNAL_CHILD") == "1" {
		marker := os.Getenv("WIDEBOI_MARKER")
		g := hostterm.NewGuard(func() error {
			return os.WriteFile(marker, []byte("restored"), 0o644)
		})
		g.Arm(syscall.SIGTERM)
		// Signal the parent that the handler is installed.
		if err := os.WriteFile(os.Getenv("WIDEBOI_READY"), []byte("ok"), 0o644); err != nil {
			t.Fatalf("write ready file: %v", err)
		}
		time.Sleep(10 * time.Second) // killed well before this
		return
	}

	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")
	ready := filepath.Join(dir, "ready")

	cmd := exec.Command(os.Args[0], "-test.run=TestGuardArmRestoresAndReRaises")
	cmd.Env = append(os.Environ(),
		"WIDEBOI_SIGNAL_CHILD=1",
		"WIDEBOI_MARKER="+marker,
		"WIDEBOI_READY="+ready,
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}

	waitForFile(t, ready, 5*time.Second)

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal child: %v", err)
	}
	err := cmd.Wait()

	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("shutdown func did not run: %v", statErr)
	}

	ee, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("child exited with %v, want ExitError", err)
	}
	ws, ok := ee.Sys().(syscall.WaitStatus)
	if !ok {
		t.Fatalf("no WaitStatus available")
	}
	if !ws.Signaled() || ws.Signal() != syscall.SIGTERM {
		t.Fatalf("child status = %v, want death by SIGTERM", ws)
	}
}

// POSIX requires a shell to set SIGINT and SIGQUIT to SIG_IGN for an
// asynchronous list, so anything started as `cmd &` -- including every
// target under `make -j` -- inherits them ignored.
//
// signal.Notify installs a Go handler over SIG_IGN, so the guard still
// catches the signal and the shutdown func still runs. But signal.Stop
// and signal.Reset then faithfully restore the *original* disposition,
// which is SIG_IGN, so the re-raise is discarded and returns. Before the
// fix the process lived forever with the terminal already restored,
// immune to every signal it had armed.
//
// SIGINT specifically, not SIGTERM: Go's runtime respects an inherited
// SIG_IGN only for SIGHUP and SIGINT (sigInstallGoHandler), and installs
// its own handler over an ignored SIGTERM. So SIGTERM cannot reproduce
// this -- Reset hands SIGTERM back as SIG_DFL and the re-raise works.
//
// The child is launched through sh with `trap "" INT` so the disposition
// is genuinely inherited across exec. signal.Ignore in-process would
// exercise Go's own bookkeeping rather than the case that bites.
func TestGuardExitsWhenTheReRaiseIsIgnored(t *testing.T) {
	if os.Getenv("WIDEBOI_SIGNAL_CHILD") == "1" {
		marker := os.Getenv("WIDEBOI_MARKER")
		g := hostterm.NewGuard(func() error {
			return os.WriteFile(marker, []byte("restored"), 0o644)
		})
		g.Arm(syscall.SIGINT)
		if err := os.WriteFile(os.Getenv("WIDEBOI_READY"), []byte("ok"), 0o644); err != nil {
			t.Fatalf("write ready file: %v", err)
		}
		time.Sleep(10 * time.Second) // killed well before this
		return
	}

	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")
	ready := filepath.Join(dir, "ready")

	cmd := exec.Command("/bin/sh", "-c",
		`trap "" INT; exec "$0" -test.run=TestGuardExitsWhenTheReRaiseIsIgnored`,
		os.Args[0])
	cmd.Env = append(os.Environ(),
		"WIDEBOI_SIGNAL_CHILD=1",
		"WIDEBOI_MARKER="+marker,
		"WIDEBOI_READY="+ready,
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}

	waitForFile(t, ready, 5*time.Second)

	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatalf("signal child: %v", err)
	}

	// Bounded: before the fix the child never exits, and a bare
	// cmd.Wait would hang the package instead of failing it.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var err error
	select {
	case err = <-done:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		t.Fatal("child never exited after SIGINT -- the re-raise was discarded " +
			"because Reset restored the inherited SIG_IGN, and nothing followed it")
	}

	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("shutdown func did not run: %v", statErr)
	}

	ee, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("child exited with %v, want ExitError", err)
	}
	ws, ok := ee.Sys().(syscall.WaitStatus)
	if !ok {
		t.Fatalf("no WaitStatus available")
	}
	// It cannot die *by* the signal here -- the disposition really is
	// SIG_IGN. The conventional 128+signo exit code is the best a
	// process can do, and is what a shell would have reported anyway.
	if ws.Signaled() {
		t.Fatalf("child died by signal %v; with SIG_IGN inherited that should be impossible", ws.Signal())
	}
	if got, want := ws.ExitStatus(), 128+int(syscall.SIGINT); got != want {
		t.Fatalf("child exit status = %d, want %d (128+SIGINT)", got, want)
	}
}

func waitForFile(t *testing.T, path string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}
