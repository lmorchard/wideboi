package term_test

import (
	"github.com/lmorchard/wideboi/internal/protocol"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/server/term"
)

// OSC 133 had never worked. The handler matched strings.HasPrefix(s,
// "A") against a payload that is actually "133;A", because
// ansi.Parser.parseStringCmd reads the leading digits into p.cmd
// without removing them from p.data. Every one of x/vt's own OSC
// handlers splits on ';' and reads parts[1] for that reason.
//
// This package had no OSC test at all, which is why a dead switch
// survived from the day it was written. These drive real bytes through
// Grid.Write, the way a child process would.
func TestOSC133DrivesPaneStatus(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    protocol.PaneStatus
	}{
		// A is prompt-start and B is prompt-end. A shell emits both
		// back to back on every prompt, so mapping B to Working would
		// clobber A microseconds later and leave an idle shell reading
		// as busy. Both mean "waiting on you".
		{"prompt start", "\x1b]133;A\x07", protocol.StatusNeedsInput},
		{"prompt end", "\x1b]133;B\x07", protocol.StatusNeedsInput},

		// C is the start of command output: now it is actually busy.
		//
		// Note this row alone does not discriminate a working handler
		// from a dead one -- Write's activity fallback sets Working
		// regardless. It is here for completeness of the mapping, not
		// as evidence. The A/B and D rows are what prove the switch.
		{"output start", "\x1b]133;C\x07", protocol.StatusWorking},

		// D is command-finished. Bare D and D;0 are success.
		{"finished, no code", "\x1b]133;D\x07", protocol.StatusDone},
		{"finished, zero", "\x1b]133;D;0\x07", protocol.StatusDone},
		{"finished, nonzero", "\x1b]133;D;1\x07", protocol.StatusFailed},
		{"finished, signal code", "\x1b]133;D;130\x07", protocol.StatusFailed},

		// Payloads may carry trailing key=value fields. The old
		// HasSuffix(s, ";0") test read this one as a failure.
		{"zero with extra params", "\x1b]133;D;0;aid=1\x07", protocol.StatusDone},
		{"prompt with extra params", "\x1b]133;A;cl=m\x07", protocol.StatusNeedsInput},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := term.NewVT(20, 5)
			defer g.Close()

			if _, err := g.Write([]byte(tc.payload)); err != nil {
				t.Fatalf("Write(%q): %v", tc.payload, err)
			}
			if got := g.Status(); got != tc.want {
				t.Errorf("Status() after %q = %v, want %v", tc.payload, got, tc.want)
			}
		})
	}
}

// OSC 9;4 is the ConEmu / Windows Terminal progress reporting protocol,
// which coding agents (such as Claude Code) emit during turns.
func TestOSC9ProgressDrivesPaneStatus(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    protocol.PaneStatus
	}{
		// 0 is clear / turn completed.
		{"turn end (clear with trailing semicolon)", "\x1b]9;4;0;\x07", protocol.StatusDone},
		{"turn end (clear bare)", "\x1b]9;4;0\x07", protocol.StatusDone},

		// 1 is normal progress with percentage.
		{"progress percentage", "\x1b]9;4;1;45\x07", protocol.StatusWorking},

		// 2 is error / failed.
		{"turn error with code", "\x1b]9;4;2;1\x07", protocol.StatusFailed},
		{"turn error bare", "\x1b]9;4;2\x07", protocol.StatusFailed},

		// 3 is indeterminate / busy (turn start in Claude Code).
		{"turn start (busy/indeterminate with trailing semicolon)", "\x1b]9;4;3;\x07", protocol.StatusWorking},
		{"turn start (busy/indeterminate bare)", "\x1b]9;4;3\x07", protocol.StatusWorking},

		// 4 is warning / paused.
		{"warning / paused with progress", "\x1b]9;4;4;50\x07", protocol.StatusNeedsInput},
		{"warning / paused bare", "\x1b]9;4;4\x07", protocol.StatusNeedsInput},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := term.NewVT(20, 5)
			defer g.Close()

			if _, err := g.Write([]byte(tc.payload)); err != nil {
				t.Fatalf("Write(%q): %v", tc.payload, err)
			}
			if got := g.Status(); got != tc.want {
				t.Errorf("Status() after %q = %v, want %v", tc.payload, got, tc.want)
			}
		})
	}
}

