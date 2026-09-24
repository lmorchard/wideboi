package client

import (
	"testing"

	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/layout"
)

func clientWithStatuses(t *testing.T, statuses map[int]protocol.PaneStatus) *Client {
	c := &Client{
		paneStatuses: statuses,
		strip:        layout.NewStrip(),
	}
	for id := range statuses {
		c.strip.AddColumn(id, 40, 20, 0)
	}
	return c
}

// Failed outranks Done outranks NeedsInput.
func TestSmartJumpPrefersFailedOverDone(t *testing.T) {
	c := clientWithStatuses(t, map[int]protocol.PaneStatus{
		1: protocol.StatusDone,
		2: protocol.StatusFailed,
		3: protocol.StatusNeedsInput,
	})
	if got := c.smartJumpTargetLocked(); got != 2 {
		t.Errorf("smartJumpTargetLocked() = %d, want 2", got)
	}
}

func TestSmartJumpPrefersDoneOverNeedsInput(t *testing.T) {
	c := clientWithStatuses(t, map[int]protocol.PaneStatus{
		1: protocol.StatusNeedsInput,
		2: protocol.StatusNeedsInput,
		3: protocol.StatusDone,
	})
	if got := c.smartJumpTargetLocked(); got != 3 {
		t.Errorf("smartJumpTargetLocked() = %d, want 3", got)
	}
}

func TestSmartJumpIgnoresWorkingAndIdle(t *testing.T) {
	c := clientWithStatuses(t, map[int]protocol.PaneStatus{
		1: protocol.StatusWorking,
		2: protocol.StatusIdle,
		3: protocol.StatusWorking,
	})
	if got := c.smartJumpTargetLocked(); got != 0 {
		t.Errorf("smartJumpTargetLocked() = %d, want 0", got)
	}
}

func TestSmartJumpBreaksTiesOnLowestPaneID(t *testing.T) {
	c := clientWithStatuses(t, map[int]protocol.PaneStatus{
		2: protocol.StatusNeedsInput,
		5: protocol.StatusNeedsInput,
		9: protocol.StatusNeedsInput,
	})
	for i := 0; i < 50; i++ {
		if got := c.smartJumpTargetLocked(); got != 2 {
			t.Fatalf("iteration %d: smartJumpTargetLocked() = %d, want 2 every time", i, got)
		}
	}
}
