package server

import (
	"context"
	"slices"
	"testing"

	"github.com/lmorchard/wideboi/internal/protocol"
	
)

func threeIdlePanes(t *testing.T) (*Server, []int) {
	t.Helper()
	s, _ := serverWithStatuses(t, map[int]protocol.PaneStatus{
		1: protocol.StatusIdle, 2: protocol.StatusIdle, 3: protocol.StatusIdle,
	})
	// serverWithStatuses adds columns in map order, so read it back.
	return s, s.strip.PaneIDs()
}

// Moving a column reorders the strip and resizes nothing: a pane's
// logical width is its column's width, and a move changes neither.
func TestMoveVerbsReorderWithoutResizing(t *testing.T) {
	s, order := threeIdlePanes(t)
	s.strip.FocusPaneID(order[2])

	s.handleClientMsg(context.Background(), protocol.MsgVerb{Verb: protocol.VerbMoveLeft, PaneID: order[2]})

	want := []int{order[0], order[2], order[1]}
	if got := s.strip.PaneIDs(); !slices.Equal(got, want) {
		t.Fatalf("after VerbMoveLeft: order = %v, want %v", got, want)
	}
	if got := s.strip.FocusedPaneID(); got != order[2] {
		t.Errorf("focus = %d, want %d (focus follows the column)", got, order[2])
	}
	for id, p := range s.panes {
		if p.cols != 40 {
			t.Errorf("pane %d cols = %d, want 40 -- a move must not resize", id, p.cols)
		}
	}

	s.handleClientMsg(context.Background(), protocol.MsgVerb{Verb: protocol.VerbMoveRight, PaneID: order[2]})
	if got := s.strip.PaneIDs(); !slices.Equal(got, order) {
		t.Fatalf("after VerbMoveRight: order = %v, want %v", got, order)
	}
}

// A click away and back through the verb: the server records focus the
// same way whatever moved it.
