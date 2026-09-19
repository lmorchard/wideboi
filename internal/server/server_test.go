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
//
// This does not assert cols against the placement's Dst: at this viewport
// (80 -> 100 wide, two 40-wide columns) neither pane is ever clipped, so
// cols legitimately stays at its spawn value of 40 throughout, per the
// layout spec's invariant 4 ("a pane's logical width equals its column
// width, independent of what is visible"). Rows are the dimension this
// scenario actually exercises -- see TestResizeKeepsFullWidthForClippedPane
// for the clipped-column case, which is the one that must NOT change cols.
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
			cols, rows, ok := srv.PaneSize(pl.PaneID)
			if !ok {
				t.Fatalf("pane %d not found after resize", pl.PaneID)
			}
			if cols <= 0 || cols > 100 {
				t.Errorf("pane %d has cols=%d, want a positive width no wider than the 100-col viewport", pl.PaneID, cols)
			}
			if rows != 39 {
				t.Errorf("pane %d has rows=%d, want 39 (the new 40-row viewport minus the status line)", pl.PaneID, rows)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for MsgLayoutSnapshot after MsgResize")
	}

	_ = srv.Close()
}

// The regression guard for this round's Critical-1 finding: resizing to
// Placement.Dst.Dx() -- the post-clip crop -- shrank a partly-scrolled-off
// pane's own child terminal down to whatever sliver was on screen, instead
// of leaving its logical width alone as the layout spec's invariant 4
// requires. A partly-covered pane keeps its full logical width so its
// child gets no SIGWINCH and never learns it was occluded.
//
// Reproduces the exact numbers measured against the real server: two
// 59-wide columns fit an 120-wide viewport untouched; narrowing to 70
// leaves pane 1 (focused) fully visible but scrolls pane 2 down to a
// 10-column sliver (Dst=(60,0)-(70,19), Src=(0,0)-(10,19)). Both panes'
// own logical size must stay 59 wide regardless.
func TestResizeKeepsFullWidthForClippedPane(t *testing.T) {
	tp := transport.NewInProcChannel(32)
	srv := server.NewServer(tp, "/bin/sh", "")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = srv.Run(ctx)
	}()

	tp.SendClient(ctx, protocol.MsgAttach{Cols: 120, Rows: 30})
	<-tp.ServerSend // Drain initial snapshot

	tp.SendClient(ctx, protocol.MsgResize{Cols: 70, Rows: 20})

	select {
	case msg := <-tp.ServerSend:
		snap, ok := msg.(protocol.MsgLayoutSnapshot)
		if !ok {
			t.Fatalf("expected MsgLayoutSnapshot, got %T", msg)
		}
		sawClippedPlacement := false
		for _, pl := range snap.Placements {
			if pl.Dst.Dx() < 59 {
				sawClippedPlacement = true
			}
			cols, rows, ok := srv.PaneSize(pl.PaneID)
			if !ok {
				t.Fatalf("pane %d not found after resize", pl.PaneID)
			}
			if cols != 59 {
				t.Errorf("pane %d has cols=%d, want 59 (its full column width) regardless of its %d-wide on-screen crop", pl.PaneID, cols, pl.Dst.Dx())
			}
			if rows != 19 {
				t.Errorf("pane %d has rows=%d, want 19 (the 20-row viewport minus the status line)", pl.PaneID, rows)
			}
		}
		if !sawClippedPlacement {
			t.Fatal("test setup bug: expected at least one placement clipped narrower than 59 columns")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for MsgLayoutSnapshot after MsgResize")
	}

	_ = srv.Close()
}
