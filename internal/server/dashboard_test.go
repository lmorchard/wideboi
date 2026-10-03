package server

import (
	"context"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

func TestDashboardRender(t *testing.T) {
	db := NewDashboard()
	panes := []PaneInfo{
		{ID: 1, Status: protocol.StatusIdle, Title: "bash", CWD: "/Users/test/wideboi"},
		{ID: 2, Status: protocol.StatusWorking, Title: "make test", CWD: "/Users/test/wideboi/web"},
	}

	out := string(db.Render(panes, 80, 24))

	// Should contain summary banner and table headers
	if !strings.Contains(out, "[ wideboi dashboard ]") || !strings.Contains(out, "1 working") || !strings.Contains(out, "1 idle") {
		t.Fatalf("rendered output missing summary banner:\n%s", out)
	}
	if !strings.Contains(out, "PANE ID") || !strings.Contains(out, "STATUS") || !strings.Contains(out, "TITLE") {
		t.Fatalf("rendered output missing header:\n%s", out)
	}

	// Should contain pane entries
	if !strings.Contains(out, "[1]") || !strings.Contains(out, "bash") || !strings.Contains(out, "/Users/test/wideboi") {
		t.Errorf("rendered output missing pane 1 details:\n%s", out)
	}
	if !strings.Contains(out, "[2]") || !strings.Contains(out, "make test") || !strings.Contains(out, "/Users/test/wideboi/web") {
		t.Errorf("rendered output missing pane 2 details:\n%s", out)
	}

	// Priority sort puts Working (pane 2) before Idle (pane 1), so initial selection is on pane 2
	if db.SelectedPaneID() != 2 {
		t.Errorf("SelectedPaneID = %d, want 2 (higher priority)", db.SelectedPaneID())
	}
}

func TestDashboardNavigation(t *testing.T) {
	db := NewDashboard()
	panes := []PaneInfo{
		{ID: 10, Status: protocol.StatusIdle, Title: "one"},
		{ID: 20, Status: protocol.StatusWorking, Title: "two"},
		{ID: 30, Status: protocol.StatusDone, Title: "three"},
	}
	db.Render(panes, 80, 24)

	// Sorted priority puts Done (30) first, then Working (20), then Idle (10)
	if got := db.SelectedPaneID(); got != 30 {
		t.Fatalf("initial selection = %d, want 30", got)
	}

	// Move down with 'j'
	target, handled := db.HandleKey(uv.KeyPressEvent{Code: 'j'})
	if !handled || target != 0 {
		t.Errorf("HandleKey('j'): target=%d, handled=%v", target, handled)
	}
	if got := db.SelectedPaneID(); got != 20 {
		t.Errorf("after 'j': selection = %d, want 20", got)
	}

	// Move down with Down arrow
	target, handled = db.HandleKey(uv.KeyPressEvent{Code: uv.KeyDown})
	if !handled || target != 0 {
		t.Errorf("HandleKey(Down): target=%d, handled=%v", target, handled)
	}
	if got := db.SelectedPaneID(); got != 10 {
		t.Errorf("after Down: selection = %d, want 10", got)
	}

	// Move down clamped
	db.HandleKey(uv.KeyPressEvent{Code: 'j'})
	if got := db.SelectedPaneID(); got != 10 {
		t.Errorf("after clamped 'j': selection = %d, want 10", got)
	}

	// Press Enter to select
	target, handled = db.HandleKey(uv.KeyPressEvent{Code: uv.KeyEnter})
	if !handled || target != 10 {
		t.Errorf("HandleKey(Enter): target = %d, want 10", target)
	}

	// Move up with 'k'
	db.HandleKey(uv.KeyPressEvent{Code: 'k'})
	if got := db.SelectedPaneID(); got != 20 {
		t.Errorf("after 'k': selection = %d, want 20", got)
	}

	// Move up with Up arrow
	db.HandleKey(uv.KeyPressEvent{Code: uv.KeyUp})
	if got := db.SelectedPaneID(); got != 30 {
		t.Errorf("after Up: selection = %d, want 30", got)
	}

	// Move up clamped
	db.HandleKey(uv.KeyPressEvent{Code: 'k'})
	if got := db.SelectedPaneID(); got != 30 {
		t.Errorf("after clamped 'k': selection = %d, want 30", got)
	}
}

func TestDashboardMouseClick(t *testing.T) {
	db := NewDashboard()
	panes := []PaneInfo{
		{ID: 10, Status: protocol.StatusIdle, Title: "one"},
		{ID: 20, Status: protocol.StatusWorking, Title: "two"},
		{ID: 30, Status: protocol.StatusDone, Title: "three"},
	}
	db.Render(panes, 80, 24)

	// In the sorted list:
	// Row 0 is summary banner, Row 1 is table header.
	// Row 2 is index 0 (pane 30, Done).
	// Row 3 is index 1 (pane 20, Working).
	// Row 4 is index 2 (pane 10, Idle).

	// Clicking row 1 (header) should be ignored
	headerClick := uv.MouseClickEvent{X: 10, Y: 1, Button: uv.MouseLeft}
	if target, handled := db.HandleMouse(headerClick); handled || target != 0 {
		t.Errorf("HandleMouse on header: target=%d, handled=%v, want (0, false)", target, handled)
	}

	// Click row for pane 20 (row 3)
	click := uv.MouseClickEvent{
		X:      10,
		Y:      3,
		Button: uv.MouseLeft,
	}
	target, handled := db.HandleMouse(click)
	if !handled || target != 20 {
		t.Fatalf("HandleMouse(row 3): target=%d, handled=%v, want 20", target, handled)
	}
	if got := db.SelectedPaneID(); got != 20 {
		t.Errorf("after click: selection = %d, want 20", got)
	}

	// Click row for pane 30 (row 2)
	click30 := uv.MouseClickEvent{
		X:      10,
		Y:      2,
		Button: uv.MouseLeft,
	}
	target, handled = db.HandleMouse(click30)
	if !handled || target != 30 {
		t.Fatalf("HandleMouse(row 2): target=%d, handled=%v, want 30", target, handled)
	}
	if got := db.SelectedPaneID(); got != 30 {
		t.Errorf("after click: selection = %d, want 30", got)
	}
}

func TestDashboardEmptyPanes(t *testing.T) {
	db := NewDashboard()
	out := string(db.Render(nil, 80, 24))
	if !strings.Contains(out, "no active terminal panes") {
		t.Errorf("empty dashboard missing placeholder: %s", out)
	}

	// Enter with no panes returns 0
	target, handled := db.HandleKey(uv.KeyPressEvent{Code: uv.KeyEnter})
	if handled || target != 0 {
		t.Errorf("HandleKey(Enter) on empty: target=%d, handled=%v", target, handled)
	}
}

func TestDashboardFocusedMarker(t *testing.T) {
	db := NewDashboard()
	panes := []PaneInfo{
		{ID: 1, Status: protocol.StatusIdle, Title: "bash", Focused: true},
		{ID: 2, Status: protocol.StatusWorking, Title: "build", Focused: false},
	}
	out := string(db.Render(panes, 80, 24))
	if !strings.Contains(out, "[1]*") {
		t.Errorf("expected focused pane 1 to be marked with '*', got:\n%s", out)
	}
	if strings.Contains(out, "[2]*") {
		t.Errorf("unfocused pane 2 should not have '*', got:\n%s", out)
	}
}

func TestDashboardNarrowClipping(t *testing.T) {
	db := NewDashboard()
	panes := []PaneInfo{
		{ID: 1, Status: protocol.StatusIdle, Title: "very-long-terminal-title-exceeding-columns", CWD: "/very/long/path/to/cwd"},
	}
	out := string(db.Render(panes, 20, 24))
	lines := strings.Split(out, "\r\n")
	for _, l := range lines {
		clean := stripANSI(l)
		if len([]rune(clean)) > 20 {
			t.Errorf("line exceeded 20 cols (%d): %q", len([]rune(clean)), clean)
		}
	}
}

func stripANSI(s string) string {
	var out []rune
	inEscape := false
	for _, r := range s {
		if r == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '~' {
				inEscape = false
			}
			continue
		}
		out = append(out, r)
	}
	return string(out)
}

