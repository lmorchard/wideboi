package server_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/layout"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/server"
	"github.com/lmorchard/wideboi/internal/transport"
)

func TestConfiguredStartupPanes(t *testing.T) {
	tp := transport.NewInProcChannel(32)
	marker := filepath.Join(t.TempDir(), "started")
	srv := server.NewServer(tp, "/bin/sh", "")
	srv.SetCloseGrace(testGrace)
	srv.SetStartupPanes([]server.StartupPane{{Width: 59}, {Width: 59}})
	srv.SetStartupPanes([]server.StartupPane{
		{Command: fmt.Sprintf("printf started > %q; exec sleep 30", marker), Width: 77},
		{Width: 90},
		{Command: "exec sleep 30", Width: 55},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Run(ctx) }()
	defer srv.Close()

	tp.SendClient(ctx, protocol.MsgAttach{Cols: 80, Rows: 24})
	snap := recvLayoutSnapshot(t, tp.ServerSend, 2*time.Second)
	if len(snap.Columns) != 3 {
		t.Fatalf("startup columns = %d, want 3", len(snap.Columns))
	}
	for i, width := range []int{77, 90, 55} {
		if snap.Columns[i].Width != width {
			t.Errorf("column %d width = %d, want %d", i, snap.Columns[i].Width, width)
		}
		if cols, _, ok := srv.PaneSize(snap.Columns[i].PaneID); !ok || cols != width {
			t.Errorf("pane %d PTY width = %d (exists %v), want %d", i, cols, ok, width)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		} else if !os.IsNotExist(err) || time.Now().After(deadline) {
			t.Fatalf("startup command did not run: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	tp.SendClient(ctx, protocol.MsgAttach{Cols: 80, Rows: 24})
	snap = recvLayoutSnapshot(t, tp.ServerSend, 2*time.Second)
	if len(snap.Columns) != 3 {
		t.Errorf("reattach created more panes: %d", len(snap.Columns))
	}
}

// testGrace is the hangup grace these tests tear down with. None of them
// asserts anything about teardown -- that contract belongs to
// internal/server/ptyx's hangup tests and to scripts/ptycheck.py via
// `make verify-exit`.
const testGrace = 100 * time.Millisecond

// placementsAt computes what a scroll-mode client would place for snap
// at cols x rows. The server no longer computes placements (#47), so a
// test that needs to know what is on screen -- to confirm its own
// clipping scenario -- computes it the way a client does.
func placementsAt(snap protocol.MsgLayoutSnapshot, cols, rows int) []layout.Placement {
	s := layout.NewStrip()
	layout.ApplyMode(s, protocol.LayoutScroll)
	s.SyncColumns(snap.Columns, 1)
	return s.ComputePlacements(cols, rows)
}

func recvLayoutSnapshot(t *testing.T, ch <-chan transport.ServerMessage, timeout time.Duration) protocol.MsgLayoutSnapshot {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case msg := <-ch:
			if snap, ok := msg.(protocol.MsgLayoutSnapshot); ok {
				return snap
			}
		case <-deadline:
			t.Fatalf("timeout waiting for MsgLayoutSnapshot")
			return protocol.MsgLayoutSnapshot{}
		}
	}
}

func TestServerLifecycleAndAttach(t *testing.T) {
	tp := transport.NewInProcChannel(32)
	srv := server.NewServer(tp, "/bin/sh", "")
	srv.SetCloseGrace(testGrace)
	srv.SetStartupPanes([]server.StartupPane{{Width: 59}, {Width: 59}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = srv.Run(ctx)
	}()

	// Send Attach
	tp.SendClient(ctx, protocol.MsgAttach{Cols: 80, Rows: 24})

	snap := recvLayoutSnapshot(t, tp.ServerSend, 2*time.Second)
	if len(snap.Columns) != 2 {
		t.Fatalf("expected 2 initial columns, got %d", len(snap.Columns))
	}

	if err := srv.Close(); err != nil {
		t.Fatalf("server close error: %v", err)
	}
}

func TestServerVerbHandling(t *testing.T) {
	tp := transport.NewInProcChannel(32)
	srv := server.NewServer(tp, "/bin/sh", "")
	srv.SetCloseGrace(testGrace)
	srv.SetStartupPanes([]server.StartupPane{{Width: 59}, {Width: 59}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = srv.Run(ctx)
	}()

	tp.SendClient(ctx, protocol.MsgAttach{Cols: 80, Rows: 24})
	_ = recvLayoutSnapshot(t, tp.ServerSend, 2*time.Second)

	// Request new column
	tp.SendClient(ctx, protocol.MsgVerb{Verb: protocol.VerbNewColumn})

	// The session starts with two panes. This used to count the
	// server's placements, which at 80 columns in scroll mode was 2
	// either way -- the new pane scrolls off -- so it never showed the
	// verb did anything. Columns count every pane.
	snap := recvLayoutSnapshot(t, tp.ServerSend, 2*time.Second)
	if len(snap.Columns) != 3 {
		t.Fatalf("expected 3 columns after NewColumn verb, got %d", len(snap.Columns))
	}

	// Test GrowWidth verb
	focusedID := 1
	initialCols, _, ok := srv.PaneSize(focusedID)
	if !ok {
		t.Fatalf("pane %d not found", focusedID)
	}

	tp.SendClient(ctx, protocol.MsgVerb{Verb: protocol.VerbGrowWidth, PaneID: focusedID})
	_ = recvLayoutSnapshot(t, tp.ServerSend, 2*time.Second)

	grownCols, _, ok := srv.PaneSize(focusedID)
	if !ok || grownCols != initialCols+10 {
		t.Errorf("after VerbGrowWidth: cols = %d, want %d", grownCols, initialCols+10)
	}

	// Test ShrinkWidth verb
	tp.SendClient(ctx, protocol.MsgVerb{Verb: protocol.VerbShrinkWidth, PaneID: focusedID})
	_ = recvLayoutSnapshot(t, tp.ServerSend, 2*time.Second)

	shrunkCols, _, ok := srv.PaneSize(focusedID)
	if !ok || shrunkCols != initialCols {
		t.Errorf("after VerbShrinkWidth: cols = %d, want %d", shrunkCols, initialCols)
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
	srv.SetCloseGrace(testGrace)
	srv.SetStartupPanes([]server.StartupPane{{Width: 59}, {Width: 59}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = srv.Run(ctx)
	}()

	tp.SendClient(ctx, protocol.MsgAttach{Cols: 80, Rows: 24})
	_ = recvLayoutSnapshot(t, tp.ServerSend, 2*time.Second)

	tp.SendClient(ctx, protocol.MsgResize{Cols: 100, Rows: 40})

	snap := recvLayoutSnapshot(t, tp.ServerSend, 2*time.Second)
	if len(snap.Columns) == 0 {
		t.Fatal("expected columns after resize")
	}
	for _, col := range snap.Columns {
		cols, rows, ok := srv.PaneSize(col.PaneID)
		if !ok {
			t.Fatalf("pane %d not found after resize", col.PaneID)
		}
		if cols <= 0 || cols > 100 {
			t.Errorf("pane %d has cols=%d, want a positive width no wider than the 100-col viewport", col.PaneID, cols)
		}
		if rows != 38 {
			t.Errorf("pane %d has rows=%d, want 38 (the new 40-row viewport minus header and status line)", col.PaneID, rows)
		}
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
	srv.SetCloseGrace(testGrace)
	srv.SetStartupPanes([]server.StartupPane{{Width: 59}, {Width: 59}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = srv.Run(ctx)
	}()

	tp.SendClient(ctx, protocol.MsgAttach{Cols: 120, Rows: 30})
	_ = recvLayoutSnapshot(t, tp.ServerSend, 2*time.Second)

	tp.SendClient(ctx, protocol.MsgResize{Cols: 70, Rows: 20})

	snap := recvLayoutSnapshot(t, tp.ServerSend, 2*time.Second)
	sawClippedPlacement := false
	for _, pl := range placementsAt(snap, 70, 20) {
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
	srv.SetCloseGrace(testGrace)
	srv.SetStartupPanes([]server.StartupPane{{Width: 59}, {Width: 59}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = srv.Run(ctx)
	}()

	tp.SendClient(ctx, protocol.MsgAttach{Cols: 120, Rows: 30})
	snap := recvLayoutSnapshot(t, tp.ServerSend, 2*time.Second)
	if len(snap.Columns) != 2 {
		t.Fatalf("expected 2 initial columns, got %+v", snap)
	}
	var paneIDs []int
	for _, col := range snap.Columns {
		paneIDs = append(paneIDs, col.PaneID)
	}

	tp.SendClient(ctx, protocol.MsgResize{Cols: 40, Rows: 20})

	snap = recvLayoutSnapshot(t, tp.ServerSend, 2*time.Second)

	if n := len(placementsAt(snap, 40, 20)); n != 1 {
		t.Fatalf("test setup bug: expected exactly 1 placement (the other pane scrolled fully off), got %d", n)
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
	srv.SetCloseGrace(testGrace)
	srv.SetStartupPanes([]server.StartupPane{{Width: 59}, {Width: 59}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = srv.Run(ctx)
	}()

	tp.SendClient(ctx, protocol.MsgAttach{Cols: 120, Rows: 30})
	snap, ok := (<-tp.ServerSend).(protocol.MsgLayoutSnapshot)
	if !ok || len(snap.Columns) != 2 {
		t.Fatalf("expected 2 initial columns, got %+v", snap)
	}
	killID := snap.Columns[0].PaneID
	surviveID := snap.Columns[1].PaneID

	// Drain every further server->client message for the rest of the test.
	// broadcastLayout runs under s.mu, so an unread, full ServerSend
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
	srv.SetCloseGrace(testGrace)
	srv.SetStartupPanes([]server.StartupPane{{Width: 59}, {Width: 59}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = srv.Run(ctx)
	}()

	// No MsgAttach: s.rows is still its zero value.
	tp.SendClient(ctx, protocol.MsgVerb{Verb: protocol.VerbNewColumn})

	select {
	case msg := <-tp.ServerSend:
		if created, ok := msg.(protocol.MsgPaneCreated); ok {
			if created.PaneID != 1 {
				t.Fatalf("created pane = %d, want 1", created.PaneID)
			}
			msg = <-tp.ServerSend
		}
		snap, ok := msg.(protocol.MsgLayoutSnapshot)
		if !ok {
			t.Fatalf("expected MsgLayoutSnapshot, got %T", msg)
		}
		if len(snap.Columns) != 1 || snap.Columns[0].PaneID != 1 {
			t.Fatalf("expected the new pane in the snapshot, got %+v", snap)
		}
		_, rows, ok := srv.PaneSize(1)
		if !ok {
			t.Fatalf("pane %d not found", 1)
		}
		if rows == 1 {
			t.Errorf("pane %d has rows=1 -- resizePanesLocked ran against an unset (0) viewport instead of skipping it", 1)
		}
		if rows <= 0 {
			t.Errorf("pane %d has non-positive rows=%d", 1, rows)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for MsgLayoutSnapshot after VerbNewColumn")
	}

	_ = srv.Close()
}
