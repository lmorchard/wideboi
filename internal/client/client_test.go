package client_test

import (
	"context"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/client"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

func TestClientCalculatesPlacementsLocallyFromColumns(t *testing.T) {
	tp := transport.NewInProcChannel(16)
	cli := client.NewClient(tp, 100, 30, "C-b")

	cols := []protocol.ColumnData{
		{PaneID: 1, Width: 40, Height: 28},
		{PaneID: 2, Width: 40, Height: 28},
	}

	snap := protocol.MsgLayoutSnapshot{
		Columns: cols,
	}

	cli.HandleServerMsg(snap)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	cli.SendResize(ctx, 120, 30)

	if cli.FocusedPaneID() != 1 {
		t.Errorf("FocusedPaneID after resize = %d, want 1", cli.FocusedPaneID())
	}
}

func TestClientsKeepIndependentFocusAcrossSnapshots(t *testing.T) {
	first := client.NewClient(transport.NewInProcChannel(16), 100, 24, "C-b")
	second := client.NewClient(transport.NewInProcChannel(16), 100, 24, "C-b")
	snapshot := protocol.MsgLayoutSnapshot{Columns: []protocol.ColumnData{
		{PaneID: 1, Width: 40, Height: 22},
		{PaneID: 2, Width: 40, Height: 22},
	}}
	first.HandleServerMsg(snapshot)
	second.HandleServerMsg(snapshot)
	first.SendVerb(context.Background(), protocol.VerbFocusRight)
	if first.FocusedPaneID() != 2 || second.FocusedPaneID() != 1 {
		t.Fatalf("focus after local move: first=%d second=%d", first.FocusedPaneID(), second.FocusedPaneID())
	}

	// A status broadcast must preserve both clients' choices.
	first.HandleServerMsg(snapshot)
	second.HandleServerMsg(snapshot)
	if first.FocusedPaneID() != 2 || second.FocusedPaneID() != 1 {
		t.Fatalf("focus after broadcast: first=%d second=%d", first.FocusedPaneID(), second.FocusedPaneID())
	}

	// Only the requester hears that it created pane 3.
	first.HandleServerMsg(protocol.MsgPaneCreated{PaneID: 3})
	snapshot.Columns = append(snapshot.Columns, protocol.ColumnData{PaneID: 3, Width: 40, Height: 22})
	first.HandleServerMsg(snapshot)
	second.HandleServerMsg(snapshot)
	if first.FocusedPaneID() != 3 || second.FocusedPaneID() != 1 {
		t.Fatalf("focus after new pane: first=%d second=%d", first.FocusedPaneID(), second.FocusedPaneID())
	}

	// Closing each focused pane selects a live pane locally.
	snapshot.Columns = snapshot.Columns[:2]
	first.HandleServerMsg(snapshot)
	if first.FocusedPaneID() != 2 {
		t.Fatalf("focus after pane 3 closed = %d, want 2", first.FocusedPaneID())
	}
}

func TestMsgFocusPaneSwitchesFocus(t *testing.T) {
	cli := client.NewClient(transport.NewInProcChannel(16), 100, 24, "C-b")
	cli.HandleServerMsg(protocol.MsgLayoutSnapshot{Columns: []protocol.ColumnData{
		{PaneID: 1, Width: 40, Height: 22},
		{PaneID: 2, Width: 40, Height: 22},
	}})

	if got := cli.FocusedPaneID(); got != 1 {
		t.Fatalf("initial focus = %d, want 1", got)
	}

	cli.HandleServerMsg(protocol.MsgFocusPane{PaneID: 2})
	if got := cli.FocusedPaneID(); got != 2 {
		t.Fatalf("after MsgFocusPane: focus = %d, want 2", got)
	}
}
