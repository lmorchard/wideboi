package server_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func TestStartupDashboardPane(t *testing.T) {
	tp := transport.NewInProcChannel(32)
	srv := server.NewServer(tp, "/bin/sh", "")
	srv.SetCloseGrace(testGrace)
	srv.SetStartupPanes([]server.StartupPane{
		{Type: "dashboard", Width: 28},
		{Width: 80},
		{Type: "dashboard", Width: 35}, // duplicate dashboard; must be ignored
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Run(ctx) }()
	defer srv.Close()

	tp.SendClient(ctx, protocol.MsgAttach{Cols: 120, Rows: 24})
	snap := recvLayoutSnapshot(t, tp.ServerSend, 2*time.Second)

	// Should create exactly 2 columns (dashboard + interactive shell, duplicate ignored)
	if len(snap.Columns) != 2 {
		t.Fatalf("startup columns count = %d, want 2", len(snap.Columns))
	}
	if snap.Columns[0].Width != 28 {
		t.Errorf("dashboard column width = %d, want 28", snap.Columns[0].Width)
	}
	if snap.Columns[1].Width != 80 {
		t.Errorf("shell column width = %d, want 80", snap.Columns[1].Width)
	}

	dashboardID := snap.Columns[0].PaneID
	shellID := snap.Columns[1].PaneID

	// Initial focus must prefer the interactive shell, not the dashboard
	// (verified via server strip focused pane)
	if _, ok := snap.PaneStatuses[dashboardID]; !ok {
		t.Errorf("dashboard pane %d missing from PaneStatuses", dashboardID)
	}

	// Pressing VerbToggleStatus should target the already-running dashboard pane
	tp.SendClient(ctx, protocol.MsgVerb{Verb: protocol.VerbToggleStatus, PaneID: shellID})
	deadline := time.After(2 * time.Second)
	focusReceived := false
	for !focusReceived {
		select {
		case msg := <-tp.ServerSend:
			if foc, ok := msg.(protocol.MsgFocusPane); ok {
				if foc.PaneID != dashboardID {
					t.Errorf("MsgFocusPane = %d, want dashboard ID %d", foc.PaneID, dashboardID)
				}
				focusReceived = true
			}
		case <-deadline:
			t.Fatal("timed out waiting for MsgFocusPane after VerbToggleStatus")
		}
	}
}

func TestStartupPinnedColumns(t *testing.T) {
	tp := transport.NewInProcChannel(32)
	srv := server.NewServer(tp, "/bin/sh", "")
	srv.SetCloseGrace(testGrace)
	srv.SetStartupPanes([]server.StartupPane{
		{Type: "dashboard", Width: 28, Pinned: true},
		{Width: 80},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Run(ctx) }()
	defer srv.Close()

	tp.SendClient(ctx, protocol.MsgAttach{Cols: 120, Rows: 24})
	snap := recvLayoutSnapshot(t, tp.ServerSend, 2*time.Second)

	if len(snap.Columns) != 2 {
		t.Fatalf("startup columns count = %d, want 2", len(snap.Columns))
	}
	if !snap.Columns[0].Pinned {
		t.Errorf("expected column 0 to be pinned: %+v", snap.Columns[0])
	}
	if snap.Columns[1].Pinned {
		t.Errorf("expected column 1 to be unpinned: %+v", snap.Columns[1])
	}
}

func TestServerDashboardUnseenDone(t *testing.T) {
	tp := transport.NewInProcChannel(32)
	srv := server.NewServer(tp, "/bin/sh", "")
	srv.SetCloseGrace(testGrace)
	srv.SetStartupPanes([]server.StartupPane{
		{Type: "dashboard", Width: 28},
		{Width: 80},
		{Width: 80},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Run(ctx) }()
	defer srv.Close()

	tp.SendClient(ctx, protocol.MsgAttach{Cols: 120, Rows: 24})
	snap := recvLayoutSnapshot(t, tp.ServerSend, 2*time.Second)

	shell2ID := snap.Columns[2].PaneID

	// Simulate shell2 completing work in background
	srv.SimulateUnseenCompletionForTest(shell2ID)
	dbText := srv.DashboardTextForTest(10)

	if !strings.Contains(dbText, "✔") {
		t.Errorf("dashboard text missing '✔' (done) for unseen completed pane %d:\n%s", shell2ID, dbText)
	}

	// Focusing shell2 marks it seen and clears 'done'
	srv.MarkSeenForTest(shell2ID)
	dbTextAfter := srv.DashboardTextForTest(10)

	if strings.Contains(dbTextAfter, "✔") {
		t.Errorf("dashboard text still contains '✔' after pane %d focused:\n%s", shell2ID, dbTextAfter)
	}
}

func TestVerbTogglePin(t *testing.T) {
	tp := transport.NewInProcChannel(32)
	srv := server.NewServer(tp, "/bin/sh", "")
	srv.SetCloseGrace(testGrace)
	srv.SetStartupPanes([]server.StartupPane{
		{Width: 80},
		{Width: 80},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Run(ctx) }()
	defer srv.Close()

	tp.SendClient(ctx, protocol.MsgAttach{Cols: 120, Rows: 24})
	snap := recvLayoutSnapshot(t, tp.ServerSend, 2*time.Second)

	pane2ID := snap.Columns[1].PaneID

	// Send VerbTogglePin on pane 2
	tp.SendClient(ctx, protocol.MsgVerb{Verb: protocol.VerbTogglePin, PaneID: pane2ID})

	// Wait for updated layout snapshot with pane 2 pinned
	deadline := time.After(2 * time.Second)
	pinned := false
	for !pinned {
		select {
		case msg := <-tp.ServerSend:
			if nextSnap, ok := msg.(protocol.MsgLayoutSnapshot); ok {
				if len(nextSnap.Columns) > 0 && nextSnap.Columns[0].PaneID == pane2ID && nextSnap.Columns[0].Pinned {
					pinned = true
				}
			}
		case <-deadline:
			t.Fatal("timed out waiting for MsgLayoutSnapshot with pinned pane 2")
		}
	}
}

func TestVerbToggleCollapse(t *testing.T) {
	tp := transport.NewInProcChannel(32)
	srv := server.NewServer(tp, "/bin/sh", "")
	srv.SetCloseGrace(testGrace)
	srv.SetStartupPanes([]server.StartupPane{
		{Width: 80},
		{Width: 80},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Run(ctx) }()
	defer srv.Close()

	tp.SendClient(ctx, protocol.MsgAttach{Cols: 120, Rows: 24})
	snap := recvLayoutSnapshot(t, tp.ServerSend, 2*time.Second)

	pane1ID := snap.Columns[0].PaneID

	// Send VerbToggleCollapse on pane 1
	tp.SendClient(ctx, protocol.MsgVerb{Verb: protocol.VerbToggleCollapse, PaneID: pane1ID})

	// Wait for updated layout snapshot with pane 1 collapsed
	deadline := time.After(2 * time.Second)
	collapsed := false
	for !collapsed {
		select {
		case msg := <-tp.ServerSend:
			if nextSnap, ok := msg.(protocol.MsgLayoutSnapshot); ok {
				for _, col := range nextSnap.Columns {
					if col.PaneID == pane1ID && col.Collapsed {
						collapsed = true
						break
					}
				}
			}
		case <-deadline:
			t.Fatal("timed out waiting for MsgLayoutSnapshot with collapsed pane 1")
		}
	}
}

func recvSetPaneStatusResponse(t *testing.T, ch <-chan transport.ServerMessage, timeout time.Duration) protocol.MsgSetPaneStatusResponse {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case msg := <-ch:
			if resp, ok := msg.(protocol.MsgSetPaneStatusResponse); ok {
				return resp
			}
		case <-deadline:
			t.Fatalf("timeout waiting for MsgSetPaneStatusResponse")
			return protocol.MsgSetPaneStatusResponse{}
		}
	}
}

func TestSetPaneStatusExplicitOverride(t *testing.T) {
	tp := transport.NewInProcChannel(32)
	srv := server.NewServer(tp, "/bin/sh", "")
	srv.SetCloseGrace(testGrace)
	srv.SetStartupPanes([]server.StartupPane{
		{Width: 80},
		{Width: 80},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Run(ctx) }()
	defer srv.Close()

	tp.SendClient(ctx, protocol.MsgAttach{Cols: 120, Rows: 24})
	snap := recvLayoutSnapshot(t, tp.ServerSend, 2*time.Second)

	pane2ID := snap.Columns[1].PaneID

	// 1. Explicitly set StatusWorking
	tp.SendClient(ctx, protocol.MsgSetPaneStatusRequest{PaneID: pane2ID, Status: protocol.StatusWorking})
	res := recvSetPaneStatusResponse(t, tp.ServerSend, 2*time.Second)
	if res.Error != "" {
		t.Fatalf("unexpected error setting working: %s", res.Error)
	}

	// 2. Explicitly set StatusNeedsInput
	tp.SendClient(ctx, protocol.MsgSetPaneStatusRequest{PaneID: pane2ID, Status: protocol.StatusNeedsInput})
	res = recvSetPaneStatusResponse(t, tp.ServerSend, 2*time.Second)
	if res.Error != "" {
		t.Fatalf("unexpected error setting input: %s", res.Error)
	}

	// 3. Clear explicit override
	tp.SendClient(ctx, protocol.MsgSetPaneStatusRequest{PaneID: pane2ID, Clear: true})
	res = recvSetPaneStatusResponse(t, tp.ServerSend, 2*time.Second)
	if res.Error != "" {
		t.Fatalf("unexpected error clearing status: %s", res.Error)
	}

	// 4. Non-existent pane returns error
	tp.SendClient(ctx, protocol.MsgSetPaneStatusRequest{PaneID: 9999, Status: protocol.StatusWorking})
	res = recvSetPaneStatusResponse(t, tp.ServerSend, 2*time.Second)
	if res.Error == "" || !strings.Contains(res.Error, "not found") {
		t.Fatalf("expected not found error, got: %q", res.Error)
	}
}

func recvWaitOutputResponse(t *testing.T, ch <-chan transport.ServerMessage, timeout time.Duration) protocol.MsgWaitOutputResponse {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case msg := <-ch:
			if resp, ok := msg.(protocol.MsgWaitOutputResponse); ok {
				return resp
			}
		case <-deadline:
			t.Fatalf("timeout waiting for MsgWaitOutputResponse")
			return protocol.MsgWaitOutputResponse{}
		}
	}
}

func recvWaitStatusResponse(t *testing.T, ch <-chan transport.ServerMessage, timeout time.Duration) protocol.MsgWaitStatusResponse {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case msg := <-ch:
			if resp, ok := msg.(protocol.MsgWaitStatusResponse); ok {
				return resp
			}
		case <-deadline:
			t.Fatalf("timeout waiting for MsgWaitStatusResponse")
			return protocol.MsgWaitStatusResponse{}
		}
	}
}