func TestMalformedOSC9LeavesTheIdleFallbackArmed(t *testing.T) {
	const idle = 100 * time.Millisecond
	cases := []struct {
		name    string
		payload string
	}{
		{"non-progress OSC 9", "\x1b]9;desktop notification\x07"},
		{"unrecognised state", "\x1b]9;4;Z\x07"},
		{"empty state", "\x1b]9;4;\x07"},
		{"truncated 9;4", "\x1b]9;4\x07"},
		{"state 1 without progress", "\x1b]9;4;1\x07"},
		{"state 1 with non-integer progress", "\x1b]9;4;1;abc\x07"},
		{"state 1 with negative progress", "\x1b]9;4;1;-10\x07"},
		{"state 1 with progress over 100", "\x1b]9;4;1;101\x07"},
		{"other state with invalid progress", "\x1b]9;4;2;invalid\x07"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := term.NewVTWithIdleTimeout(20, 5, idle)
			defer g.Close()

			if _, err := g.Write([]byte(tc.payload)); err != nil {
				t.Fatalf("Write: %v", err)
			}
			if got := g.Status(); got != protocol.StatusWorking {
				t.Fatalf("Status() = %v immediately after a write, want %v", got, protocol.StatusWorking)
			}

			time.Sleep(idle + 50*time.Millisecond)

			if got := g.Status(); got != protocol.StatusIdle {
				t.Errorf("Status() = %v after idle window, want %v -- payload %q latched authoritative status",
					got, protocol.StatusIdle, tc.payload)
			}
		})
	}
}

