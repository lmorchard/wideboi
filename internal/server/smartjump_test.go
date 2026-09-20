package server

// White-box (package server): drives smartJumpTargetLocked directly and
// reuses the statusGrid / serverWithStatuses fixtures from
// status_test.go.

import (
	"testing"

	"github.com/lmorchard/wideboi/internal/server/term"
)

// Failed outranks Done outranks NeedsInput. Once OSC 133's A and B both
// mean NeedsInput, a shell sits at a prompt almost all the time, so a
// first-match scan would land on whichever idle shell the map yielded
// first.
func TestSmartJumpPrefersFailedOverDone(t *testing.T) {
	s, _ := serverWithStatuses(t, map[int]term.PaneStatus{
		1: term.StatusDone,
		2: term.StatusFailed,
		3: term.StatusNeedsInput,
	})
	if got := s.smartJumpTargetLocked(); got != 2 {
		t.Errorf("smartJumpTargetLocked() = %d, want 2 (the failed pane)", got)
	}
}

func TestSmartJumpPrefersDoneOverNeedsInput(t *testing.T) {
	s, _ := serverWithStatuses(t, map[int]term.PaneStatus{
		1: term.StatusNeedsInput,
		2: term.StatusNeedsInput,
		3: term.StatusDone,
	})
	if got := s.smartJumpTargetLocked(); got != 3 {
		t.Errorf("smartJumpTargetLocked() = %d, want 3 (the finished pane)", got)
	}
}

// Working and Idle are never targets: a busy pane does not want you and
// an empty one has nothing to say.
func TestSmartJumpIgnoresWorkingAndIdle(t *testing.T) {
	s, _ := serverWithStatuses(t, map[int]term.PaneStatus{
		1: term.StatusWorking,
		2: term.StatusIdle,
		3: term.StatusWorking,
	})
	if got := s.smartJumpTargetLocked(); got != 0 {
		t.Errorf("smartJumpTargetLocked() = %d, want 0 (nothing wants attention)", got)
	}
}

// The tie-break is what makes repeated presses deterministic. s.panes is
// a map, so without an explicit rule the target varies run to run --
// re-run the selection enough times that map ordering would show.
func TestSmartJumpBreaksTiesOnLowestPaneID(t *testing.T) {
	s, _ := serverWithStatuses(t, map[int]term.PaneStatus{
		2: term.StatusNeedsInput,
		5: term.StatusNeedsInput,
		9: term.StatusNeedsInput,
	})
	for i := 0; i < 50; i++ {
		if got := s.smartJumpTargetLocked(); got != 2 {
			t.Fatalf("iteration %d: smartJumpTargetLocked() = %d, want 2 every time", i, got)
		}
	}
}
