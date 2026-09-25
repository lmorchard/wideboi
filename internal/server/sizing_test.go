package server_test

import (
	"context"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/layout"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/server"
	"github.com/lmorchard/wideboi/internal/transport"
)

func drainChannel(ch <-chan transport.ServerMessage) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

func TestMultiClientSizingPolicy(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tp1 := transport.NewInProcChannel(32)
	tp2 := transport.NewInProcChannel(32)

	srv := server.NewServer(tp1, "/bin/sh", "")
	defer srv.Close()

	go func() { _ = srv.Run(ctx) }()

	// 1. First client attaches with 100x40.
	tp1.SendClient(ctx, protocol.MsgAttach{Cols: 100, Rows: 40})
	snap1 := recvLayoutSnapshot(t, tp1.ServerSend, 2*time.Second)
	if len(snap1.Columns) == 0 {
		t.Fatal("expected columns in first snapshot")
	}
	paneID := snap1.Columns[0].PaneID
	_, pRows, ok := srv.PaneSize(paneID)
	if !ok {
		t.Fatalf("pane %d not found", paneID)
	}
	if want := layout.AvailHeight(40); pRows != want {
		t.Fatalf("initial pane rows = %d, want %d", pRows, want)
	}
	cols, rows := srv.SessionDimensions()
	if cols != 100 || rows != 40 {
		t.Fatalf("session dimensions = %dx%d, want 100x40", cols, rows)
	}

	// 2. Second smaller client attaches with 60x20.
	srv.AddClientForTest(ctx, tp2)
	tp2.SendClient(ctx, protocol.MsgAttach{Cols: 60, Rows: 20})
	_ = recvLayoutSnapshot(t, tp2.ServerSend, 2*time.Second)

	// Verify session size did NOT shrink to 60x20.
	cols, rows = srv.SessionDimensions()
	if cols != 100 || rows != 40 {
		t.Fatalf("after second client attached, session shrunk to %dx%d, want 100x40", cols, rows)
	}
	_, pRows, _ = srv.PaneSize(paneID)
	if want := layout.AvailHeight(40); pRows != want {
		t.Fatalf("after second client attached, pane rows shrunk to %d, want %d", pRows, want)
	}

	// 3. Second client resizes to 50x15.
	tp2.SendClient(ctx, protocol.MsgResize{Cols: 50, Rows: 15})
	time.Sleep(50 * time.Millisecond)

	// Verify session size still did NOT change.
	cols, rows = srv.SessionDimensions()
	if cols != 100 || rows != 40 {
		t.Fatalf("after second client resized, session changed to %dx%d, want 100x40", cols, rows)
	}

	// 4. Primary client (owner) resizes to 120x50.
	drainChannel(tp1.ServerSend)
	tp1.SendClient(ctx, protocol.MsgResize{Cols: 120, Rows: 50})
	_ = recvLayoutSnapshot(t, tp1.ServerSend, 2*time.Second)

	cols, rows = srv.SessionDimensions()
	if cols != 120 || rows != 50 {
		t.Fatalf("after owner resized, session = %dx%d, want 120x50", cols, rows)
	}
	_, pRows, _ = srv.PaneSize(paneID)
	if want := layout.AvailHeight(50); pRows != want {
		t.Fatalf("after owner resized, pane rows = %d, want %d", pRows, want)
	}

	// 5. Owner client disconnects.
	close(tp1.ClientSend)
	time.Sleep(50 * time.Millisecond)

	// Session size should remain locked at 120x50.
	cols, rows = srv.SessionDimensions()
	if cols != 120 || rows != 50 {
		t.Fatalf("after owner disconnected, session changed to %dx%d, want 120x50", cols, rows)
	}
	_, pRows, _ = srv.PaneSize(paneID)
	if want := layout.AvailHeight(50); pRows != want {
		t.Fatalf("after owner disconnected, pane rows changed to %d, want %d", pRows, want)
	}

	// 6. Second client claims size.
	drainChannel(tp2.ServerSend)
	tp2.SendClient(ctx, protocol.MsgVerb{Verb: protocol.VerbClaimSize})
	snapClaim := recvLayoutSnapshot(t, tp2.ServerSend, 2*time.Second)
	if len(snapClaim.Columns) == 0 || snapClaim.Columns[0].Height != layout.AvailHeight(15) {
		t.Fatalf("after claim size, snapshot column height = %v, want %d", snapClaim.Columns, layout.AvailHeight(15))
	}

	// Session size should now match second client's recorded size (50x15).
	cols, rows = srv.SessionDimensions()
	if cols != 50 || rows != 15 {
		t.Fatalf("after claim size, session = %dx%d, want 50x15", cols, rows)
	}
	_, pRows, _ = srv.PaneSize(paneID)
	if want := layout.AvailHeight(15); pRows != want {
		t.Fatalf("after claim size, pane rows = %d, want %d", pRows, want)
	}

	// Now second client is the owner: second client resizes to 70x25.
	drainChannel(tp2.ServerSend)
	tp2.SendClient(ctx, protocol.MsgResize{Cols: 70, Rows: 25})
	_ = recvLayoutSnapshot(t, tp2.ServerSend, 2*time.Second)

	cols, rows = srv.SessionDimensions()
	if cols != 70 || rows != 25 {
		t.Fatalf("after new owner resized, session = %dx%d, want 70x25", cols, rows)
	}
}

func TestTransientZeroAttachFollowedByValidResizeEstablishesSize(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tp := transport.NewInProcChannel(32)
	srv := server.NewServer(tp, "/bin/sh", "")
	defer srv.Close()

	go func() { _ = srv.Run(ctx) }()

	// First attach arrives with 0x0
	tp.SendClient(ctx, protocol.MsgAttach{Cols: 0, Rows: 0})
	_ = recvLayoutSnapshot(t, tp.ServerSend, 2*time.Second)

	cols, rows := srv.SessionDimensions()
	if cols != 0 || rows != 0 {
		t.Fatalf("initial zero attach set session to %dx%d, want 0x0", cols, rows)
	}

	// Follow-up resize arrives with valid dimensions
	drainChannel(tp.ServerSend)
	tp.SendClient(ctx, protocol.MsgResize{Cols: 80, Rows: 24})
	_ = recvLayoutSnapshot(t, tp.ServerSend, 2*time.Second)

	cols, rows = srv.SessionDimensions()
	if cols != 80 || rows != 24 {
		t.Fatalf("after valid resize following zero attach, session = %dx%d, want 80x24", cols, rows)
	}
	if srv.SizeOwner() != tp {
		t.Fatalf("sizeOwner = %p, want %p", srv.SizeOwner(), tp)
	}
}
