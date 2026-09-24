package server

import (
	"context"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

func TestMetadataChangeBroadcastsMsgPaneMetadata(t *testing.T) {
	s, grids := serverWithStatuses(t, map[int]protocol.PaneStatus{1: protocol.StatusIdle})
	tp := s.transports[0].(*transport.InProcChannel)
	ctx := context.Background()

	// Initial baseline broadcast
	if !s.broadcastMetadataIfChanged(ctx) {
		t.Fatal("first broadcastMetadataIfChanged should establish baseline")
	}

	select {
	case msg := <-tp.ServerSendChan():
		meta, ok := msg.(protocol.MsgPaneMetadata)
		if !ok || meta.PaneID != 1 {
			t.Fatalf("expected initial MsgPaneMetadata for pane 1, got %#v", msg)
		}
	default:
		t.Fatal("expected MsgPaneMetadata on channel")
	}

	// No changes: should not broadcast
	if s.broadcastMetadataIfChanged(ctx) {
		t.Fatal("broadcastMetadataIfChanged reported change with no update")
	}

	// Update CWD
	grids[1].setCWD("/home/user/project")
	if !s.broadcastMetadataIfChanged(ctx) {
		t.Fatal("broadcastMetadataIfChanged did not detect CWD change")
	}

	select {
	case msg := <-tp.ServerSendChan():
		meta, ok := msg.(protocol.MsgPaneMetadata)
		if !ok || meta.PaneID != 1 || meta.CWD != "/home/user/project" {
			t.Fatalf("expected MsgPaneMetadata with updated CWD, got %#v", msg)
		}
	default:
		t.Fatal("expected MsgPaneMetadata on channel after CWD change")
	}

	// Update UserVar
	grids[1].setUserVar("agent", "claude")
	if !s.broadcastMetadataIfChanged(ctx) {
		t.Fatal("broadcastMetadataIfChanged did not detect UserVar change")
	}

	select {
	case msg := <-tp.ServerSendChan():
		meta, ok := msg.(protocol.MsgPaneMetadata)
		if !ok || meta.PaneID != 1 || meta.UserVars["agent"] != "claude" {
			t.Fatalf("expected MsgPaneMetadata with updated UserVars, got %#v", msg)
		}
	default:
		t.Fatal("expected MsgPaneMetadata on channel after UserVar change")
	}
}

func TestMsgStatusRequestSendsPaneMetadata(t *testing.T) {
	s, grids := serverWithStatuses(t, map[int]protocol.PaneStatus{
		1: protocol.StatusIdle,
		2: protocol.StatusWorking,
	})
	grids[1].setCWD("/path/one")
	grids[2].setCWD("/path/two")
	grids[2].setUserVar("task", "build")

	tp := s.transports[0].(*transport.InProcChannel)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	s.handleClientMsg(ctx, tp, protocol.MsgStatusRequest{})

	receivedLayout := false
	metas := make(map[int]protocol.MsgPaneMetadata)

	for !receivedLayout || len(metas) < 2 {
		select {
		case msg := <-tp.ServerSendChan():
			switch m := msg.(type) {
			case protocol.MsgLayoutSnapshot:
				receivedLayout = true
			case protocol.MsgPaneMetadata:
				metas[m.PaneID] = m
			}
		case <-ctx.Done():
			t.Fatalf("timed out waiting for messages (receivedLayout=%v, metas=%d)", receivedLayout, len(metas))
		}
	}

	if !receivedLayout {
		t.Error("did not receive MsgLayoutSnapshot on MsgStatusRequest")
	}
	if len(metas) != 2 {
		t.Fatalf("received %d metadata messages, want 2", len(metas))
	}
	if metas[1].CWD != "/path/one" {
		t.Errorf("pane 1 CWD = %q, want /path/one", metas[1].CWD)
	}
	if metas[2].CWD != "/path/two" || metas[2].UserVars["task"] != "build" {
		t.Errorf("pane 2 metadata = %+v, want CWD /path/two and task=build", metas[2])
	}
}