func waitForSnapshot(t *testing.T, ch <-chan transport.ServerMessage, timeout time.Duration) protocol.MsgLayoutSnapshot {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case msg := <-ch:
			if snap, ok := msg.(protocol.MsgLayoutSnapshot); ok {
				return snap
			}
		case <-deadline:
			t.Fatal("timeout waiting for MsgLayoutSnapshot")
		}
	}
}

func TestServerDashboardToggleAndNavigate(t *testing.T) {
	tp := transport.NewInProcChannel(64)
	srv := NewServer(tp, "/bin/sh", "")
	srv.SetCloseGrace(50 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Run(ctx) }()
	defer srv.Close()

	tp.SendClient(ctx, protocol.MsgAttach{Cols: 80, Rows: 24})

	// Wait for initial layout snapshot (2 default panes)
	snap := waitForSnapshot(t, tp.ServerSend, 2*time.Second)
	if len(snap.Columns) != 2 {
		t.Fatalf("expected 2 initial panes, got %d", len(snap.Columns))
	}
	pane1ID := snap.Columns[0].PaneID

	// Send VerbToggleStatus
	tp.SendClient(ctx, protocol.MsgVerb{Verb: protocol.VerbToggleStatus, PaneID: pane1ID})

	// Client should receive MsgFocusPane for the new dashboard pane
	var dbPaneID int
	deadline := time.After(2 * time.Second)
	for dbPaneID == 0 {
		select {
		case msg := <-tp.ServerSend:
			switch m := msg.(type) {
			case protocol.MsgFocusPane:
				dbPaneID = m.PaneID
			}
		case <-deadline:
			t.Fatal("timeout waiting for MsgFocusPane after VerbToggleStatus")
		}
	}

	if dbPaneID == 0 {
		t.Fatal("expected dashboard pane ID > 0")
	}

	// Verify snapshot now has 3 columns (2 terminal + 1 dashboard)
	snap = waitForSnapshot(t, tp.ServerSend, 2*time.Second)
	if len(snap.Columns) != 3 {
		t.Fatalf("expected 3 columns after toggle status, got %d", len(snap.Columns))
	}

	// Send Enter key to dashboard pane
	tp.SendClient(ctx, protocol.MsgInput{PaneID: dbPaneID, Key: protocol.KeyData{Code: 13}})

	// Should receive MsgFocusPane jumping back to pane 1
	var jumpedID int
	deadline = time.After(2 * time.Second)
	for jumpedID == 0 {
		select {
		case msg := <-tp.ServerSend:
			switch m := msg.(type) {
			case protocol.MsgFocusPane:
				jumpedID = m.PaneID
			}
		case <-deadline:
			t.Fatal("timeout waiting for MsgFocusPane after Enter on dashboard")
		}
	}

	if jumpedID != pane1ID {
		t.Errorf("jumped to pane %d, want %d", jumpedID, pane1ID)
	}

	// Test lone dashboard terminates session when terminal panes are killed:
	for _, col := range snap.Columns {
		if col.PaneID != dbPaneID {
			tp.SendClient(ctx, protocol.MsgVerb{Verb: protocol.VerbKillPane, PaneID: col.PaneID})
		}
	}

	// Server should close
	select {
	case <-srv.stopCh:
		// success: server stopped
	case <-time.After(3 * time.Second):
		t.Fatal("server did not shut down after all terminal panes closed")
	}
}

