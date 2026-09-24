package server

import (
	"context"
	"testing"

	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

func TestCreationNoticeRetriesBeforeSnapshot(t *testing.T) {
	tp := transport.NewInProcChannel(1)
	s := NewServer(tp, "/bin/sh", "")
	s.strip.AddColumn(1, 40, 20, 0)
	s.strip.AddColumn(2, 40, 20, 1)
	s.pendingPaneCreated[tp] = []int{2}
	s.pendingCreationSnapshot[tp] = true
	ctx := context.Background()

	// A full queue must leave the notice pending.
	tp.ServerSend <- protocol.MsgPaneClosed{PaneID: 9}
	s.broadcastLayout(ctx)
	<-tp.ServerSend
	if len(s.pendingPaneCreated[tp]) != 1 {
		t.Fatal("creation notice was lost when the queue was full")
	}

	// A one-slot queue can accept the notice and snapshot only on
	// separate passes. The snapshot must keep retrying after the notice.
	s.broadcastLayout(ctx)
	if msg := <-tp.ServerSend; msg != (protocol.MsgPaneCreated{PaneID: 2}) {
		t.Fatalf("first accepted message = %T %+v, want creation notice", msg, msg)
	}
	if !s.pendingCreationSnapshot[tp] {
		t.Fatal("snapshot retry was lost after the notice was accepted")
	}
	s.broadcastLayout(ctx)
	msg := <-tp.ServerSend
	snap, ok := msg.(protocol.MsgLayoutSnapshot)
	if !ok || len(snap.Columns) != 2 {
		t.Fatalf("message after notice = %T %+v, want two-column snapshot", msg, msg)
	}
	if s.pendingCreationSnapshot[tp] {
		t.Fatal("creation snapshot still pending after delivery")
	}
}
