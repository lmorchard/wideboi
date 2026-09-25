package server

import (
	"context"
	"testing"

	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

func TestPerPaneWidthOwnerTransfersOnClaim(t *testing.T) {
	a := transport.NewInProcChannel(32)
	b := transport.NewInProcChannel(32)
	s := NewServer(a, "/bin/sh", "")
	defer s.Close()
	s.strip.AddColumn(1, 40, 20, 0)
	s.clientSizes[a] = protocol.MsgResize{Cols: 100, Rows: 30}
	s.clientSizes[b] = protocol.MsgResize{Cols: 80, Rows: 24}
	s.sizeOwner = a
	ctx := context.Background()

	s.handleClientMsg(ctx, b, protocol.MsgSetPaneWidth{PaneID: 1, Width: 50})
	if width, _ := s.strip.ColumnWidth(1); width != 40 {
		t.Fatalf("viewer resized PTY to %d", width)
	}
	s.handleClientMsg(ctx, a, protocol.MsgSetPaneWidth{PaneID: 1, Width: 60})
	if width, _ := s.strip.ColumnWidth(1); width != 60 {
		t.Fatalf("owner width = %d, want 60", width)
	}
	s.handleClientMsg(ctx, b, protocol.MsgVerb{Verb: protocol.VerbClaimSize, Widths: map[int]int{1: 30}})
	if s.sizeOwner != b {
		t.Fatal("claim did not transfer size ownership")
	}
	if width, _ := s.strip.ColumnWidth(1); width != 30 {
		t.Fatalf("claim width = %d, want 30", width)
	}
	s.handleClientMsg(ctx, a, protocol.MsgSetPaneWidth{PaneID: 1, Width: 80})
	if width, _ := s.strip.ColumnWidth(1); width != 30 {
		t.Fatalf("former owner resized PTY to %d", width)
	}
}
