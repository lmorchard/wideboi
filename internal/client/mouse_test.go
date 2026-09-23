package client

import (
	"context"
	"image"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

// newMouseClient is a scroll-layout client with two 40-wide panes in a
// 100-column viewport, focused on pane 1, and the channel it sends on.
func newMouseClient(t *testing.T) (*Client, *transport.InProcChannel) {
	t.Helper()
	ch := transport.NewInProcChannel(64)
	cli := NewClient(ch, 100, 24, "C-b")
	cli.SetLayoutMode(protocol.LayoutScroll)
	cli.HandleServerMsg(protocol.MsgLayoutSnapshot{
		Columns: []protocol.ColumnData{
			{PaneID: 1, Width: 40, Height: 22},
			{PaneID: 2, Width: 40, Height: 22},
		},
		FocusPaneID: 1,
	})
	return cli, ch
}

// sent drains everything the client has queued for the server.
func sent(ch *transport.InProcChannel) []transport.ClientMessage {
	var out []transport.ClientMessage
	for {
		select {
		case m := <-ch.ClientSend:
			out = append(out, m)
		default:
			return out
		}
	}
}

func press(x, y int) uv.MouseEvent {
	return uv.MouseClickEvent{X: x, Y: y, Button: uv.MouseLeft}
}

// click presses and releases on the same cell.
func click(cli *Client, x, y int) {
	cli.HandleMouse(context.Background(), press(x, y))
	cli.HandleMouse(context.Background(), release(x, y))
}

func focusRequests(msgs []transport.ClientMessage) []int {
	var ids []int
	for _, m := range msgs {
		if f, ok := m.(protocol.MsgFocusPane); ok {
			ids = append(ids, f.PaneID)
		}
	}
	return ids
}

func TestClickFocusesPaneUnderPointer(t *testing.T) {
	cli, ch := newMouseClient(t)
	p2 := placementFor(cli, 2)
	if p2.Dst.Empty() {
		t.Fatal("pane 2 has no placement; fixture is wrong")
	}

	click(cli, p2.Dst.Min.X+2, p2.Dst.Min.Y+2)

	if got := focusRequests(sent(ch)); len(got) != 1 || got[0] != 2 {
		t.Errorf("focus requests = %v, want [2]", got)
	}
}

// The header row is chrome drawn above the pane, not inside its Dst,
// and it is the most obvious thing to click.
func TestClickOnHeaderRowFocuses(t *testing.T) {
	cli, ch := newMouseClient(t)
	p2 := placementFor(cli, 2)

	cli.HandleMouse(context.Background(), press(p2.Dst.Min.X+2, 0))

	if got := focusRequests(sent(ch)); len(got) != 1 || got[0] != 2 {
		t.Errorf("focus requests = %v, want [2]", got)
	}
}

func TestClickOnFocusedPaneSendsNothing(t *testing.T) {
	cli, ch := newMouseClient(t)
	p1 := placementFor(cli, 1)

	click(cli, p1.Dst.Min.X+2, p1.Dst.Min.Y+2)

	if got := focusRequests(sent(ch)); len(got) != 0 {
		t.Errorf("focus requests = %v, want none", got)
	}
}

// Cards overlap, and the one painted last is the one you see. A click
// on an overlapped cell belongs to it, not to whatever lies beneath.
func TestClickHitsTopmostCard(t *testing.T) {
	ch := transport.NewInProcChannel(64)
	cli := NewClient(ch, 100, 24, "C-b")
	cli.SetLayoutMode(protocol.LayoutCards)
	cli.HandleServerMsg(protocol.MsgLayoutSnapshot{
		Columns: threeColumns(), FocusPaneID: 2,
	})
	p1, p2 := placementFor(cli, 1), placementFor(cli, 2)
	overlap := p1.Dst.Intersect(p2.Dst)
	if overlap.Empty() {
		t.Fatalf("fixture has no overlap: p1=%v p2=%v", p1.Dst, p2.Dst)
	}
	if p2.Z <= p1.Z {
		t.Fatalf("fixture wants pane 2 above pane 1: z1=%d z2=%d", p1.Z, p2.Z)
	}

	click(cli, overlap.Min.X, overlap.Min.Y+1)
	if got := focusRequests(sent(ch)); len(got) != 0 {
		t.Errorf("click on overlap sent focus %v; pane 2 is on top and already focused", got)
	}

	click(cli, p1.Dst.Min.X, p1.Dst.Min.Y+1)
	if got := focusRequests(sent(ch)); len(got) != 1 || got[0] != 1 {
		t.Errorf("click on pane 1's visible edge sent %v, want [1]", got)
	}
}

func TestMouseIgnoredWhileHelpVisible(t *testing.T) {
	cli, ch := newMouseClient(t)
	cli.SetHelpVisible(true)
	p2 := placementFor(cli, 2)

	cli.HandleMouse(context.Background(), press(p2.Dst.Min.X+2, p2.Dst.Min.Y+2))

	if got := sent(ch); len(got) != 0 {
		t.Errorf("sent %v while help was up, want nothing", got)
	}
}

// Nothing is under the status bar or past the last pane.
func TestClickOnEmptySpaceSendsNothing(t *testing.T) {
	cli, ch := newMouseClient(t)

	cli.HandleMouse(context.Background(), press(99, 10))
	cli.HandleMouse(context.Background(), press(10, 23))

	if got := sent(ch); len(got) != 0 {
		t.Errorf("sent %v for clicks on empty space, want nothing", got)
	}
}

func wheel(x, y int, b uv.MouseButton) uv.MouseEvent {
	return uv.MouseWheelEvent{X: x, Y: y, Button: b}
}

func scrolls(msgs []transport.ClientMessage) []protocol.MsgScroll {
	var out []protocol.MsgScroll
	for _, m := range msgs {
		if s, ok := m.(protocol.MsgScroll); ok {
			out = append(out, s)
		}
	}
	return out
}

// The wheel scrolls what is under it, which need not be what has
// focus -- and reading an unfocused pane's history must not steal
// focus from the one being typed into.
func TestWheelScrollsPaneUnderPointer(t *testing.T) {
	cli, ch := newMouseClient(t)
	p2 := placementFor(cli, 2)

	cli.HandleMouse(context.Background(), wheel(p2.Dst.Min.X+2, p2.Dst.Min.Y+2, uv.MouseWheelUp))

	msgs := sent(ch)
	got := scrolls(msgs)
	if len(got) != 1 || got[0] != (protocol.MsgScroll{PaneID: 2, Delta: wheelStep}) {
		t.Errorf("scrolls = %v, want [{2 %d}]", got, wheelStep)
	}
	if f := focusRequests(msgs); len(f) != 0 {
		t.Errorf("wheel sent focus requests %v, want none", f)
	}
}

func TestWheelDownScrollsForward(t *testing.T) {
	cli, ch := newMouseClient(t)
	p1 := placementFor(cli, 1)

	cli.HandleMouse(context.Background(), wheel(p1.Dst.Min.X+2, p1.Dst.Min.Y+2, uv.MouseWheelDown))

	got := scrolls(sent(ch))
	if len(got) != 1 || got[0] != (protocol.MsgScroll{PaneID: 1, Delta: -wheelStep}) {
		t.Errorf("scrolls = %v, want [{1 %d}]", got, -wheelStep)
	}
}

// A sliver shows chrome, not history, and the header row is not
// content: neither has anything to scroll.
//
// The sliver is injected rather than laid out: since cards began
// overlapping (#65) no strategy emits PlacementSliver, but the
// renderer still honours it, so the mouse should too.
func TestWheelOverSliverOrHeaderDoesNothing(t *testing.T) {
	cli, ch := newMouseClient(t)
	p2 := placementFor(cli, 2)

	cli.HandleMouse(context.Background(), wheel(p2.Dst.Min.X+2, 0, uv.MouseWheelUp))
	if got := sent(ch); len(got) != 0 {
		t.Errorf("wheel on header sent %v, want nothing", got)
	}

	cli.mu.Lock()
	for i := range cli.placements {
		if cli.placements[i].PaneID == 2 {
			cli.placements[i].Kind = protocol.PlacementSliver
		}
	}
	cli.mu.Unlock()

	cli.HandleMouse(context.Background(), wheel(p2.Dst.Min.X+2, p2.Dst.Min.Y+2, uv.MouseWheelUp))
	if got := sent(ch); len(got) != 0 {
		t.Errorf("wheel on sliver sent %v, want nothing", got)
	}
}

// paneLines is a pane update carrying one string per row. A rune
// followed by "\x00" is treated as double width: the glyph cell gets
// Width 2 and the next cell is its placeholder, as the server sends it.
func paneLines(paneID, cols, rows int, rowsText ...string) protocol.MsgPaneUpdate {
	lines := make([]protocol.LineData, rows)
	for y, text := range rowsText {
		var line protocol.LineData
		rs := []rune(text)
		for i := 0; i < len(rs); i++ {
			if i+1 < len(rs) && rs[i+1] == 0 {
				line = append(line, protocol.CellData{Content: string(rs[i]), Width: 2})
				i++
				continue
			}
			line = append(line, protocol.CellData{Content: string(rs[i]), Width: 1})
		}
		lines[y] = line
	}
	return protocol.MsgPaneUpdate{PaneID: paneID, Cols: cols, Rows: rows, Lines: lines}
}

func moveTo(x, y int) uv.MouseEvent {
	return uv.MouseMotionEvent{X: x, Y: y, Button: uv.MouseLeft}
}

func release(x, y int) uv.MouseEvent {
	return uv.MouseReleaseEvent{X: x, Y: y, Button: uv.MouseLeft}
}

// drag presses at from, moves to to, releases there, and returns what
// the client wants copied.
func drag(cli *Client, from, to image.Point) string {
	ctx := context.Background()
	cli.HandleMouse(ctx, press(from.X, from.Y))
	cli.HandleMouse(ctx, moveTo(to.X, to.Y))
	return cli.HandleMouse(ctx, release(to.X, to.Y))
}

// newSelectClient is newMouseClient with known text in both panes,
// drawn once so the composed screen holds it.
func newSelectClient(t *testing.T) (*Client, *transport.InProcChannel, *fakeHostScreen) {
	t.Helper()
	cli, ch := newMouseClient(t)
	cli.HandleServerMsg(paneLines(1, 40, 22, "HELLO WORLD", "SECOND LINE", "THIRD"))
	cli.HandleServerMsg(paneLines(2, 40, 22, "NEIGHBOUR TEXT", "MORE NEIGHBOUR"))
	scr := newFakeHostScreen(100, 24)
	cli.Draw(scr)
	return cli, ch, scr
}

// Stream selection, like every terminal: the tail of the first row,
// the head of the last. Backwards drags read the same as forwards.
func TestDragSelectsAndReturnsText(t *testing.T) {
	cli, _, _ := newSelectClient(t)
	d := placementFor(cli, 1).Dst

	got := drag(cli, image.Pt(d.Min.X+6, d.Min.Y), image.Pt(d.Min.X+2, d.Min.Y+1))
	if got != "WORLD\nSEC" {
		t.Errorf("forward drag copied %q, want %q", got, "WORLD\nSEC")
	}

	got = drag(cli, image.Pt(d.Min.X+2, d.Min.Y+1), image.Pt(d.Min.X+6, d.Min.Y))
	if got != "WORLD\nSEC" {
		t.Errorf("backward drag copied %q, want %q", got, "WORLD\nSEC")
	}
}

// Rows between the ends are taken whole, and trailing blanks -- the
// empty rest of a 40-wide row -- are not copied.
func TestDragAcrossRowsTrimsTrailingBlanks(t *testing.T) {
	cli, _, _ := newSelectClient(t)
	d := placementFor(cli, 1).Dst

	got := drag(cli, image.Pt(d.Min.X, d.Min.Y), image.Pt(d.Min.X+2, d.Min.Y+2))
	want := "HELLO WORLD\nSECOND LINE\nTHI"
	if got != want {
		t.Errorf("copied %q, want %q", got, want)
	}
}

func TestReleaseWithoutDragCopiesNothing(t *testing.T) {
	cli, _, _ := newSelectClient(t)
	d := placementFor(cli, 1).Dst

	if got := drag(cli, image.Pt(d.Min.X+3, d.Min.Y), image.Pt(d.Min.X+3, d.Min.Y)); got != "" {
		t.Errorf("a click copied %q, want nothing", got)
	}
	cli.mu.Lock()
	sel := cli.sel
	cli.mu.Unlock()
	if sel != nil {
		t.Error("a click left a selection behind")
	}
}

// A drag that wanders into the next pane stays in the one it started
// in: its neighbour's text is someone else's output.
func TestDragIsClampedToStartingPane(t *testing.T) {
	cli, _, _ := newSelectClient(t)
	d1, d2 := placementFor(cli, 1).Dst, placementFor(cli, 2).Dst

	got := drag(cli, image.Pt(d1.Min.X, d1.Min.Y), image.Pt(d2.Min.X+5, d1.Min.Y+1))
	// Pane 2's second row starts "MORE"; an unclamped drag ending five
	// cells into it would copy exactly that.
	if strings.Contains(got, "MORE") {
		t.Errorf("selection crossed into pane 2: %q", got)
	}
	if !strings.HasPrefix(got, "HELLO WORLD\nSECOND LINE") {
		t.Errorf("copied %q, want pane 1's first two rows", got)
	}
}

// The highlight is what tells you what a release will copy.
func TestSelectionIsHighlighted(t *testing.T) {
	cli, _, scr := newSelectClient(t)
	d := placementFor(cli, 1).Dst
	drag(cli, image.Pt(d.Min.X+6, d.Min.Y), image.Pt(d.Min.X+10, d.Min.Y))
	cli.Draw(scr)

	reversed := func(x, y int) bool {
		c := scr.CellAt(x, y)
		return c != nil && c.Style.Attrs&uv.AttrReverse != 0
	}
	for x := d.Min.X + 6; x <= d.Min.X+10; x++ {
		if !reversed(x, d.Min.Y) {
			t.Errorf("cell %d in the selection is not highlighted", x)
		}
	}
	if reversed(d.Min.X+5, d.Min.Y) || reversed(d.Min.X+11, d.Min.Y) {
		t.Error("a cell outside the selection is highlighted")
	}
}

// A wide glyph is one piece of text across two cells: copied once,
// and highlighted without blanking it.
func TestSelectionHandlesWideGlyphs(t *testing.T) {
	cli, ch := newMouseClient(t)
	_ = ch
	cli.HandleServerMsg(paneLines(1, 40, 22, "A世\x00B"))
	scr := newFakeHostScreen(100, 24)
	cli.Draw(scr)
	d := placementFor(cli, 1).Dst

	got := drag(cli, image.Pt(d.Min.X, d.Min.Y), image.Pt(d.Min.X+3, d.Min.Y))
	if got != "A世B" {
		t.Errorf("copied %q, want %q", got, "A世B")
	}

	cli.Draw(scr)
	if c := scr.CellAt(d.Min.X+1, d.Min.Y); c == nil || c.Content != "世" {
		t.Errorf("highlighting blanked the wide glyph: cell = %+v", c)
	}
}

func TestClearSelectionOnKey(t *testing.T) {
	cli, _, _ := newSelectClient(t)
	d := placementFor(cli, 1).Dst
	drag(cli, image.Pt(d.Min.X, d.Min.Y), image.Pt(d.Min.X+4, d.Min.Y))

	cli.ClearSelection()

	cli.mu.Lock()
	defer cli.mu.Unlock()
	if cli.sel != nil {
		t.Error("ClearSelection left a selection")
	}
}

// Once the pane moves, the highlighted cells no longer hold the text
// that was selected.
func TestSelectionClearsWhenPaneMoves(t *testing.T) {
	cli, _, _ := newSelectClient(t)
	d := placementFor(cli, 1).Dst
	drag(cli, image.Pt(d.Min.X, d.Min.Y), image.Pt(d.Min.X+4, d.Min.Y))

	// Same layout again: nothing moved, selection survives.
	cli.SetLayoutMode(protocol.LayoutScroll)
	cli.HandleServerMsg(protocol.MsgLayoutSnapshot{
		Columns: []protocol.ColumnData{
			{PaneID: 1, Width: 40, Height: 22},
			{PaneID: 2, Width: 40, Height: 22},
		},
		FocusPaneID: 1,
	})
	cli.mu.Lock()
	survived := cli.sel != nil
	cli.mu.Unlock()
	if !survived {
		t.Fatal("an unchanged snapshot cleared the selection")
	}

	// Pane 1 widens: its rect changes.
	cli.SetLayoutMode(protocol.LayoutScroll)
	cli.HandleServerMsg(protocol.MsgLayoutSnapshot{
		Columns: []protocol.ColumnData{
			{PaneID: 1, Width: 60, Height: 22},
			{PaneID: 2, Width: 40, Height: 22},
		},
		FocusPaneID: 1,
	})
	cli.mu.Lock()
	defer cli.mu.Unlock()
	if cli.sel != nil {
		t.Error("selection survived its pane moving")
	}
}

// newTrackingClient is newMouseClient focused on pane 2, whose child
// has enabled mouse tracking. Pane 2 sits right of pane 1, so its
// screen and local coordinates differ.
func newTrackingClient(t *testing.T, focus int) (*Client, *transport.InProcChannel) {
	t.Helper()
	cli, ch := newMouseClient(t)
	cli.SetLayoutMode(protocol.LayoutScroll)
	cli.HandleServerMsg(protocol.MsgLayoutSnapshot{
		Columns: []protocol.ColumnData{
			{PaneID: 1, Width: 40, Height: 22},
			{PaneID: 2, Width: 40, Height: 22},
		},
		FocusPaneID: focus,
	})
	upd := paneLines(2, 40, 22, "TRACKING CHILD")
	upd.MouseTracking = true
	cli.HandleServerMsg(upd)
	cli.HandleServerMsg(paneLines(1, 40, 22, "PLAIN SHELL"))
	cli.Draw(newFakeHostScreen(100, 24))
	sent(ch)
	return cli, ch
}

func mice(msgs []transport.ClientMessage) []protocol.MsgMouse {
	var out []protocol.MsgMouse
	for _, m := range msgs {
		if mm, ok := m.(protocol.MsgMouse); ok {
			out = append(out, mm)
		}
	}
	return out
}

func TestPressInTrackingFocusedPaneIsForwarded(t *testing.T) {
	cli, ch := newTrackingClient(t, 2)
	d := placementFor(cli, 2).Dst
	if d.Min.X == 0 {
		t.Fatal("fixture wants pane 2 off the left edge so translation is visible")
	}

	cli.HandleMouse(context.Background(), press(d.Min.X+3, d.Min.Y+2))

	got := mice(sent(ch))
	want := protocol.MsgMouse{PaneID: 2, Kind: protocol.MousePress, X: 3, Y: 2, Button: int(uv.MouseLeft)}
	if len(got) != 1 || got[0] != want {
		t.Errorf("forwarded %+v, want [%+v]", got, want)
	}
}

// The child never sees half a click: the press that focuses a pane is
// wideboi's, and its release must not arrive alone.
func TestPressOnUnfocusedTrackingPaneOnlyFocuses(t *testing.T) {
	cli, ch := newTrackingClient(t, 1)
	d := placementFor(cli, 2).Dst

	cli.HandleMouse(context.Background(), press(d.Min.X+3, d.Min.Y+2))
	copied := cli.HandleMouse(context.Background(), release(d.Min.X+3, d.Min.Y+2))

	msgs := sent(ch)
	if f := focusRequests(msgs); len(f) != 1 || f[0] != 2 {
		t.Errorf("focus requests = %v, want [2]", f)
	}
	if m := mice(msgs); len(m) != 0 {
		t.Errorf("forwarded %+v to a pane that was not focused", m)
	}
	if copied != "" {
		t.Errorf("copied %q", copied)
	}
}

// Once a press reaches the child, the rest of that drag does too, even
// past the pane's edge -- clamped, so the child never sees a cell it
// does not have.
func TestForwardedDragFollowsGrabOutsidePane(t *testing.T) {
	cli, ch := newTrackingClient(t, 2)
	d := placementFor(cli, 2).Dst
	ctx := context.Background()

	cli.HandleMouse(ctx, press(d.Min.X+3, d.Min.Y+2))
	cli.HandleMouse(ctx, moveTo(0, d.Min.Y+2))
	copied := cli.HandleMouse(ctx, release(0, d.Min.Y+2))

	msgs := sent(ch)
	got := mice(msgs)
	if len(got) != 3 {
		t.Fatalf("forwarded %d events, want press, motion, release: %+v", len(got), got)
	}
	if got[1].Kind != protocol.MouseMotion || got[1].X != 0 {
		t.Errorf("motion = %+v, want MouseMotion clamped to X=0", got[1])
	}
	if got[2].Kind != protocol.MouseRelease || got[2].X != 0 {
		t.Errorf("release = %+v, want MouseRelease clamped to X=0", got[2])
	}
	if f := focusRequests(msgs); len(f) != 0 {
		t.Errorf("drag into pane 1 sent focus %v", f)
	}
	if copied != "" {
		t.Errorf("a forwarded drag copied %q; the child owns this pane's mouse", copied)
	}

	// The grab ends on release: the next motion goes nowhere.
	cli.HandleMouse(ctx, moveTo(d.Min.X+5, d.Min.Y+2))
	if m := mice(sent(ch)); len(m) != 0 {
		t.Errorf("motion after release forwarded %+v", m)
	}
}

// The wheel goes to whatever child is under it, focused or not -- the
// same rule as scrolling a shell's history.
func TestWheelOverTrackingPaneIsForwarded(t *testing.T) {
	cli, ch := newTrackingClient(t, 1)
	d := placementFor(cli, 2).Dst

	cli.HandleMouse(context.Background(), wheel(d.Min.X+1, d.Min.Y+1, uv.MouseWheelUp))

	msgs := sent(ch)
	if s := scrolls(msgs); len(s) != 0 {
		t.Errorf("wheel over a tracking child scrolled wideboi's history: %+v", s)
	}
	got := mice(msgs)
	want := protocol.MsgMouse{PaneID: 2, Kind: protocol.MouseWheel, X: 1, Y: 1, Button: int(uv.MouseWheelUp)}
	if len(got) != 1 || got[0] != want {
		t.Errorf("forwarded %+v, want [%+v]", got, want)
	}
}

// When the child turns tracking off, the pane is wideboi's again.
func TestTrackingOffRestoresSelection(t *testing.T) {
	cli, ch := newTrackingClient(t, 2)
	cli.HandleServerMsg(paneLines(2, 40, 22, "TRACKING CHILD"))
	scr := newFakeHostScreen(100, 24)
	cli.Draw(scr)
	d := placementFor(cli, 2).Dst

	got := drag(cli, image.Pt(d.Min.X, d.Min.Y), image.Pt(d.Min.X+7, d.Min.Y))
	if got != "TRACKING" {
		t.Errorf("copied %q, want %q", got, "TRACKING")
	}
	if m := mice(sent(ch)); len(m) != 0 {
		t.Errorf("forwarded %+v after tracking was turned off", m)
	}
}

// In the card fan a lower card's rect runs underneath the card above
// it. A drag starting on the lower card's visible strip must stop where
// that strip ends, or it copies the text painted over it by the card
// on top.
func TestCardSelectionStopsAtTheCardAbove(t *testing.T) {
	ch := transport.NewInProcChannel(64)
	cli := NewClient(ch, 100, 24, "C-b")
	cli.SetLayoutMode(protocol.LayoutCards)
	cli.HandleServerMsg(protocol.MsgLayoutSnapshot{
		Columns: threeColumns(), FocusPaneID: 2,
	})
	cli.HandleServerMsg(paneLines(1, 60, 10, "LOWER-CARD-TEXT-THAT-RUNS-UNDER-THE-NEXT", "LOWER-ROW-TWO"))
	cli.HandleServerMsg(paneLines(2, 60, 10, "TOP-CARD", "TOP-ROW-TWO"))
	cli.HandleServerMsg(paneLines(3, 60, 10, "RIGHT"))
	cli.Draw(newFakeHostScreen(100, 24))
	d1, d2 := placementFor(cli, 1).Dst, placementFor(cli, 2).Dst
	if !d1.Overlaps(d2) || d2.Min.X <= d1.Min.X {
		t.Fatalf("fixture wants pane 2 over pane 1's right side: p1=%v p2=%v", d1, d2)
	}

	got := drag(cli, image.Pt(d1.Min.X, d1.Min.Y), image.Pt(d2.Min.X+10, d1.Min.Y+1))
	// Not "TOP": the card above paints its left divider over its own
	// first column, so what leaks is "OP-CARD".
	if strings.Contains(got, "OP-CARD") || strings.Contains(got, "┃") {
		t.Errorf("selection on pane 1 copied pane 2's text: %q", got)
	}
	if !strings.HasPrefix(got, "LOWER") {
		t.Errorf("copied %q, want pane 1's visible text", got)
	}
}

// A forwarded drag whose release never arrived -- swallowed while the
// help overlay was up, say -- must not eat the next press.
func TestStaleGrabDoesNotSwallowNextPress(t *testing.T) {
	cli, ch := newTrackingClient(t, 2)
	d := placementFor(cli, 2).Dst
	ctx := context.Background()

	cli.HandleMouse(ctx, press(d.Min.X+3, d.Min.Y+2))
	cli.HandleMouse(ctx, press(d.Min.X+5, d.Min.Y+2))

	got := mice(sent(ch))
	if len(got) != 2 || got[1].Kind != protocol.MousePress || got[1].X != 5 {
		t.Errorf("forwarded %+v, want two presses, the second at X=5", got)
	}
}

// A status-glyph snapshot re-sends an unchanged layout every few
// seconds. A selection on an overlapped card -- whose bounds are
// narrower than its rect -- must survive it.
func TestCardSelectionSurvivesUnchangedSnapshot(t *testing.T) {
	ch := transport.NewInProcChannel(64)
	cli := NewClient(ch, 100, 24, "C-b")
	cli.SetLayoutMode(protocol.LayoutCards)
	snap := protocol.MsgLayoutSnapshot{Columns: threeColumns(), FocusPaneID: 2}
	cli.HandleServerMsg(snap)
	cli.HandleServerMsg(paneLines(1, 60, 10, "LOWER"))
	cli.Draw(newFakeHostScreen(100, 24))
	d1 := placementFor(cli, 1).Dst
	drag(cli, image.Pt(d1.Min.X, d1.Min.Y), image.Pt(d1.Min.X+4, d1.Min.Y))

	snap.PaneStatuses = map[int]string{3: "»"}
	cli.HandleServerMsg(snap)

	cli.mu.Lock()
	defer cli.mu.Unlock()
	if cli.sel == nil {
		t.Error("an unchanged layout cleared a card selection")
	}
}

// Dragging to copy from a background pane must not bring it forward:
// focusing on press would re-deal the layout mid-drag, moving the text
// out from under the pointer and throwing the selection away. Content
// clicks therefore focus on release, and only if the pointer did not
// move.
func TestDragOnUnfocusedPaneSelectsWithoutFocusing(t *testing.T) {
	cli, ch, _ := newSelectClient(t)
	d := placementFor(cli, 2).Dst

	got := drag(cli, image.Pt(d.Min.X, d.Min.Y), image.Pt(d.Min.X+8, d.Min.Y))

	if got != "NEIGHBOUR" {
		t.Errorf("copied %q, want %q", got, "NEIGHBOUR")
	}
	if f := focusRequests(sent(ch)); len(f) != 0 {
		t.Errorf("a drag on an unfocused pane sent focus %v", f)
	}
}

// The header is not content and cannot start a selection, so there is
// no reason to wait for the release there.
func TestHeaderPressFocusesImmediately(t *testing.T) {
	cli, ch := newMouseClient(t)
	p2 := placementFor(cli, 2)

	cli.HandleMouse(context.Background(), press(p2.Dst.Min.X+2, 0))

	if got := focusRequests(sent(ch)); len(got) != 1 || got[0] != 2 {
		t.Errorf("focus requests after header press = %v, want [2]", got)
	}
}
