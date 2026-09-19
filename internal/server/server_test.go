package server_test

import (
	"context"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/server"
	"github.com/lmorchard/wideboi/internal/transport"
)

func TestServerLifecycleAndAttach(t *testing.T) {
	tp := transport.NewInProcChannel(32)
	srv := server.NewServer(tp, "/bin/sh", "")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = srv.Run(ctx)
	}()

	// Send Attach
	tp.SendClient(ctx, protocol.MsgAttach{Cols: 80, Rows: 24})

	// Expect MsgLayoutSnapshot
	select {
	case msg := <-tp.ServerSend:
		snap, ok := msg.(protocol.MsgLayoutSnapshot)
		if !ok {
			t.Fatalf("expected MsgLayoutSnapshot, got %T", msg)
		}
		if len(snap.Placements) != 2 {
			t.Fatalf("expected 2 initial placements, got %d", len(snap.Placements))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for server MsgLayoutSnapshot")
	}

	if err := srv.Close(); err != nil {
		t.Fatalf("server close error: %v", err)
	}
}

func TestServerVerbHandling(t *testing.T) {
	tp := transport.NewInProcChannel(32)
	srv := server.NewServer(tp, "/bin/sh", "")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = srv.Run(ctx)
	}()

	tp.SendClient(ctx, protocol.MsgAttach{Cols: 80, Rows: 24})
	<-tp.ServerSend // Drain initial snapshot

	// Request new column
	tp.SendClient(ctx, protocol.MsgVerb{Verb: protocol.VerbNewColumn})

	select {
	case msg := <-tp.ServerSend:
		snap, ok := msg.(protocol.MsgLayoutSnapshot)
		if !ok {
			t.Fatalf("expected MsgLayoutSnapshot, got %T", msg)
		}
		if len(snap.Placements) != 2 {
			t.Fatalf("expected 2 placements after NewColumn verb, got %d", len(snap.Placements))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for MsgLayoutSnapshot after VerbNewColumn")
	}

	_ = srv.Close()
}

// The emulator and the child must both learn a new size. Before this,
// Pane.Resize had no callers at all and term.Reflow was dead code in the
// running binary.
func TestResizePropagatesToPanes(t *testing.T) {
	tp := transport.NewInProcChannel(32)
	srv := server.NewServer(tp, "/bin/sh", "")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = srv.Run(ctx)
	}()

	tp.SendClient(ctx, protocol.MsgAttach{Cols: 80, Rows: 24})
	<-tp.ServerSend // Drain initial snapshot

	tp.SendClient(ctx, protocol.MsgResize{Cols: 100, Rows: 40})

	select {
	case msg := <-tp.ServerSend:
		snap, ok := msg.(protocol.MsgLayoutSnapshot)
		if !ok {
			t.Fatalf("expected MsgLayoutSnapshot, got %T", msg)
		}
		if len(snap.Placements) == 0 {
			t.Fatal("expected placements after resize")
		}
		for _, pl := range snap.Placements {
			wantCols, wantRows := pl.Dst.Dx(), pl.Dst.Dy()
			cols, rows, ok := srv.PaneSize(pl.PaneID)
			if !ok {
				t.Fatalf("pane %d not found after resize", pl.PaneID)
			}
			if cols > 100 || rows > 40 {
				t.Errorf("pane %d has size %dx%d, larger than the 100x40 viewport", pl.PaneID, cols, rows)
			}
			if cols != wantCols || rows != wantRows {
				t.Errorf("pane %d size = %dx%d, want %dx%d to match its placement", pl.PaneID, cols, rows, wantCols, wantRows)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for MsgLayoutSnapshot after MsgResize")
	}

	_ = srv.Close()
}
