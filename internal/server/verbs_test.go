package server

import (
	"context"
	"slices"
	"testing"

	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/server/term"
)

func threeIdlePanes(t *testing.T) (*Server, []int) {
	t.Helper()
	s, _ := serverWithStatuses(t, map[int]term.PaneStatus{
		1: term.StatusIdle, 2: term.StatusIdle, 3: term.StatusIdle,
	})
	// serverWithStatuses adds columns in map order, so read it back.
	return s, s.strip.PaneIDs()
}

// Moving a column reorders the strip and resizes nothing: a pane's
// logical width is its column's width, and a move changes neither.
func TestMoveVerbsReorderWithoutResizing(t *testing.T) {
	s, order := threeIdlePanes(t)
	s.strip.FocusPaneID(order[2])

	s.handleClientMsg(context.Background(), nil, protocol.MsgVerb{Verb: protocol.VerbMoveLeft})

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

	s.handleClientMsg(context.Background(), nil, protocol.MsgVerb{Verb: protocol.VerbMoveRight})
	if got := s.strip.PaneIDs(); !slices.Equal(got, order) {
		t.Fatalf("after VerbMoveRight: order = %v, want %v", got, order)
	}
}

// A click away and back through the verb: the server records focus the
// same way whatever moved it.
func TestFocusLastVerbFlipsBack(t *testing.T) {
	s, order := threeIdlePanes(t)
	ctx := context.Background()
	s.handleClientMsg(ctx, nil, protocol.MsgFocusPane{PaneID: order[0]})
	s.handleClientMsg(ctx, nil, protocol.MsgFocusPane{PaneID: order[2]})

	s.handleClientMsg(ctx, nil, protocol.MsgVerb{Verb: protocol.VerbFocusLast})

	if got := s.strip.FocusedPaneID(); got != order[0] {
		t.Errorf("focus after VerbFocusLast = %d, want %d", got, order[0])
	}
}
