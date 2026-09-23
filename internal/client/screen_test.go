package client

import (
	"image"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/client/compose"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

// fakeHostScreen is a HostScreen backed by an off-screen surface, so a
// test can call Draw and then read the cells back.
//
// This exists because Draw used to take *uv.TerminalScreen, a type no
// unit test could cheaply build. Nothing tested Draw as a result, which
// is how a wipe between two blank frames shipped green.
type fakeHostScreen struct {
	compose.Surface
	cursorShown bool
	cursorX     int
	cursorY     int
}

func newFakeHostScreen(cols, rows int) *fakeHostScreen {
	return &fakeHostScreen{Surface: compose.NewSurface(cols, rows)}
}

func (f *fakeHostScreen) HideCursor()                { f.cursorShown = false }
func (f *fakeHostScreen) ShowCursor()                { f.cursorShown = true }
func (f *fakeHostScreen) SetCursorPosition(x, y int) { f.cursorX, f.cursorY = x, y }

// text returns the whole screen as lines, status row included.
func (f *fakeHostScreen) text() []string {
	b := f.Bounds()
	return compose.Text(f, image.Rect(0, 0, b.Dx(), b.Dy()))
}

// textAbove returns rows 0..limit-1, which is the region a composed
// frame covers. The status bar lives on the last row and is drawn
// outside the frame, so tests about pane content exclude it.
func (f *fakeHostScreen) textAbove(limit int) []string {
	b := f.Bounds()
	return compose.Text(f, image.Rect(0, 0, b.Dx(), limit))
}

// blankAbove reports whether every cell in rows 0..limit-1 is empty.
func blankAbove(scr *fakeHostScreen, limit int) bool {
	for _, line := range scr.textAbove(limit) {
		if strings.TrimSpace(line) != "" {
			return false
		}
	}
	return true
}

// twoColumns is the layout both pane fixtures below describe: two
// equal panes side by side, each narrower than the viewport.
func twoColumns() []*protocol.ColumnData {
	return []*protocol.ColumnData{
		&protocol.ColumnData{PaneId: int32(1), Width: 25, Height: 10},
		&protocol.ColumnData{PaneId: int32(2), Width: 25, Height: 10},
	}
}

// paneUpdate builds a MsgPaneUpdate whose first row is text, so a test
// can tell which pane it is looking at on the composited screen.
func paneUpdate(paneID, cols, rows int, text string) *protocol.ServerEnvelope {
	line := make([]*protocol.CellData, 0, len(text))
	for _, r := range text {
		line = append(line, &protocol.CellData{Content: string(r), Width: 1})
	}
	lines := make([]*protocol.LineData, rows)
	lines[0] = &protocol.LineData{Cells: line}
	return &protocol.ServerEnvelope{Payload: &protocol.ServerEnvelope_PaneUpdate{PaneUpdate: &protocol.MsgPaneUpdate{
		PaneId:        int32(paneID),
		Cols:          int32(cols),
		Rows:          int32(rows),
		Lines:         lines,
		CursorX:       0,
		CursorY:       0,
		CursorVisible: true,
	}}}
}

// newTestClientWithTwoPanes returns a client holding populated mirrors
// for panes 1 and 2, focused on pane 1.
//
// Pane content comes from the mirrors, which a test can populate
// without standing up a server.
func newTestClientWithTwoPanes(t *testing.T, cols, rows int) *Client {
	t.Helper()
	cli := NewClient(transport.NewInProcChannel(16), cols, rows, "C-b")
	cli.HandleServerMsg(&protocol.ServerEnvelope{Payload: &protocol.ServerEnvelope_LayoutSnapshot{LayoutSnapshot: &protocol.MsgLayoutSnapshot{
		Columns:     twoColumns(),
		FocusPaneId: 1,
		Layout:      protocol.LayoutMode_LAYOUT_SCROLL,
	}}})
	cli.HandleServerMsg(paneUpdate(1, 25, 10, "PANE-ONE"))
	cli.HandleServerMsg(paneUpdate(2, 25, 10, "PANE-TWO"))
	return cli
}

// The point of this test is not its assertions, which are mild -- it is
// that Draw is callable at all. Every other test in this file and in
// draw_wipe_test.go depends on that.
func TestDrawRendersPaneContent(t *testing.T) {
	const cols, rows = 60, 12
	cli := newTestClientWithTwoPanes(t, cols, rows)

	scr := newFakeHostScreen(cols, rows)
	cli.Draw(scr)

	got := strings.Join(scr.text(), "\n")
	for _, want := range []string{"PANE-ONE", "PANE-TWO"} {
		if !strings.Contains(got, want) {
			t.Errorf("composited screen is missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "focus: [pane 1") {
		t.Errorf("composited screen is missing the status bar:\n%s", got)
	}
}

func TestClientDrawDirtyDetection(t *testing.T) {
	const cols, rows = 60, 12
	cli := newTestClientWithTwoPanes(t, cols, rows)
	scr := newFakeHostScreen(cols, rows)

	// First draw must report dirty / changed.
	dirty := cli.Draw(scr)
	if !dirty {
		t.Fatal("first Draw: expected dirty=true, got false")
	}

	// Second draw with no state change must report clean / unchanged.
	dirty = cli.Draw(scr)
	if dirty {
		t.Fatal("second identical Draw: expected dirty=false, got true")
	}

	// Passing a new HostScreen target must report dirty.
	scr2 := newFakeHostScreen(cols, rows)
	dirty = cli.Draw(scr2)
	if !dirty {
		t.Fatal("Draw with new HostScreen: expected dirty=true, got false")
	}

	// Updating a pane should cause next Draw to report dirty.
	cli.HandleServerMsg(paneUpdate(1, 25, 10, "PANE-ONE-CHANGED"))
	dirty = cli.Draw(scr2)
	if !dirty {
		t.Fatal("Draw after pane update: expected dirty=true, got false")
	}

	// Subsequent draw without change should report clean again.
	dirty = cli.Draw(scr2)
	if dirty {
		t.Fatal("Draw after clean pane: expected dirty=false, got true")
	}

	// Control mode toggle should report dirty.
	cli.SetControlMode(true)
	dirty = cli.Draw(scr2)
	if !dirty {
		t.Fatal("Draw after control mode enabled: expected dirty=true, got false")
	}

	dirty = cli.Draw(scr2)
	if dirty {
		t.Fatal("Draw with control mode unchanged: expected dirty=false, got true")
	}

	cli.SetControlMode(false)
	dirty = cli.Draw(scr2)
	if !dirty {
		t.Fatal("Draw after control mode disabled: expected dirty=true, got false")
	}

	// Verify scr is completely untouched when Draw returns false.
	markerCell := uv.NewCell(scr2.WidthMethod(), "Z")
	scr2.SetCell(0, 0, markerCell)
	dirty = cli.Draw(scr2)
	if dirty {
		t.Fatal("expected dirty=false on unchanged state")
	}
	if got := scr2.CellAt(0, 0); got == nil || got.Content != "Z" {
		t.Fatalf("expected scr2(0,0) to remain untouched with 'Z', got %v", got)
	}

	// Verify cursor position change reports dirty.
	cli.HandleServerMsg(&protocol.ServerEnvelope{Payload: &protocol.ServerEnvelope_PaneUpdate{PaneUpdate: &protocol.MsgPaneUpdate{
		PaneId:        1,
		Cols:          25,
		Rows:          10,
		Lines:         paneUpdate(1, 25, 10, "PANE-ONE-CHANGED").GetPaneUpdate().Lines,
		CursorX:       5,
		CursorY:       2,
		CursorVisible: true,
	}}})
	dirty = cli.Draw(scr2)
	if !dirty {
		t.Fatal("Draw after cursor position change: expected dirty=true, got false")
	}
	if scr2.cursorX != 5 || scr2.cursorY != 3 { // Y=3 because header is row 0, pane begins at Y=1
		t.Fatalf("expected host cursor position (5, 3), got (%d, %d)", scr2.cursorX, scr2.cursorY)
	}

	// Verify cursor visibility change reports dirty.
	cli.HandleServerMsg(&protocol.ServerEnvelope{Payload: &protocol.ServerEnvelope_PaneUpdate{PaneUpdate: &protocol.MsgPaneUpdate{
		PaneId:        1,
		Cols:          25,
		Rows:          10,
		Lines:         paneUpdate(1, 25, 10, "PANE-ONE-CHANGED").GetPaneUpdate().Lines,
		CursorX:       5,
		CursorY:       2,
		CursorVisible: false,
	}}})
	dirty = cli.Draw(scr2)
	if !dirty {
		t.Fatal("Draw after cursor visibility change: expected dirty=true, got false")
	}
	if scr2.cursorShown {
		t.Fatal("expected host cursor to be hidden")
	}

	// Settled again: should be clean.
	dirty = cli.Draw(scr2)
	if dirty {
		t.Fatal("expected clean draw after cursor settled")
	}
}

func TestClientDrawDirtyDetectionDuringMotion(t *testing.T) {
	const cols, rows = 90, 12
	cli := newMotionClient(t, cols, rows)
	scr := newFakeHostScreen(cols, rows)

	// Initial render
	if !cli.Draw(scr) {
		t.Fatal("initial draw: expected dirty=true")
	}
	if cli.Draw(scr) {
		t.Fatal("subsequent draw: expected dirty=false")
	}

	// Trigger motion by switching focus in card layout
	focusTo(cli, 2)

	cli.mu.Lock()
	animating := cli.motion != nil
	cli.mu.Unlock()
	if !animating {
		t.Fatal("expected motion to be armed")
	}

	// Each animation step should report dirty=true
	for step := 0; step < motionFrames; step++ {
		dirty := cli.Draw(scr)
		if !dirty {
			t.Fatalf("step %d of motion: expected dirty=true, got false", step)
		}
	}

	// Post-animation frame (cursor becomes visible) should report dirty=true
	dirty := cli.Draw(scr)
	if !dirty {
		t.Fatal("post-animation frame: expected dirty=true for cursor reveal, got false")
	}

	// Settled frame after motion should report dirty=false
	dirty = cli.Draw(scr)
	if dirty {
		t.Fatal("settled post-motion frame: expected dirty=false, got true")
	}
}
