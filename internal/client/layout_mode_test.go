package client

import (
	"strings"
	"testing"

	"github.com/lmorchard/wideboi/internal/layout"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

// Layout mode is the client's own since #92: set from its config,
// toggled locally, never learned from or told to the server.

// expectedPlacements is what a strip in mode would place for cols at
// the given viewport, computed independently of the client.
func expectedPlacements(mode protocol.LayoutMode, cols []protocol.ColumnData, focus, w, h int) []protocol.PlacementData {
	s := layout.NewStrip()
	layout.ApplyMode(s, mode)
	s.SyncColumns(cols, focus)
	return layout.ToProtocol(s.ComputePlacements(w, h))
}

func clientPlacements(cli *Client) []protocol.PlacementData {
	cli.mu.Lock()
	defer cli.mu.Unlock()
	return append([]protocol.PlacementData(nil), cli.placements...)
}

func TestToggleLayoutSendsNothing(t *testing.T) {
	tp := transport.NewInProcChannel(16)
	cli := NewClient(tp, 100, 24, "C-b")
	cli.SetLayoutMode(protocol.LayoutScroll)
	cli.focusPaneID = 2
	cli.HandleServerMsg(protocol.MsgLayoutSnapshot{Columns: threeColumns()})

	cli.ToggleLayout()

	cli.mu.Lock()
	mode := cli.layoutMode
	cli.mu.Unlock()
	if mode != protocol.LayoutCards {
		t.Errorf("layoutMode = %v after one toggle from scroll, want %v", mode, protocol.LayoutCards)
	}
	want := expectedPlacements(protocol.LayoutCards, threeColumns(), 2, 100, 24)
	if got := clientPlacements(cli); !placementsEqual(got, want) {
		t.Errorf("placements after toggle are not the card layout:\n got %+v\nwant %+v", got, want)
	}
	select {
	case msg := <-tp.ClientSend:
		t.Errorf("toggle sent %T to the server; layout is client-local", msg)
	default:
	}
}

func TestToggleLayoutArmsMotion(t *testing.T) {
	cli := NewClient(transport.NewInProcChannel(16), 100, 24, "C-b")
	cli.SetLayoutMode(protocol.LayoutScroll)
	cli.focusPaneID = 2
	cli.HandleServerMsg(protocol.MsgLayoutSnapshot{Columns: threeColumns()})
	if placementsEqual(
		expectedPlacements(protocol.LayoutScroll, threeColumns(), 2, 100, 24),
		expectedPlacements(protocol.LayoutCards, threeColumns(), 2, 100, 24)) {
		t.Fatal("test setup bug: scroll and cards place this fixture identically")
	}

	for i, want := range []protocol.LayoutMode{protocol.LayoutCards, protocol.LayoutScroll} {
		cli.mu.Lock()
		cli.motion = nil
		cli.mu.Unlock()

		cli.ToggleLayout()

		cli.mu.Lock()
		m := cli.motion
		cli.mu.Unlock()
		if m == nil {
			t.Fatalf("toggle %d (to %v) did not arm motion", i+1, want)
		}
		if !placementsEqual(m.to, expectedPlacements(want, threeColumns(), 2, 100, 24)) {
			t.Errorf("toggle %d motion heads for %+v, not the %v layout", i+1, m.to, want)
		}
	}
}

func TestSetLayoutModeBeforeFirstSnapshot(t *testing.T) {
	cli := NewClient(transport.NewInProcChannel(16), 100, 24, "C-b")
	cli.SetLayoutMode(protocol.LayoutCards)
	cli.focusPaneID = 2
	cli.HandleServerMsg(protocol.MsgLayoutSnapshot{Columns: threeColumns()})

	want := expectedPlacements(protocol.LayoutCards, threeColumns(), 2, 100, 24)
	if got := clientPlacements(cli); !placementsEqual(got, want) {
		t.Errorf("first snapshot did not use the mode set beforehand:\n got %+v\nwant %+v", got, want)
	}
}

// A toggle changes placements with no snapshot and no resend, so the
// pane it reveals has to already have a mirror. Mirrors used to be
// pruned by placement, which dropped every off-screen pane's on the
// next snapshot -- including the status-only ones a busy pane causes.
func TestToggleRevealsOffscreenPaneContent(t *testing.T) {
	const cols, rows = 100, 16
	cli := NewClient(transport.NewInProcChannel(16), cols, rows, "C-b")
	cli.SetLayoutMode(protocol.LayoutScroll)

	snap := protocol.MsgLayoutSnapshot{
		Columns:    threeColumns(),
		PaneTitles: map[int]string{3: "TITLE"},
	}

	cli.HandleServerMsg(snap)
	if p := placementFor(cli, 3); p.PaneID != 0 {
		t.Fatalf("test setup bug: pane 3 is placed in scroll mode at %v", p.Dst)
	}
	cli.HandleServerMsg(paneUpdate(1, 60, 10, "CONTENT-ONE"))
	cli.HandleServerMsg(paneUpdate(2, 60, 10, "CONTENT-TWO"))
	cli.HandleServerMsg(paneUpdate(3, 60, 10, "CONTENT-THREE"))
	snap.PaneStatuses = map[int]protocol.PaneStatus{3: protocol.StatusWorking} // a status-only change
	cli.HandleServerMsg(snap)

	cli.ToggleLayout()
	cli.mu.Lock()
	cli.motion = nil // draw the settled layout
	cli.mu.Unlock()

	p3 := placementFor(cli, 3)
	if p3.PaneID == 0 {
		t.Fatal("test setup bug: pane 3 is not placed in card mode either")
	}
	scr := newFakeHostScreen(cols, rows)
	cli.Draw(scr)
	if got := regionText(scr, p3.Dst); !strings.Contains(got, "ONTENT-THREE") {
		t.Errorf("pane 3 revealed by the toggle is blank; its mirror was pruned:\n%s", got)
	}
}
