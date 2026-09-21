package term_test

import (
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
		want    term.PaneStatus
	}{
		// A is prompt-start and B is prompt-end. A shell emits both
		// back to back on every prompt, so mapping B to Working would
		// clobber A microseconds later and leave an idle shell reading
		// as busy. Both mean "waiting on you".
		{"prompt start", "\x1b]133;A\x07", term.StatusNeedsInput},
		{"prompt end", "\x1b]133;B\x07", term.StatusNeedsInput},

		// C is the start of command output: now it is actually busy.
		//
		// Note this row alone does not discriminate a working handler
		// from a dead one -- Write's activity fallback sets Working
		// regardless. It is here for completeness of the mapping, not
		// as evidence. The A/B and D rows are what prove the switch.
		{"output start", "\x1b]133;C\x07", term.StatusWorking},

		// D is command-finished. Bare D and D;0 are success.
		{"finished, no code", "\x1b]133;D\x07", term.StatusDone},
		{"finished, zero", "\x1b]133;D;0\x07", term.StatusDone},
		{"finished, nonzero", "\x1b]133;D;1\x07", term.StatusFailed},
		{"finished, signal code", "\x1b]133;D;130\x07", term.StatusFailed},

		// Payloads may carry trailing key=value fields. The old
		// HasSuffix(s, ";0") test read this one as a failure.
		{"zero with extra params", "\x1b]133;D;0;aid=1\x07", term.StatusDone},
		{"prompt with extra params", "\x1b]133;A;cl=m\x07", term.StatusNeedsInput},
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
	if got := g.Status(); got != term.StatusFailed {
		t.Errorf("Status() = %v after a malformed 133 then a valid D;1, want %v", got, term.StatusFailed)
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
	if got := g.Status(); got != term.StatusWorking {
		t.Fatalf("Status() = %v immediately after a write, want %v", got, term.StatusWorking)
	}

	time.Sleep(idle + 50*time.Millisecond)

	if got := g.Status(); got != term.StatusIdle {
		t.Errorf("Status() = %v after the idle window, want %v -- an unrecognised 133 payload latched sawOSC133 and disabled the idle fallback", got, term.StatusIdle)
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