func TestWaitOutputImmediateAndDelayed(t *testing.T) {
	tp := transport.NewInProcChannel(32)
	srv := server.NewServer(tp, "/bin/sh", "")
	srv.SetCloseGrace(testGrace)
	srv.SetStartupPanes([]server.StartupPane{
		{Width: 80},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Run(ctx) }()
	defer srv.Close()

	tp.SendClient(ctx, protocol.MsgAttach{Cols: 120, Rows: 24})
	snap := recvLayoutSnapshot(t, tp.ServerSend, 2*time.Second)
	paneID := snap.Columns[0].PaneID

	// Send echo command to pane
	tp.SendClient(ctx, protocol.MsgSendInputRequest{
		PaneID: paneID,
		Data:   []byte("echo 'ready-step-1'\n"),
	})

	// Immediate wait for output that appeared
	tp.SendClient(ctx, protocol.MsgWaitOutputRequest{
		PaneID: paneID,
		Match:  "ready-step-1",
	})
	resp := recvWaitOutputResponse(t, tp.ServerSend, 2*time.Second)
	if resp.Error != "" {
		t.Fatalf("unexpected error waiting for immediate output: %s", resp.Error)
	}
	if !strings.Contains(resp.MatchedLine, "ready-step-1") {
		t.Fatalf("expected matched line to contain ready-step-1, got: %q", resp.MatchedLine)
	}

	// Delayed wait: register waiter for regex before command executes
	tp.SendClient(ctx, protocol.MsgWaitOutputRequest{
		PaneID: paneID,
		Regex:  `step-[0-9]+-done`,
	})

	// Small pause, then send command that outputs the match
	time.Sleep(50 * time.Millisecond)
	tp.SendClient(ctx, protocol.MsgSendInputRequest{
		PaneID: paneID,
		Data:   []byte("echo 'step-2-done'\n"),
	})

	resp = recvWaitOutputResponse(t, tp.ServerSend, 2*time.Second)
	if resp.Error != "" {
		t.Fatalf("unexpected error waiting for delayed output: %s", resp.Error)
	}
	if !strings.Contains(resp.MatchedLine, "step-2-done") {
		t.Fatalf("expected matched line to contain step-2-done, got: %q", resp.MatchedLine)
	}
}

func TestWaitStatusImmediateAndDelayed(t *testing.T) {
	tp := transport.NewInProcChannel(32)
	srv := server.NewServer(tp, "/bin/sh", "")
	srv.SetCloseGrace(testGrace)
	srv.SetStartupPanes([]server.StartupPane{
		{Width: 80},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Run(ctx) }()
	defer srv.Close()

	tp.SendClient(ctx, protocol.MsgAttach{Cols: 120, Rows: 24})
	snap := recvLayoutSnapshot(t, tp.ServerSend, 2*time.Second)
	paneID := snap.Columns[0].PaneID

	// Immediate: status is StatusIdle or StatusWorking
	tp.SendClient(ctx, protocol.MsgWaitStatusRequest{
		PaneID: paneID,
		Until:  []protocol.PaneStatus{protocol.StatusIdle, protocol.StatusWorking},
	})
	resp := recvWaitStatusResponse(t, tp.ServerSend, 2*time.Second)
	if resp.Error != "" {
		t.Fatalf("unexpected error in immediate wait-status: %s", resp.Error)
	}

	// Delayed: wait for StatusNeedsInput
	tp.SendClient(ctx, protocol.MsgWaitStatusRequest{
		PaneID: paneID,
		Until:  []protocol.PaneStatus{protocol.StatusNeedsInput},
	})

	// Small pause, then explicitly set StatusNeedsInput
	time.Sleep(50 * time.Millisecond)
	tp.SendClient(ctx, protocol.MsgSetPaneStatusRequest{
		PaneID: paneID,
		Status: protocol.StatusNeedsInput,
	})

	resp = recvWaitStatusResponse(t, tp.ServerSend, 2*time.Second)
	if resp.Error != "" {
		t.Fatalf("unexpected error in delayed wait-status: %s", resp.Error)
	}
	if resp.Status != protocol.StatusNeedsInput {
		t.Fatalf("expected StatusNeedsInput, got: %v", resp.Status)
	}
}

func TestWaitStatusNonKeptExitDone(t *testing.T) {
	tp := transport.NewInProcChannel(32)
	srv := server.NewServer(tp, "/bin/sh", "")
	srv.SetCloseGrace(testGrace)
	srv.SetStartupPanes([]server.StartupPane{
		{Width: 80},
		{Width: 80, Command: "sleep 0.2; exit 0"},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Run(ctx) }()
	defer srv.Close()

	tp.SendClient(ctx, protocol.MsgAttach{Cols: 120, Rows: 24})
	snap := recvLayoutSnapshot(t, tp.ServerSend, 2*time.Second)
	if len(snap.Columns) < 2 {
		t.Fatalf("expected at least 2 columns, got %d", len(snap.Columns))
	}
	pane2ID := snap.Columns[1].PaneID

	// Wait for status done on pane 2
	tp.SendClient(ctx, protocol.MsgWaitStatusRequest{
		PaneID: pane2ID,
		Until:  []protocol.PaneStatus{protocol.StatusDone},
	})
	resp := recvWaitStatusResponse(t, tp.ServerSend, 3*time.Second)
	if resp.Error != "" {
		t.Fatalf("unexpected error waiting for non-kept exit done: %s", resp.Error)
	}
	if resp.Status != protocol.StatusDone {
		t.Fatalf("expected StatusDone, got: %v", resp.Status)
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

func recvLayoutWidth(t *testing.T, ch <-chan transport.ServerMessage, paneID, width int) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case msg := <-ch:
			if snap, ok := msg.(protocol.MsgLayoutSnapshot); ok {
				for _, col := range snap.Columns {
					if col.PaneID == paneID && col.Width == width {
						return
					}
				}
			}
		case <-deadline:
			t.Fatalf("timeout waiting for pane %d layout width %d", paneID, width)
		}
	}
}

func recvLayoutColumns(t *testing.T, ch <-chan transport.ServerMessage, wantCols int, timeout time.Duration) protocol.MsgLayoutSnapshot {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case msg := <-ch:
			if snap, ok := msg.(protocol.MsgLayoutSnapshot); ok {
				if len(snap.Columns) == wantCols {
					return snap
				}
			}
		case <-deadline:
			t.Fatalf("timeout waiting for MsgLayoutSnapshot with %d columns", wantCols)
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
	_ = recvLayoutColumns(t, tp.ServerSend, 3, 2*time.Second)

	// Test GrowWidth verb
	focusedID := 1
	initialCols, _, ok := srv.PaneSize(focusedID)
	if !ok {
		t.Fatalf("pane %d not found", focusedID)
	}

	tp.SendClient(ctx, protocol.MsgVerb{Verb: protocol.VerbGrowWidth, PaneID: focusedID})
	recvLayoutWidth(t, tp.ServerSend, focusedID, initialCols+10)

	grownCols, _, ok := srv.PaneSize(focusedID)
	if !ok || grownCols != initialCols+10 {
		t.Errorf("after VerbGrowWidth: cols = %d, want %d", grownCols, initialCols+10)
	}

	// Test ShrinkWidth verb
	tp.SendClient(ctx, protocol.MsgVerb{Verb: protocol.VerbShrinkWidth, PaneID: focusedID})
	recvLayoutWidth(t, tp.ServerSend, focusedID, initialCols)

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

func TestServerSpawnsPanesWithWideboiEnvironment(t *testing.T) {
	tp := transport.NewInProcChannel(32)
	envFile := filepath.Join(t.TempDir(), "pane.env")
	srv := server.NewServer(tp, "/bin/sh", "")
	srv.SetSession("test-session", "/tmp/test.sock")
	srv.SetCloseGrace(testGrace)
	srv.SetStartupPanes([]server.StartupPane{
		{Command: fmt.Sprintf("env > %q; exec sleep 30", envFile)},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Run(ctx) }()
	defer srv.Close()

	tp.SendClient(ctx, protocol.MsgAttach{Cols: 80, Rows: 24})
	_ = recvLayoutSnapshot(t, tp.ServerSend, 2*time.Second)

	deadline := time.Now().Add(3 * time.Second)
	for {
		if data, err := os.ReadFile(envFile); err == nil && len(data) > 0 {
			envStr := string(data)
			lines := strings.Split(envStr, "\n")
			lineSet := make(map[string]bool)
			for _, l := range lines {
				lineSet[l] = true
			}
			for _, want := range []string{
				"WIDEBOI=1",
				"LC_WIDEBOI=1",
				"WIDEBOI_PANE_ID=1",
				"WIDEBOI_SESSION=test-session",
				"WIDEBOI_SOCK=/tmp/test.sock",
			} {
				if !lineSet[want] {
					t.Errorf("pane env missing %q, got:\n%s", want, envStr)
				}
			}
			if wantTP := os.Getenv("TERM_PROGRAM"); wantTP != "" {
				if !lineSet["TERM_PROGRAM="+wantTP] {
					t.Errorf("pane env should preserve inherited TERM_PROGRAM=%s", wantTP)
				}
			} else if lineSet["TERM_PROGRAM=wideboi"] {
				t.Errorf("pane env must not inject TERM_PROGRAM=wideboi when not inherited")
			}
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("pane env file not written in time")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestServerBellNotification(t *testing.T) {
	tp := transport.NewInProcChannel(32)
	srv := server.NewServer(tp, "/bin/sh", "")
	srv.SetCloseGrace(testGrace)
	srv.SetStartupPanes([]server.StartupPane{
		{Command: "exec sleep 30"},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Run(ctx) }()
	defer srv.Close()

	tp.SendClient(ctx, protocol.MsgAttach{Cols: 80, Rows: 24})
	_ = recvLayoutSnapshot(t, tp.ServerSend, 2*time.Second)

	grid := srv.PaneGrid(1)
	if grid == nil {
		t.Fatal("grid for pane 1 not found")
	}

	_, err := grid.Write([]byte("\a"))
	if err != nil {
		t.Fatalf("grid.Write: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		select {
		case msg := <-tp.ServerSend:
			if notif, ok := msg.(protocol.MsgPaneNotification); ok {
				if notif.PaneID != 1 {
					t.Errorf("PaneID = %d, want 1", notif.PaneID)
				}
				if notif.Message != "Alert" {
					t.Errorf("Message = %q, want Alert", notif.Message)
				}
				return
			}
		case <-time.After(50 * time.Millisecond):
			if time.Now().After(deadline) {
				t.Fatal("timeout waiting for MsgPaneNotification")
			}
		}
	}
}

// showClipboard asks the server for its stored clipboard write and
// returns the response, skipping whatever else the channel carries.
func showClipboard(t *testing.T, ctx context.Context, tp *transport.InProcChannel) protocol.MsgShowClipboardResponse {
	t.Helper()
	tp.SendClient(ctx, protocol.MsgShowClipboardRequest{})
	timeout := time.After(2 * time.Second)
	for {
		select {
		case msg := <-tp.ServerSend:
			if resp, ok := msg.(protocol.MsgShowClipboardResponse); ok {
				return resp
			}
		case <-timeout:
			t.Fatal("timeout waiting for MsgShowClipboardResponse")
		}
	}
}

func TestServerStoresPaneClipboard(t *testing.T) {
	tp := transport.NewInProcChannel(64)
	srv := server.NewServer(tp, "/bin/sh", "")
	srv.SetCloseGrace(testGrace)
	srv.SetStartupPanes([]server.StartupPane{
		{Command: "exec sleep 30"},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Run(ctx) }()
	defer srv.Close()

	tp.SendClient(ctx, protocol.MsgAttach{Cols: 80, Rows: 24})
	_ = recvLayoutSnapshot(t, tp.ServerSend, 2*time.Second)

	if resp := showClipboard(t, ctx, tp); resp.Error == "" {
		t.Fatalf("show-clipboard before any copy = %+v, want an error", resp)
	}

	grid := srv.PaneGrid(1)
	if grid == nil {
		t.Fatal("grid for pane 1 not found")
	}
	// Zmlyc3Q= is "first", c2Vjb25k is "second": the later write wins.
	if _, err := grid.Write([]byte("\x1b]52;c;Zmlyc3Q=\x07\x1b]52;c;c2Vjb25k\x07")); err != nil {
		t.Fatalf("grid.Write: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		resp := showClipboard(t, ctx, tp)
		if resp.Text == "second" {
			if resp.PaneID != 1 || resp.Title == "" || resp.UnixMilli == 0 || resp.Error != "" {
				t.Fatalf("show-clipboard = %+v, want pane 1 with a title and time", resp)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("show-clipboard never returned the second write; last %+v", resp)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// clipTap drains a client's server channel for the life of ctx -- an
// InProcChannel drops sends when full, and pane updates would fill it --
// keeping only clipboard writes and show-clipboard responses.
type clipTap struct {
	tp    *transport.InProcChannel
	clips chan protocol.MsgPaneClipboard
	resps chan protocol.MsgShowClipboardResponse
}

func newClipTap(ctx context.Context, tp *transport.InProcChannel) *clipTap {
	c := &clipTap{
		tp:    tp,
		clips: make(chan protocol.MsgPaneClipboard, 16),
		resps: make(chan protocol.MsgShowClipboardResponse, 16),
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case msg := <-tp.ServerSend:
				switch m := msg.(type) {
				case protocol.MsgPaneClipboard:
					c.clips <- m
				case protocol.MsgShowClipboardResponse:
					c.resps <- m
				}
			}
		}
	}()
	return c
}

// barrier round-trips a request through the tap's client. The server
// handles one client's messages in order, so on return everything that
// client sent before has been processed.
func (c *clipTap) barrier(t *testing.T, ctx context.Context) {
	t.Helper()
	c.tp.SendClient(ctx, protocol.MsgShowClipboardRequest{})
	select {
	case <-c.resps:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for MsgShowClipboardResponse")
	}
}

// clipboardServer starts a server with one pane and two attached
// in-process clients, and returns a tap on each. The caller writes OSC 52 to srv.PaneGrid(1).
func clipboardServer(t *testing.T, ctx context.Context) (srv *server.Server, tap1, tap2 *clipTap) {
	t.Helper()
	tp1 := transport.NewInProcChannel(256)
	srv = server.NewServer(tp1, "/bin/sh", "")
	srv.SetCloseGrace(testGrace)
	srv.SetStartupPanes([]server.StartupPane{{Command: "exec sleep 30"}})
	go func() { _ = srv.Run(ctx) }()
	t.Cleanup(func() { srv.Close() })

	tp1.SendClient(ctx, protocol.MsgAttach{Cols: 80, Rows: 24})
	_ = recvLayoutSnapshot(t, tp1.ServerSend, 2*time.Second)

	tp2 := transport.NewInProcChannel(256)
	srv.AddClientForTest(ctx, tp2)
	tp2.SendClient(ctx, protocol.MsgAttach{Cols: 80, Rows: 24})
	_ = recvLayoutSnapshot(t, tp2.ServerSend, 2*time.Second)

	return srv, newClipTap(ctx, tp1), newClipTap(ctx, tp2)
}

// writeOSC52Hello makes pane 1 copy "hello".
func writeOSC52Hello(t *testing.T, srv *server.Server) {
	t.Helper()
	if _, err := srv.PaneGrid(1).Write([]byte("\x1b]52;c;aGVsbG8=\x07")); err != nil {
		t.Fatalf("grid.Write: %v", err)
	}
}

func expectClipboard(t *testing.T, name string, ch <-chan protocol.MsgPaneClipboard) {
	t.Helper()
	select {
	case c := <-ch:
		if c.PaneID != 1 || c.Text != "hello" || c.Title == "" {
			t.Fatalf("%s got %+v, want pane 1 'hello' with a title", name, c)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("%s never received the clipboard write", name)
	}
}

// expectNoClipboard is a negative check, so it has to wait out a window
// rather than a condition; it runs only after a positive delivery to
// another client, by which time a send to this one would have happened.
func expectNoClipboard(t *testing.T, name string, ch <-chan protocol.MsgPaneClipboard) {
	t.Helper()
	select {
	case c := <-ch:
		t.Fatalf("%s received %+v, want nothing", name, c)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestPaneClipboardGoesToLastInputClient(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, tap1, tap2 := clipboardServer(t, ctx)

	tap2.tp.SendClient(ctx, protocol.MsgInput{PaneID: 1, Data: []byte(" ")})
	tap2.barrier(t, ctx)
	writeOSC52Hello(t, srv)

	expectClipboard(t, "tp2 (last input)", tap2.clips)
	expectNoClipboard(t, "tp1", tap1.clips)
}

func TestPaneClipboardMouseCountsAsInput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, tap1, tap2 := clipboardServer(t, ctx)

	tap2.tp.SendClient(ctx, protocol.MsgMouse{PaneID: 1, Kind: protocol.MousePress, X: 1, Y: 1})
	tap2.barrier(t, ctx)
	writeOSC52Hello(t, srv)

	expectClipboard(t, "tp2 (last mouse)", tap2.clips)
	expectNoClipboard(t, "tp1", tap1.clips)
}

// Motion and wheel are not a person choosing a pane: scrolling a
// mouse-tracking pane from another client must not steal its copies.
func TestPaneClipboardIgnoresMouseMotionAndWheel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, tap1, tap2 := clipboardServer(t, ctx)

	tap2.tp.SendClient(ctx, protocol.MsgMouse{PaneID: 1, Kind: protocol.MousePress, X: 1, Y: 1})
	tap2.barrier(t, ctx)
	tap1.tp.SendClient(ctx, protocol.MsgMouse{PaneID: 1, Kind: protocol.MouseMotion, X: 2, Y: 2})
	tap1.tp.SendClient(ctx, protocol.MsgMouse{PaneID: 1, Kind: protocol.MouseWheel, X: 2, Y: 2, Button: 64})
	tap1.tp.SendClient(ctx, protocol.MsgMouse{PaneID: 1, Kind: protocol.MouseRelease, X: 2, Y: 2})
	tap1.barrier(t, ctx)
	writeOSC52Hello(t, srv)

	expectClipboard(t, "tp2 (last press)", tap2.clips)
	expectNoClipboard(t, "tp1 (motion/wheel/release only)", tap1.clips)
}

func TestPaneClipboardFallsBackToAttachedClients(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, tap1, tap2 := clipboardServer(t, ctx)

	writeOSC52Hello(t, srv)

	expectClipboard(t, "tp1", tap1.clips)
	expectClipboard(t, "tp2", tap2.clips)
}

func TestPaneClipboardSkipsUnattachedTransports(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, tap1, _ := clipboardServer(t, ctx)

	// A CLI command connection: registered, never attached.
	tp3 := transport.NewInProcChannel(256)
	srv.AddClientForTest(ctx, tp3)
	tap3 := newClipTap(ctx, tp3)

	writeOSC52Hello(t, srv)

	expectClipboard(t, "tp1", tap1.clips)
	expectNoClipboard(t, "tp3 (unattached)", tap3.clips)
}

func TestPaneClipboardIgnoresSendInputRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, tap1, tap2 := clipboardServer(t, ctx)

	// Automation driving a pane is not a person who just copied.
	tap2.tp.SendClient(ctx, protocol.MsgSendInputRequest{PaneID: 1, Data: []byte(" ")})
	tap2.barrier(t, ctx)
	writeOSC52Hello(t, srv)

	expectClipboard(t, "tp1", tap1.clips)
	expectClipboard(t, "tp2", tap2.clips)
}

func TestPaneClipboardLastInputClearedOnDisconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, tap1, tap2 := clipboardServer(t, ctx)

	tap2.tp.SendClient(ctx, protocol.MsgInput{PaneID: 1, Data: []byte(" ")})
	tap2.barrier(t, ctx)
	writeOSC52Hello(t, srv)
	expectClipboard(t, "tp2 (last input)", tap2.clips)

	close(tap2.tp.ClientSend) // the connection ends

	// Until the drop is processed the write still targets tp2, so
	// repeat it until tp1 -- the fallback -- sees one.
	deadline := time.After(2 * time.Second)
	for {
		writeOSC52Hello(t, srv)
		select {
		case c := <-tap1.clips:
			if c.Text != "hello" {
				t.Fatalf("tp1 got %+v", c)
			}
			return
		case <-time.After(50 * time.Millisecond):
		case <-deadline:
			t.Fatal("tp1 never received a write after tp2 disconnected")
		}
	}
}

// A pane can emit copies far faster than they are delivered. Delivery
// must stay bounded -- at most one in flight and one waiting, the newest
// -- and the last copy must be the one that lands.
func TestPaneClipboardBurstIsBoundedAndLatestWins(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, tap1, _ := clipboardServer(t, ctx)

	// A stalled client: every clipboard send to it runs out its timeout.
	slow := &stallingTransport{InProcChannel: transport.NewInProcChannel(256)}
	srv.AddClientForTest(ctx, slow)
	slow.SendClient(ctx, protocol.MsgAttach{Cols: 80, Rows: 24})
	_ = recvLayoutSnapshot(t, slow.ServerSend, 2*time.Second)
	go func() { // keep its ordinary traffic drained
		for {
			select {
			case <-ctx.Done():
				return
			case <-slow.ServerSend:
			}
		}
	}()

	var burst strings.Builder
	for i := 0; i < 1000; i++ {
		text := fmt.Sprintf("copy-%04d-%s", i, strings.Repeat("x", 4096))
		fmt.Fprintf(&burst, "\x1b]52;c;%s\x07", base64.StdEncoding.EncodeToString([]byte(text)))
	}
	before := runtime.NumGoroutine()
	if _, err := srv.PaneGrid(1).Write([]byte(burst.String())); err != nil {
		t.Fatalf("grid.Write: %v", err)
	}
	if grew := runtime.NumGoroutine() - before; grew > 10 {
		t.Errorf("a burst of 1000 copies left %d extra goroutines; delivery should be bounded", grew)
	}

	deadline := time.After(5 * time.Second)
	for {
		select {
		case c := <-tap1.clips:
			if strings.HasPrefix(c.Text, "copy-0999-") {
				if resp := showClipboardVia(t, ctx, tap1); !strings.HasPrefix(resp.Text, "copy-0999-") {
					t.Fatalf("show-clipboard holds %.10q, want the last copy", resp.Text)
				}
				return
			}
		case <-deadline:
			t.Fatal("the last copy of the burst never arrived")
		}
	}
}

// stallingTransport blocks every clipboard send until its context
// expires, as a socket client that has stopped reading does.
type stallingTransport struct {
	*transport.InProcChannel
}

func (s *stallingTransport) SendServer(ctx context.Context, msg transport.ServerMessage) bool {
	if _, ok := msg.(protocol.MsgPaneClipboard); ok {
		<-ctx.Done()
		return false
	}
	return s.InProcChannel.SendServer(ctx, msg)
}

// showClipboardVia is showClipboard for a client whose channel a tap owns.
func showClipboardVia(t *testing.T, ctx context.Context, c *clipTap) protocol.MsgShowClipboardResponse {
	t.Helper()
	c.tp.SendClient(ctx, protocol.MsgShowClipboardRequest{})
	select {
	case r := <-c.resps:
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for MsgShowClipboardResponse")
	}
	return protocol.MsgShowClipboardResponse{}
}