func TestDashboardHandleInput(t *testing.T) {
	db := NewDashboard()
	db.panes = []PaneInfo{
		{ID: 10, Status: protocol.StatusIdle, Title: "first"},
		{ID: 20, Status: protocol.StatusIdle, Title: "second"},
		{ID: 30, Status: protocol.StatusIdle, Title: "third"},
	}

	// 'j' moves down
	target, handled := db.HandleInput(protocol.MsgInput{Data: []byte("j")})
	if !handled || target != 0 || db.selected != 1 {
		t.Errorf("HandleInput('j'): handled=%v, target=%d, selected=%d", handled, target, db.selected)
	}

	// 'k' moves up
	target, handled = db.HandleInput(protocol.MsgInput{Data: []byte("k")})
	if !handled || target != 0 || db.selected != 0 {
		t.Errorf("HandleInput('k'): handled=%v, target=%d, selected=%d", handled, target, db.selected)
	}

	// '\r' activates current selection
	target, handled = db.HandleInput(protocol.MsgInput{Data: []byte("\r")})
	if !handled || target != 10 {
		t.Errorf("HandleInput('\\r'): handled=%v, target=%d, want 10", handled, target)
	}

	// Key encoding
	target, handled = db.HandleInput(protocol.MsgInput{
		Key: protocol.EncodeKey(uv.KeyPressEvent{Code: 'j'}),
	})
	if !handled || db.selected != 1 {
		t.Errorf("HandleInput(encoded 'j'): handled=%v, selected=%d", handled, db.selected)
	}
}

func TestDashboardPrioritySorting(t *testing.T) {
	db := NewDashboard()
	panes := []PaneInfo{
		{ID: 1, Status: protocol.StatusIdle, Title: "idle-1"},
		{ID: 2, Status: protocol.StatusWorking, Title: "working-2"},
		{ID: 3, Status: protocol.StatusNeedsInput, Title: "needs-input-3"},
		{ID: 4, Status: protocol.StatusFailed, Title: "failed-4"},
		{ID: 5, Status: protocol.StatusDone, Title: "done-5"},
		{ID: 6, Status: protocol.StatusNeedsInput, Title: "needs-input-6"},
	}

	out := string(db.Render(panes, 120, 24))

	// Verify fleet summary banner has accurate counts
	if !strings.Contains(out, "2 needs input") ||
		!strings.Contains(out, "1 failed") ||
		!strings.Contains(out, "1 done") ||
		!strings.Contains(out, "1 working") ||
		!strings.Contains(out, "1 idle") {
		t.Errorf("summary banner missing expected status counts:\n%s", out)
	}

	// Verify priority ordering:
	// NeedsInput (3, 6 by ID) -> Failed (4) -> Done (5) -> Working (2) -> Idle (1)
	expectedOrder := []int{3, 6, 4, 5, 2, 1}
	if len(db.panes) != len(expectedOrder) {
		t.Fatalf("len(db.panes) = %d, want %d", len(db.panes), len(expectedOrder))
	}
	for i, wantID := range expectedOrder {
		if db.panes[i].ID != wantID {
			t.Errorf("db.panes[%d].ID = %d, want %d", i, db.panes[i].ID, wantID)
		}
	}
}