// TestValidOSC9ArmsTheAuthoritativeLatch asserts that receiving a valid OSC 9;4
// progress sequence latches authoritative status mode, permanently disabling
// the idle decay fallback.
func TestValidOSC9ArmsTheAuthoritativeLatch(t *testing.T) {
	const idle = 100 * time.Millisecond
	g := term.NewVTWithIdleTimeout(20, 5, idle)
	defer g.Close()

	if _, err := g.Write([]byte("\x1b]9;4;3;\x07")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := g.Status(); got != protocol.StatusWorking {
		t.Fatalf("Status() immediately after 9;4;3 = %v, want %v", got, protocol.StatusWorking)
	}

	// Wait past the idle timeout; authoritative mode must prevent decay to Idle.
	time.Sleep(idle + 50*time.Millisecond)

	if got := g.Status(); got != protocol.StatusWorking {
		t.Errorf("Status() after idle window = %v, want %v (latch was not armed by valid 9;4)",
			got, protocol.StatusWorking)
	}
}

func TestOSC9And133Interleaving(t *testing.T) {
	g := term.NewVT(20, 5)
	defer g.Close()

	// 1. Shell prompt (OSC 133;A) -> NeedsInput
	if _, err := g.Write([]byte("\x1b]133;A\x07")); err != nil {
		t.Fatal(err)
	}
	if got := g.Status(); got != protocol.StatusNeedsInput {
		t.Fatalf("Status() after 133;A = %v, want %v", got, protocol.StatusNeedsInput)
	}

	// 2. Agent turn starts (OSC 9;4;3) -> Working
	if _, err := g.Write([]byte("\x1b]9;4;3;\x07")); err != nil {
		t.Fatal(err)
	}
	if got := g.Status(); got != protocol.StatusWorking {
		t.Fatalf("Status() after 9;4;3; = %v, want %v", got, protocol.StatusWorking)
	}

	// 3. Agent turn ends (OSC 9;4;0) -> Done
	if _, err := g.Write([]byte("\x1b]9;4;0;\x07")); err != nil {
		t.Fatal(err)
	}
	if got := g.Status(); got != protocol.StatusDone {
		t.Fatalf("Status() after 9;4;0; = %v, want %v", got, protocol.StatusDone)
	}

	// 4. Shell next prompt (OSC 133;A) -> NeedsInput (last writer wins)
	if _, err := g.Write([]byte("\x1b]133;A\x07")); err != nil {
		t.Fatal(err)
	}
	if got := g.Status(); got != protocol.StatusNeedsInput {
		t.Fatalf("Status() after second 133;A = %v, want %v", got, protocol.StatusNeedsInput)
	}
}

// sawOSC133 gates two fallbacks: the activity heuristic in Write and
// the idle timeout in Status. It used to latch at the top of the
// handler, before any matching, so a single unrecognised 133 payload
// would switch both off permanently. Latch only on a command we
// actually understood.
//
// Observing the latch directly is awkward: right after any Write the
// status is Working either way, so the obvious assertion does not
// discriminate. Two tests below, one fast and one slow, that do.

// Fast: a malformed payload must not stop a later valid one being
// honoured. Fails against the old handler, whose switch never matched.
func TestMalformedOSC133StillAllowsALaterValidSequence(t *testing.T) {
	g := term.NewVT(20, 5)
	defer g.Close()

	if _, err := g.Write([]byte("\x1b]133;Z\x07")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := g.Write([]byte("\x1b]133;D;1\x07")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := g.Status(); got != protocol.StatusFailed {
		t.Errorf("Status() = %v after a malformed 133 then a valid D;1, want %v", got, protocol.StatusFailed)
	}
}

// Slow but exact: the only way to observe the latch itself is the
// 3-second idle timeout in Status, which is gated on sawOSC133 being
// clear. If an unrecognised payload latched, this pane would read
// Working forever instead of going Idle.
//
// The idle window is injected rather than waited out: this test is about
// whether an unrecognised payload latches sawOSC133, not about the length
// of the production timeout. At the 3s default it slept 3100ms and was
// the whole cost of this package's suite.
func TestMalformedOSC133LeavesTheIdleFallbackArmed(t *testing.T) {
	const idle = 100 * time.Millisecond
	g := term.NewVTWithIdleTimeout(20, 5, idle)
	defer g.Close()

	if _, err := g.Write([]byte("\x1b]133;Z\x07")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := g.Status(); got != protocol.StatusWorking {
		t.Fatalf("Status() = %v immediately after a write, want %v", got, protocol.StatusWorking)
	}

	time.Sleep(idle + 50*time.Millisecond)

	if got := g.Status(); got != protocol.StatusIdle {
		t.Errorf("Status() = %v after the idle window, want %v -- an unrecognised 133 payload latched sawOSC133 and disabled the idle fallback", got, protocol.StatusIdle)
	}
}

// x/vt parses OSC 0/1/2 into a title and offers a Title callback.
// NewVT registered only CursorVisibility, so the title was parsed and
// dropped on the floor.
//
// This is worth more than a nicety: measured 2026-09-20, Claude Code
// emits no OSC 133 at all but does keep a live title carrying a
// spinner and a summary of the current turn. For the agent workload
// this is the status signal that actually exists.
func TestGridTracksTerminalTitle(t *testing.T) {
	g := term.NewVT(20, 5)
	defer g.Close()

	if got := g.Title(); got != "" {
		t.Errorf("fresh grid Title() = %q, want empty", got)
	}

	if _, err := g.Write([]byte("\x1b]2;first title\x07")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := g.Title(); got != "first title" {
		t.Errorf("Title() = %q, want %q", got, "first title")
	}

	// A live title is replaced, not appended.
	if _, err := g.Write([]byte("\x1b]2;second title\x07")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := g.Title(); got != "second title" {
		t.Errorf("Title() = %q, want %q", got, "second title")
	}
}

// OSC 0 sets both the icon name and the window title; OSC 1 sets only
// the icon name. Panes care about the title.
func TestGridTracksTitleFromOSC0(t *testing.T) {
	g := term.NewVT(20, 5)
	defer g.Close()

	if _, err := g.Write([]byte("\x1b]0;both\x07")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := g.Title(); got != "both" {
		t.Errorf("Title() after OSC 0 = %q, want %q", got, "both")
	}
}
