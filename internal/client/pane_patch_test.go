package client

import (
	"testing"

	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

func TestPanePatchUpdatesMirrorAndRequestsRecovery(t *testing.T) {
	tp := transport.NewInProcChannel(4)
	c := NewClient(tp, 40, 20, "C-b")
	lines := make([]protocol.LineData, 4)
	for y := range lines {
		lines[y] = make(protocol.LineData, 4)
		for x := range lines[y] {
			lines[y][x] = protocol.CellData{Content: " ", Width: 1}
		}
	}
	full := protocol.MsgPaneUpdate{PaneID: 1, Generation: 1, Cols: 4, Rows: 4, Lines: lines}
	c.HandleServerMsg(full)
	changed := append(protocol.LineData(nil), lines[0]...)
	changed[0] = protocol.CellData{Content: "X", Width: 1}
	patch := protocol.MsgPanePatch{PaneID: 1, Cols: 4, Rows: 4, BaseGeneration: 1, Generation: 2,
		ChangedRows: []protocol.PaneRow{{Y: 0, Cells: changed}}, CursorX: 1, CursorVisible: true, MouseTracking: true}
	c.HandleServerMsg(patch)
	if got := c.mirrors[1].Surface.CellAt(0, 0); got == nil || got.Content != "X" {
		t.Fatalf("patched mirror cell = %v", got)
	}
	if got := c.paneUpdates[1].Generation; got != 2 {
		t.Fatalf("pane generation = %d, want 2", got)
	}
	if !c.cursorInfos[1].visible || !c.mouseTracking[1] {
		t.Fatal("patch cursor or mouse state missing")
	}

	patch.Generation = 3
	patch.BaseGeneration = 1 // stale: the client now has generation 2
	c.HandleServerMsg(patch)
	select {
	case msg := <-tp.ClientSend:
		if msg != (protocol.MsgPaneResync{PaneID: 1}) {
			t.Fatalf("recovery request = %#v", msg)
		}
	default:
		t.Fatal("missing resync request")
	}
	if _, ok := c.paneUpdates[1]; ok {
		t.Fatal("invalid baseline remained usable")
	}
	c.HandleServerMsg(protocol.MsgPaneUpdate{PaneID: 1, Generation: 3, Cols: 4, Rows: 4, Lines: lines})
	if got := c.paneUpdates[1].Generation; got != 3 {
		t.Fatalf("full recovery generation = %d", got)
	}
}
