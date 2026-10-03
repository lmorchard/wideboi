package term

import (
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/protocol"
)

func TestIsWorkingTitle(t *testing.T) {
	tests := []struct {
		name  string
		title string
		want  bool
	}{
		{"empty", "", false},
		{"normal text", "bash - /home/user", false},
		{"claude braille thinking", "⠋ Thinking...", true},
		{"claude braille running", "⠙ Running tool...", true},
		{"arc spinner 1", "◐ Updating index", true},
		{"arc spinner 2", "◒ Syncing", true},
		{"plain tool name", "claude code", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isWorkingTitle(tt.title); got != tt.want {
				t.Errorf("isWorkingTitle(%q) = %v, want %v", tt.title, got, tt.want)
			}
		})
	}
}

func TestIsBlockerTitle(t *testing.T) {
	tests := []struct {
		name  string
		title string
		want  bool
	}{
		{"empty", "", false},
		{"action required", "Codex - Action Required", true},
		{"action required lowercase", "action required: approve file edit", true},
		{"other title", "Codex - Working", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBlockerTitle(tt.title); got != tt.want {
				t.Errorf("isBlockerTitle(%q) = %v, want %v", tt.title, got, tt.want)
			}
		})
	}
}

func TestScanScreenTail(t *testing.T) {
	tests := []struct {
		name        string
		lines       []string
		wantStatus  protocol.PaneStatus
		wantMatched bool
	}{
		{
			name:        "empty",
			lines:       nil,
			wantStatus:  protocol.StatusIdle,
			wantMatched: false,
		},
		{
			name: "plain shell idle",
			lines: []string{
				"total 4",
				"-rw-r--r-- 1 user user 10 Oct 3 12:00 file.txt",
				"user@host:~/project$ ",
			},
			wantStatus:  protocol.StatusIdle,
			wantMatched: false,
		},
		{
			name: "opencode permission required",
			lines: []string{
				"│ Checking system state...",
				"│ △ Permission required",
				"│ Allow executing bash command 'rm -rf tmp'?",
				"└ esc dismiss · enter confirm",
			},
			wantStatus:  protocol.StatusNeedsInput,
			wantMatched: true,
		},
		{
			name: "claude code permission prompt",
			lines: []string{
				"Tool: bash",
				"Command: make test",
				"Do you want to run this command?",
				"  1) Yes",
				"  2) No",
				"esc to cancel · enter to confirm",
			},
			wantStatus:  protocol.StatusNeedsInput,
			wantMatched: true,
		},
		{
			name: "claude code waiting for permission",
			lines: []string{
				"I'll need to read the config file first.",
				"waiting for permission to access ~/.config/app...",
			},
			wantStatus:  protocol.StatusNeedsInput,
			wantMatched: true,
		},
		{
			name: "codex trust directory",
			lines: []string{
				"Do you trust the contents of this directory?",
				"Trust and continue [Enter]",
			},
			wantStatus:  protocol.StatusNeedsInput,
			wantMatched: true,
		},
		{
			name: "generic confirmation prompt y/n",
			lines: []string{
				"Building packages...",
				"File /etc/config exists. Overwrite? [y/N]: ",
			},
			wantStatus:  protocol.StatusNeedsInput,
			wantMatched: true,
		},
		{
			name: "generic confirmation prompt (y/n)",
			lines: []string{
				"Install dependencies? (y/n) ",
			},
			wantStatus:  protocol.StatusNeedsInput,
			wantMatched: true,
		},
		{
			name: "active working interrupt hint",
			lines: []string{
				"Running test suite...",
				"=== RUN   TestEverything",
				"esc to interrupt",
			},
			wantStatus:  protocol.StatusWorking,
			wantMatched: true,
		},
		{
			name: "active working progress bar",
			lines: []string{
				"Downloading assets...",
				"■■■■■■■■■■⬝⬝⬝⬝⬝ 65%",
			},
			wantStatus:  protocol.StatusWorking,
			wantMatched: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotStatus, gotMatched := scanScreenTail(tt.lines)
			if gotStatus != tt.wantStatus || gotMatched != tt.wantMatched {
				t.Errorf("scanScreenTail() = (%v, %v), want (%v, %v)", gotStatus, gotMatched, tt.wantStatus, tt.wantMatched)
			}
		})
	}
}