func TestDashboardSelectionPreservedAcrossResort(t *testing.T) {
	db := NewDashboard()
	panes := []PaneInfo{
		{ID: 10, Status: protocol.StatusWorking, Title: "ten"},
		{ID: 20, Status: protocol.StatusIdle, Title: "twenty"},
	}
	db.Render(panes, 80, 24)

	// In initial sort: Pane 10 (Working) is index 0; Pane 20 (Idle) is index 1.
	// Select Pane 20 (move down with 'j')
	db.HandleKey(uv.KeyPressEvent{Code: 'j'})
	if got := db.SelectedPaneID(); got != 20 {
		t.Fatalf("selected = %d, want 20", got)
	}

	// Now Pane 20 starts working, and Pane 10 becomes idle.
	// New sort order: Pane 20 (Working) is index 0; Pane 10 (Idle) is index 1.
	updatedPanes := []PaneInfo{
		{ID: 10, Status: protocol.StatusIdle, Title: "ten"},
		{ID: 20, Status: protocol.StatusWorking, Title: "twenty"},
	}
	db.Render(updatedPanes, 80, 24)

	// Selection should remain locked to Pane 20 (now at index 0)
	if got := db.SelectedPaneID(); got != 20 {
		t.Errorf("selection after re-sort = %d, want 20", got)
	}
	if db.selected != 0 {
		t.Errorf("db.selected index after re-sort = %d, want 0", db.selected)
	}
}

func TestDashboardBannerRuneTruncation(t *testing.T) {
	db := NewDashboard()
	panes := []PaneInfo{
		{ID: 1, Status: protocol.StatusNeedsInput, Title: "one"},
		{ID: 2, Status: protocol.StatusWorking, Title: "two"},
		{ID: 3, Status: protocol.StatusIdle, Title: "three"},
	}

	// Test a range of narrow column widths where byte-based truncation would cut through '•'
	for cols := 20; cols <= 60; cols++ {
		out := string(db.Render(panes, cols, 24))
		lines := strings.Split(out, "\r\n")
		for _, l := range lines {
			clean := stripANSI(l)
			// Must not contain unicode replacement char from broken UTF-8
			if strings.ContainsRune(clean, '\uFFFD') {
				t.Fatalf("cols %d: emitted invalid UTF-8 (replacement char): %q", cols, clean)
			}
			if len([]rune(clean)) > cols {
				t.Errorf("cols %d: line length %d exceeded target width: %q", cols, len([]rune(clean)), clean)
			}
		}
	}
}

func TestDashboardVerticalScrolling(t *testing.T) {
	db := NewDashboard()
	var panes []PaneInfo
	for i := 1; i <= 10; i++ {
		panes = append(panes, PaneInfo{
			ID:     i,
			Status: protocol.StatusIdle,
			Title:  strings.Repeat("x", 5),
		})
	}

	// Total rows = 8.
	// Chrome rows = 4 (banner, header, blank, footer).
	// maxVisible = 4 data rows.
	db.Render(panes, 80, 8)

	// Initially, scrollOffset is 0; visible rows are panes 1..4
	if db.scrollOffset != 0 {
		t.Errorf("initial scrollOffset = %d, want 0", db.scrollOffset)
	}

	// Move down 5 times to pane 6 (0-indexed 5)
	for i := 0; i < 5; i++ {
		db.HandleKey(uv.KeyPressEvent{Code: 'j'})
	}
	if got := db.SelectedPaneID(); got != 6 {
		t.Fatalf("SelectedPaneID = %d, want 6", got)
	}

	// Re-render: scrollOffset should adjust so pane 6 is in the visible window
	db.Render(panes, 80, 8)
	if db.scrollOffset < 2 {
		t.Errorf("scrollOffset after moving to pane 6 = %d, want >= 2", db.scrollOffset)
	}

	// Mouse click on row 2 (which is the first visible data row = index db.scrollOffset)
	click := uv.MouseClickEvent{X: 10, Y: 2, Button: uv.MouseLeft}
	target, handled := db.HandleMouse(click)
	wantTarget := panes[db.scrollOffset].ID
	if !handled || target != wantTarget {
		t.Errorf("HandleMouse(Y: 2) with scrollOffset %d: target = %d, want %d", db.scrollOffset, target, wantTarget)
	}
}
