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
