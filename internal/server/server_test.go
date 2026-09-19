package server_test

import (
	"context"
	"sync"
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
			if rows != 38 {
				t.Errorf("pane %d has rows=%d, want 38 (the new 40-row viewport minus header and status line)", pl.PaneID, rows)
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
			if rows != 18 {
				t.Errorf("pane %d has rows=%d, want 18 (the 20-row viewport minus header and status line)", pl.PaneID, rows)
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

// Regression guard for the "off-screen panes are never resized at all"
// finding: a column scrolled fully out of view has no Placement whatsoever
// (ComputePlacements drops it once its Dst is empty), so a resizePanesLocked
// that only walks Placements silently skips it -- it keeps whatever height
// it had before the resize forever, mismatched against every visible pane,
// until it happens to scroll back into view.
//
// Reproduces the exact numbers measured against the real server: at
// 120x30, both 59-wide columns are visible. Resizing to 40x20 with pane 1
// focused scrolls pane 2 completely out of the 40-wide viewport -- it gets
// no Placement in the resulting snapshot at all. Its own logical size must
// still track the new viewport height (19 rows), not the stale 29 rows
// from before the resize.
func TestResizeCoversFullyScrolledOffPane(t *testing.T) {
	tp := transport.NewInProcChannel(32)
	srv := server.NewServer(tp, "/bin/sh", "")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = srv.Run(ctx)
	}()

	tp.SendClient(ctx, protocol.MsgAttach{Cols: 120, Rows: 30})
	snap, ok := (<-tp.ServerSend).(protocol.MsgLayoutSnapshot)
	if !ok || len(snap.Placements) != 2 {
		t.Fatalf("expected 2 initial placements, got %+v", snap)
	}
	var paneIDs []int
	for _, pl := range snap.Placements {
		paneIDs = append(paneIDs, pl.PaneID)
	}

	tp.SendClient(ctx, protocol.MsgResize{Cols: 40, Rows: 20})

	select {
	case msg := <-tp.ServerSend:
		snap, ok = msg.(protocol.MsgLayoutSnapshot)
		if !ok {
			t.Fatalf("expected MsgLayoutSnapshot, got %T", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for MsgLayoutSnapshot after MsgResize")
	}

	if len(snap.Placements) != 1 {
		t.Fatalf("test setup bug: expected exactly 1 placement (the other pane scrolled fully off), got %d", len(snap.Placements))
	}

	for _, id := range paneIDs {
		cols, rows, ok := srv.PaneSize(id)
		if !ok {
			t.Fatalf("pane %d not found after resize", id)
		}
		if cols != 59 {
			t.Errorf("pane %d has cols=%d, want 59 (its full column width)", id, cols)
		}
		if rows != 18 {
			t.Errorf("pane %d has rows=%d, want 18 (the 20-row viewport minus header and status line) -- even a pane with no Placement must track the current viewport height", id, rows)
		}
	}

	_ = srv.Close()
}

// Regression guard for the "Pane.Resize lost its mutual exclusion" finding.
// s.mu used to serialize every Pane.Resize call for free; once
// resizePanesLocked releases it around the actual resize calls, two
// invocations can be mid-flight at once -- the Run loop handling a
// MsgResize, and a pty-reader goroutine calling onPaneExit for a different
// pane's death -- and both can reach the very same surviving pane's Resize
// concurrently. p.cols/p.rows are plain ints and term.Grid.Resize does an
// unlocked read-out/reflow/write-back of the cell buffer, so this is a real
// data race, not a theoretical one.
//
// This test's only real assertion is made by `go test -race`: it drives
// exactly the interleaving described above (one goroutine hammers
// MsgResize while a different pane's own shell exits from underneath it,
// on a real pty-reader goroutine) and leaves the race detector to find the
// unsynchronized access. The size check at the end is a basic sanity
// check, not the point of the test.
func TestConcurrentResizeAndPaneExitRace(t *testing.T) {
	tp := transport.NewInProcChannel(256)
	srv := server.NewServer(tp, "/bin/sh", "")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = srv.Run(ctx)
	}()

	tp.SendClient(ctx, protocol.MsgAttach{Cols: 120, Rows: 30})
	snap, ok := (<-tp.ServerSend).(protocol.MsgLayoutSnapshot)
	if !ok || len(snap.Placements) != 2 {
		t.Fatalf("expected 2 initial placements, got %+v", snap)
	}
	killID := snap.Placements[0].PaneID
	surviveID := snap.Placements[1].PaneID

	// Drain every further server->client message for the rest of the test.
	// broadcastLayoutLocked runs under s.mu, so an unread, full ServerSend
	// would park the Run loop and prevent the very overlap this test needs.
	drainDone := make(chan struct{})
	go func() {
		defer close(drainDone)
		for {
			select {
			case <-tp.ServerSend:
			case <-ctx.Done():
				return
			}
		}
	}()

	t.Log("STEP 1: sending exit")
	tp.SendClient(ctx, protocol.MsgInput{PaneID: killID, Data: []byte("exit\r")})

	var wg sync.WaitGroup
	deadline := time.Now().Add(1500 * time.Millisecond)
	wg.Add(1)
	go func() {
		defer wg.Done()
		n := 0
		for time.Now().Before(deadline) {
			n++
			tp.SendClient(ctx, protocol.MsgResize{Cols: 80 + n%40, Rows: 20 + n%10})
			time.Sleep(1 * time.Millisecond)
		}
		t.Log("STEP 2: hammering done")
	}()
	wg.Wait()
	t.Log("STEP 3: wg wait done")

	cols, rows, sizeOK := srv.PaneSize(surviveID)
	if !sizeOK || cols <= 0 || rows <= 0 {
		t.Errorf("surviving pane %d has size %dx%d ok=%v after the concurrent hammering, want a positive size", surviveID, cols, rows, sizeOK)
	}

	t.Log("STEP 4: cancelling ctx")
	cancel()
	t.Log("STEP 5: waiting for drainDone")
	<-drainDone
	t.Log("STEP 6: closing server")
	_ = srv.Close()
	t.Log("STEP 7: server closed")
}

// Regression guard for "s.rows <= 0 no longer short-circuits":
// layout.AvailHeight(0) is max(-1, 1) == 1, a positive number, so
// resizePanesLocked iterating PaneIDs directly (rather than
// ComputePlacements, which used to return nil for a non-positive
// viewport) would resize every pane down to a 1-row grid and SIGWINCH
// every child if a verb ever reached it before the first MsgAttach set
// s.rows.
func TestResizeSkipsWhenViewportNeverAttached(t *testing.T) {
	tp := transport.NewInProcChannel(32)
	srv := server.NewServer(tp, "/bin/sh", "")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = srv.Run(ctx)
	}()

	// No MsgAttach: s.rows is still its zero value.
	tp.SendClient(ctx, protocol.MsgVerb{Verb: protocol.VerbNewColumn})

	select {
	case msg := <-tp.ServerSend:
		snap, ok := msg.(protocol.MsgLayoutSnapshot)
		if !ok {
			t.Fatalf("expected MsgLayoutSnapshot, got %T", msg)
		}
		if snap.FocusPaneID <= 0 {
			t.Fatalf("expected a focused pane after VerbNewColumn, got %+v", snap)
		}
		_, rows, ok := srv.PaneSize(snap.FocusPaneID)
		if !ok {
			t.Fatalf("pane %d not found", snap.FocusPaneID)
		}
		if rows == 1 {
			t.Errorf("pane %d has rows=1 -- resizePanesLocked ran against an unset (0) viewport instead of skipping it", snap.FocusPaneID)
		}
		if rows <= 0 {
			t.Errorf("pane %d has non-positive rows=%d", snap.FocusPaneID, rows)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for MsgLayoutSnapshot after VerbNewColumn")
	}

	_ = srv.Close()
}
