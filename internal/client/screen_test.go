package client

import (
	"image"
	"strings"
	"testing"

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
func twoColumns() []protocol.ColumnData {
	return []protocol.ColumnData{
		{PaneID: 1, Width: 25, Height: 10},
		{PaneID: 2, Width: 25, Height: 10},
	}
}

// paneUpdate builds a MsgPaneUpdate whose first row is text, so a test
// can tell which pane it is looking at on the composited screen.
func paneUpdate(paneID, cols, rows int, text string) protocol.MsgPaneUpdate {
	line := make(protocol.LineData, 0, len(text))
	for _, r := range text {
		line = append(line, protocol.CellData{Content: string(r), Width: 1})
	}
	lines := make([]protocol.LineData, rows)
	lines[0] = line
	return protocol.MsgPaneUpdate{
		PaneID:        paneID,
		Cols:          cols,
		Rows:          rows,
		Lines:         lines,
		CursorX:       0,
		CursorY:       0,
		CursorVisible: true,
	}
}

// newTestClientWithTwoPanes returns a client holding populated mirrors
// for panes 1 and 2, focused on pane 1.
//
// It drives the attach path (drawPane nil), because that is the one a
// test can supply content for without standing up a server.
func newTestClientWithTwoPanes(t *testing.T, cols, rows int) *Client {
	t.Helper()
	cli := NewClient(transport.NewInProcChannel(16), cols, rows, "C-b")
	cli.HandleServerMsg(protocol.MsgLayoutSnapshot{
		Columns:     twoColumns(),
		FocusPaneID: 1,
	})
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
	cli.Draw(scr, nil, nil)

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