func waitStatusWithCeiling(t *testing.T, g Grid, want protocol.PaneStatus, ceiling time.Duration) {
	t.Helper()
	deadline := time.Now().Add(ceiling)
	for time.Now().Before(deadline) {
		if g.Status() == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for Status() = %v; got %v", want, g.Status())
}

func TestGridStatusHeuristicBlockerPrompts(t *testing.T) {
	const idle = 100 * time.Millisecond
	g := NewVTWithIdleTimeout(80, 24, idle)
	defer g.Close()

	// 1. Initial status is idle before any writes
	if got := g.Status(); got != protocol.StatusIdle {
		t.Fatalf("initial Status() = %v, want %v", got, protocol.StatusIdle)
	}

	// 2. Write Claude Code permission prompt
	prompt := "I want to run `npm test`.\r\nDo you want to proceed?\r\n  1) Yes\r\n  2) No\r\nesc to cancel · enter to confirm\r\n"
	if _, err := g.Write([]byte(prompt)); err != nil {
		t.Fatal(err)
	}

	// Immediately after write: StatusWorking
	if got := g.Status(); got != protocol.StatusWorking {
		t.Fatalf("Status() immediately after write = %v, want %v", got, protocol.StatusWorking)
	}

	// Wait for debounce window (idle/2 = 50ms) with a ceiling: should become StatusNeedsInput
	waitStatusWithCeiling(t, g, protocol.StatusNeedsInput, 1*time.Second)

	// Even past the idle timeout, blocker holds StatusNeedsInput rather than decaying to Idle
	time.Sleep(120 * time.Millisecond)
	if got := g.Status(); got != protocol.StatusNeedsInput {
		t.Errorf("Status() past idle timeout = %v, want %v", got, protocol.StatusNeedsInput)
	}

	// 3. User responds: new write resets to StatusWorking
	if _, err := g.Write([]byte("\r\nExecuting tests...\r\nTests passed.\r\n$ ")); err != nil {
		t.Fatal(err)
	}
	if got := g.Status(); got != protocol.StatusWorking {
		t.Fatalf("Status() immediately after user response write = %v, want %v", got, protocol.StatusWorking)
	}

	// 4. After debounce and idle timeout, it must decay to StatusIdle rather than
	// reactivating StatusNeedsInput from the historical prompt lines above the current output.
	waitStatusWithCeiling(t, g, protocol.StatusIdle, 1*time.Second)
}

func TestGridStatusHeuristicTitleSpinner(t *testing.T) {
	const idle = 80 * time.Millisecond
	g := NewVTWithIdleTimeout(80, 24, idle)
	defer g.Close()

	// Write title with Braille spinner
	if _, err := g.Write([]byte("\x1b]0;⠋ Thinking...\x07")); err != nil {
		t.Fatal(err)
	}

	// Wait past idle timeout: because title has a spinner, it must remain StatusWorking
	time.Sleep(120 * time.Millisecond)
	if got := g.Status(); got != protocol.StatusWorking {
		t.Errorf("Status() with active title spinner = %v, want %v", got, protocol.StatusWorking)
	}

	// Title changes to plain title without spinner
	if _, err := g.Write([]byte("\x1b]0;bash\x07")); err != nil {
		t.Fatal(err)
	}
	// Wait past idle timeout: should decay to StatusIdle
	waitStatusWithCeiling(t, g, protocol.StatusIdle, 1*time.Second)
}

func TestGridStatusHeuristicGenericConfirm(t *testing.T) {
	const idle = 80 * time.Millisecond
	g := NewVTWithIdleTimeout(80, 24, idle)
	defer g.Close()

	if _, err := g.Write([]byte("Do you want to continue? [y/N] ")); err != nil {
		t.Fatal(err)
	}

	waitStatusWithCeiling(t, g, protocol.StatusNeedsInput, 1*time.Second)
}

func TestGridStatusHeuristicPlainDecaysToIdle(t *testing.T) {
	const idle = 80 * time.Millisecond
	g := NewVTWithIdleTimeout(80, 24, idle)
	defer g.Close()

	if _, err := g.Write([]byte("Hello world\r\n$ ")); err != nil {
		t.Fatal(err)
	}

	// Immediately after write: StatusWorking
	if got := g.Status(); got != protocol.StatusWorking {
		t.Fatalf("Status() immediately after write = %v, want %v", got, protocol.StatusWorking)
	}

	// Decays to StatusIdle within the ceiling
	waitStatusWithCeiling(t, g, protocol.StatusIdle, 1*time.Second)
}

func TestWorkingInactivityTimeout(t *testing.T) {
	const idle = 50 * time.Millisecond
	const inactivity = 100 * time.Millisecond
	g := NewVTWithTimeouts(80, 24, idle, inactivity)
	defer g.Close()

	// Write title spinner so heuristic holds StatusWorking past idleTimeout
	if _, err := g.Write([]byte("\x1b]0;⠋ Thinking...\x07")); err != nil {
		t.Fatal(err)
	}

	// Within inactivity timeout: StatusWorking
	if got := g.Status(); got != protocol.StatusWorking {
		t.Fatalf("Status() before inactivity timeout = %v, want %v", got, protocol.StatusWorking)
	}

	// After inactivity timeout: should become StatusInterrupted
	waitStatusWithCeiling(t, g, protocol.StatusInterrupted, 1*time.Second)
}

func TestWorkingInactivityAutoResume(t *testing.T) {
	const idle = 50 * time.Millisecond
	const inactivity = 100 * time.Millisecond
	g := NewVTWithTimeouts(80, 24, idle, inactivity)
	defer g.Close()

	// Write title spinner to hold StatusWorking
	if _, err := g.Write([]byte("\x1b]0;⠋ Thinking...\x07")); err != nil {
		t.Fatal(err)
	}
	waitStatusWithCeiling(t, g, protocol.StatusInterrupted, 1*time.Second)

	// New write output arrives -> immediately auto-resumes to StatusWorking
	if _, err := g.Write([]byte("Thinking chunk 2...\r\n")); err != nil {
		t.Fatal(err)
	}
	if got := g.Status(); got != protocol.StatusWorking {
		t.Fatalf("Status() immediately after write resume = %v, want %v", got, protocol.StatusWorking)
	}
}

func TestAuthoritativeWorkingInactivity(t *testing.T) {
	const idle = 50 * time.Millisecond
	const inactivity = 100 * time.Millisecond
	g := NewVTWithTimeouts(80, 24, idle, inactivity)
	defer g.Close()

	// Authoritative OSC 9;4;3 progress busy sequence
	if _, err := g.Write([]byte("\x1b]9;4;3;\x07")); err != nil {
		t.Fatal(err)
	}
	if got := g.Status(); got != protocol.StatusWorking {
		t.Fatalf("Status() after OSC 9;4;3 = %v, want %v", got, protocol.StatusWorking)
	}

	// Even with sawAuthoritativeStatus, inactivity timeout flags StatusInterrupted
	waitStatusWithCeiling(t, g, protocol.StatusInterrupted, 1*time.Second)

	// Resumes working when output resumes
	if _, err := g.Write([]byte("more progress\r\n")); err != nil {
		t.Fatal(err)
	}
	if got := g.Status(); got != protocol.StatusWorking {
		t.Fatalf("Status() after resume = %v, want %v", got, protocol.StatusWorking)
	}
}

func TestRestoredWorkingPaneInactivity(t *testing.T) {
	const idle = 50 * time.Millisecond
	const inactivity = 100 * time.Millisecond
	g1 := NewVTWithTimeouts(80, 24, idle, inactivity)
	defer g1.Close()

	if _, err := g1.Write([]byte("\x1b]0;⠋ Thinking...\x07start working\r\n")); err != nil {
		t.Fatal(err)
	}

	snap := g1.(Snapshotter).ExportSnapshot()
	if snap.Status != int32(protocol.StatusWorking) {
		t.Fatalf("snap.Status = %v, want Working", snap.Status)
	}

	g2 := NewVTWithTimeouts(80, 24, idle, inactivity)
	defer g2.Close()
	g2.(Snapshotter).RestoreSnapshot(snap)

	// Immediately after restore: StatusWorking
	if got := g2.Status(); got != protocol.StatusWorking {
		t.Fatalf("Status() immediately after restore = %v, want %v", got, protocol.StatusWorking)
	}

	// Because lastWriteTime was seeded, it must decay to StatusInterrupted after inactivity timeout
	waitStatusWithCeiling(t, g2, protocol.StatusInterrupted, 1*time.Second)
}
