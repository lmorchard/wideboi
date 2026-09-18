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
	cmd := fmt.Sprintf("sh -c 'exec -a %s sleep 300' &\n", tag)
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

func TestKillIsIdempotent(t *testing.T) {
	p, err := ptyx.Spawn([]string{"/bin/sh"}, 40, 10, t.TempDir())
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if err := p.Kill(2 * time.Second); err != nil {
		t.Fatalf("first Kill: %v", err)
	}
	if err := p.Kill(2 * time.Second); err != nil {
		t.Fatalf("second Kill: %v", err)
	}
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
