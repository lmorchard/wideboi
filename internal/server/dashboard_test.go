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

	// Should contain table headers
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

	// Initial selection should be on row 0 (pane 1)
	if db.SelectedPaneID() != 1 {
		t.Errorf("SelectedPaneID = %d, want 1", db.SelectedPaneID())
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

	// Initial selection is pane 10
	if got := db.SelectedPaneID(); got != 10 {
		t.Fatalf("initial selection = %d, want 10", got)
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
	if got := db.SelectedPaneID(); got != 30 {
		t.Errorf("after Down: selection = %d, want 30", got)
	}

	// Move down clamped
	db.HandleKey(uv.KeyPressEvent{Code: 'j'})
	if got := db.SelectedPaneID(); got != 30 {
		t.Errorf("after clamped 'j': selection = %d, want 30", got)
	}

	// Press Enter to select
	target, handled = db.HandleKey(uv.KeyPressEvent{Code: uv.KeyEnter})
	if !handled || target != 30 {
		t.Errorf("HandleKey(Enter): target = %d, want 30", target)
	}

	// Move up with 'k'
	db.HandleKey(uv.KeyPressEvent{Code: 'k'})
	if got := db.SelectedPaneID(); got != 20 {
		t.Errorf("after 'k': selection = %d, want 20", got)
	}

	// Move up with Up arrow
	db.HandleKey(uv.KeyPressEvent{Code: uv.KeyUp})
	if got := db.SelectedPaneID(); got != 10 {
		t.Errorf("after Up: selection = %d, want 10", got)
	}

	// Move up clamped
	db.HandleKey(uv.KeyPressEvent{Code: 'k'})
	if got := db.SelectedPaneID(); got != 10 {
		t.Errorf("after clamped 'k': selection = %d, want 10", got)
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

	// Click row for pane 20 (header is row 0, so row 2 is index 1 = pane 20)
	click := uv.MouseClickEvent{
		X:      10,
		Y:      2,
		Button: uv.MouseLeft,
	}
	target, handled := db.HandleMouse(click)
	if !handled || target != 20 {
		t.Fatalf("HandleMouse(row 2): target=%d, handled=%v, want 20", target, handled)
	}
	if got := db.SelectedPaneID(); got != 20 {
		t.Errorf("after click: selection = %d, want 20", got)
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
