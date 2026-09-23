package client

import (
	"fmt"
	"image"
	"strings"
	"testing"

	"github.com/lmorchard/wideboi/internal/client/compose"

	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

// threeColumns is a fan whose panes are wider than the share they
// will get, so card mode actually produces slivers.
//
// These were 30 cells wide when slivers were a fixed 10. Under
// proportional shares a 30-wide pane in a 100-column viewport gets 35
// and renders full, which is correct behaviour and useless as a
// sliver fixture -- hence 60.
func threeColumns() []*protocol.ColumnData {
	return []*protocol.ColumnData{
		{PaneId: 1, Width: 60, Height: 10},
		{PaneId: 2, Width: 60, Height: 10},
		{PaneId: 3, Width: 60, Height: 10},
	}
}

func sliverCount(ps []*protocol.PlacementData) int {
	n := 0
	for _, p := range ps {
		if p.Z == 1 {
			n++
		}
	}
	return n
}

// Placements are computed client-side (Plan 12)), so the client's own
// strip has to learn the mode. Carrying it on the snapshot is what
// keeps two clients of different sizes agreeing about the layout, the
// same argument that makes focus shared state.
func TestClientAppliesCardLayoutFromSnapshot(t *testing.T) {
	cli := NewClient(transport.NewInProcChannel(16), 100, 24, "C-b")

	cli.HandleServerMsg(&protocol.ServerEnvelope{Payload: &protocol.ServerEnvelope_LayoutSnapshot{LayoutSnapshot: &protocol.MsgLayoutSnapshot{
		Columns:     threeColumns(),
		FocusPaneId: 2,
		Layout:      protocol.LayoutMode_LAYOUT_CARDS,
	}}})

	cli.mu.Lock()
	got := sliverCount(cli.placements)
	total := len(cli.placements)
	cli.mu.Unlock()

	if total == 0 {
		t.Fatal("no placements computed")
	}
	if got == 0 {
		t.Errorf("card layout produced no Z=1 placement out of %d placements; "+
			"the client's strip is still on ScrollStrategy", total)
	}
}

// The toggle has to work in both directions, and going back to scroll
// must leave nothing marked as chrome.
func TestClientRevertsToScrollLayout(t *testing.T) {
	cli := NewClient(transport.NewInProcChannel(16), 100, 24, "C-b")

	cli.HandleServerMsg(&protocol.ServerEnvelope{Payload: &protocol.ServerEnvelope_LayoutSnapshot{LayoutSnapshot: &protocol.MsgLayoutSnapshot{
		Columns: threeColumns(), FocusPaneId: 2, Layout: protocol.LayoutMode_LAYOUT_CARDS,
	}}})
	cli.HandleServerMsg(&protocol.ServerEnvelope{Payload: &protocol.ServerEnvelope_LayoutSnapshot{LayoutSnapshot: &protocol.MsgLayoutSnapshot{
		Columns: threeColumns(), FocusPaneId: 2, Layout: protocol.LayoutMode_LAYOUT_SCROLL,
	}}})

	cli.mu.Lock()
	got := sliverCount(cli.placements)
	cli.mu.Unlock()

	if got != 0 {
		t.Errorf("after reverting to scroll layout, %d placements are still slivers", got)
	}
}

// The zero value is scroll, so a snapshot from a server that never sets
// the field behaves exactly as before.
func TestClientDefaultsToScrollLayout(t *testing.T) {
	cli := NewClient(transport.NewInProcChannel(16), 100, 24, "C-b")

	cli.HandleServerMsg(&protocol.ServerEnvelope{Payload: &protocol.ServerEnvelope_LayoutSnapshot{LayoutSnapshot: &protocol.MsgLayoutSnapshot{
		Columns: threeColumns(), FocusPaneId: 2,
	}}})

	cli.mu.Lock()
	got := sliverCount(cli.placements)
	cli.mu.Unlock()

	if got != 0 {
		t.Errorf("default layout produced %d slivers, want 0", got)
	}
}

// regionText reads back what was drawn inside a placement's Dst.
func regionText(scr *fakeHostScreen, r image.Rectangle) string {
	return strings.Join(compose.Text(scr, r), "\n")
}

func placementFor(cli *Client, paneID int) *protocol.PlacementData {
	cli.mu.Lock()
	defer cli.mu.Unlock()
	for _, p := range cli.placements {
		if int(p.PaneId) == paneID {
			return p
		}
	}
	return nil
}

// newCardClient returns a client in card mode with three panes, each
// carrying distinct content and a distinct title, focused on pane 2.
func newCardClient(t *testing.T, cols, rows int, titles map[int32]string) *Client {
	t.Helper()
	cli := NewClient(transport.NewInProcChannel(16), cols, rows, "C-b")
	cli.HandleServerMsg(&protocol.ServerEnvelope{Payload: &protocol.ServerEnvelope_LayoutSnapshot{LayoutSnapshot: &protocol.MsgLayoutSnapshot{
		Columns:      threeColumns(),
		FocusPaneId:  2,
		Layout:       protocol.LayoutMode_LAYOUT_CARDS,
		PaneTitles:   titles,
		PaneStatuses: map[int32]string{1: "✓", 2: " ", 3: "»"},
	}}})
	cli.HandleServerMsg(paneUpdate(1, 30, 10, "CONTENT-ONE"))
	cli.HandleServerMsg(paneUpdate(2, 30, 10, "CONTENT-TWO"))
	cli.HandleServerMsg(paneUpdate(3, 30, 10, "CONTENT-THREE"))
	return cli
}

// We now want genuinely overlapping cards that show their terminal output,
// so a partially occluded pane still shows its left edge and header.
func TestSliverRendersGlyphAndTitle(t *testing.T) {
	const cols, rows = 120, 16
	cli := newCardClient(t, cols, rows, map[int32]string{1: "deploying", 3: "compiling"})

	scr := newFakeHostScreen(cols, rows)
	cli.Draw(scr)

	p1 := placementFor(cli, 1)
	if p1.Kind != protocol.PlacementKind_PLACEMENT_FULL {
		t.Fatalf("pane 1 is %v, expected PlacementFull for overlapping cards", p1.Kind)
	}

	// Check just the first row for the header
	headerRect := image.Rect(p1.Dst.Decode().Min.X, 0, p1.Dst.Decode().Max.X, 1)
	headerText := regionText(scr, headerRect)
	if !strings.Contains(headerText, "deploy") {
		t.Errorf("card header does not show the title:\n%s", headerText)
	}
	if !strings.Contains(headerText, "✓") {
		t.Errorf("card header does not show the status glyph:\n%s", headerText)
	}

	// Check the left visible edge for content
	sliverRect := image.Rect(p1.Dst.Decode().Min.X, 1, p1.Dst.Decode().Min.X+4, p1.Dst.Decode().Max.Y)
	sliverText := regionText(scr, sliverRect)
	if !strings.Contains(sliverText, "CONT") {
		t.Errorf("card sliver does not show pane content:\n%s", sliverText)
	}
}

// A pane whose child never set a title must not render a row that
// looks like a rendering bug.
func TestSliverWithoutATitleStillRenders(t *testing.T) {
	const cols, rows = 120, 16
	cli := newCardClient(t, cols, rows, map[int32]string{})

	scr := newFakeHostScreen(cols, rows)
	cli.Draw(scr)

	headerRect := image.Rect(placementFor(cli, 1).Dst.Decode().Min.X, 0, placementFor(cli, 1).Dst.Decode().Max.X, 1)
	got := regionText(scr, headerRect)
	if strings.TrimSpace(got) == "" {
		t.Error("a titleless card rendered nothing in its header")
	}
	if !strings.Contains(got, "✓") {
		t.Errorf("a titleless card dropped its status glyph too:\n%s", got)
	}
}

// The focused card is the one you are actually looking at.
func TestFocusedCardRendersContentNotChrome(t *testing.T) {
	const cols, rows = 120, 16
	cli := newCardClient(t, cols, rows, map[int32]string{2: "focused title"})

	scr := newFakeHostScreen(cols, rows)
	cli.Draw(scr)

	got := regionText(scr, placementFor(cli, 2).Dst.Decode())
	// We expect "ONTENT-TWO" because the left border overwrites the first column ('C').
	if !strings.Contains(got, "ONTENT-TWO") {
		t.Errorf("focused card is not showing its content:\n%s", got)
	}
}

// TestHigherZSurfaceWinsAtOverlappingCells proves that when two panes occupy
// the same physical columns, the higher-Z (focused) pane paints over the lower-Z pane,
// even when the higher-Z pane appears earlier in slice order.
func TestHigherZSurfaceWinsAtOverlappingCells(t *testing.T) {
	const cols, rows = 60, 10
	cli := NewClient(transport.NewInProcChannel(16), cols, rows, "C-b")

	// Pane 1 is focused (Z=1).
	// We directly invoke composeFrameLocked with a slice where Z=1 comes FIRST
	// and Z=0 comes SECOND, both covering overlapping columns [10..30).
	pFocused := &protocol.PlacementData{
		PaneId: 1,
		Src:    protocol.EncodeRectangle(image.Rect(0, 0, 30, 10)),
		Dst:    protocol.EncodeRectangle(image.Rect(0, 1, 30, 9)),
		Z:      1,
		Kind:   protocol.PlacementKind_PLACEMENT_FULL,
	}
	pBackground := &protocol.PlacementData{
		PaneId: 2,
		Src:    protocol.EncodeRectangle(image.Rect(0, 0, 30, 10)),
		Dst:    protocol.EncodeRectangle(image.Rect(10, 1, 40, 9)),
		Z:      0,
		Kind:   protocol.PlacementKind_PLACEMENT_FULL,
	}

	st := frameState{
		placements:  []*protocol.PlacementData{pFocused, pBackground},
		focusPaneID: 1,
	}

	cli.HandleServerMsg(paneUpdate(1, 30, 10, strings.Repeat("1", 30)))
	cli.HandleServerMsg(paneUpdate(2, 30, 10, strings.Repeat("2", 30)))

	scr := newFakeHostScreen(cols, rows)
	cli.mu.Lock()
	cli.composeFrameLocked(scr, st)
	cli.mu.Unlock()

	// In the overlap region [10..30) at row 1, Pane 1 (Z=1) must win over Pane 2 (Z=0)),
	// even though Pane 2 was after Pane 1 in st.placements.
	sampleX := 20
	c := scr.CellAt(sampleX, 1)
	if c == nil || c.Content != "1" {
		got := ""
		if c != nil {
			got = c.Content
		}
		t.Fatalf("at overlap cell (%d, 1): got %q, want \"1\" (higher-Z pane must paint over lower-Z pane)",
			sampleX, got)
	}
}

// The regression guard for the entire Kind design: under the
// scrolling strip a pane clipped by the viewport edge is still showing
// its own content, and must never be painted over with chrome.
func TestClippedPaneIsNotDrawnAsChrome(t *testing.T) {
	// Narrow enough that the unfocused column is clipped.
	const cols, rows = 45, 16
	cli := NewClient(transport.NewInProcChannel(16), cols, rows, "C-b")
	cli.HandleServerMsg(&protocol.ServerEnvelope{Payload: &protocol.ServerEnvelope_LayoutSnapshot{LayoutSnapshot: &protocol.MsgLayoutSnapshot{
		Columns:     threeColumns(),
		FocusPaneId: 1,
		Layout:      protocol.LayoutMode_LAYOUT_SCROLL,
		PaneTitles:  map[int32]string{2: "should not appear"},
	}}})
	cli.HandleServerMsg(paneUpdate(1, 30, 10, "CONTENT-ONE"))
	cli.HandleServerMsg(paneUpdate(2, 30, 10, "CONTENT-TWO"))

	scr := newFakeHostScreen(cols, rows)
	cli.Draw(scr)

	whole := strings.Join(scr.text(), "\n")
	if strings.Contains(whole, "should not appear") {
		t.Errorf("a clipped pane was drawn as chrome:\n%s", whole)
	}
}

// A title the child chose can be any width, and a sliver must not
// draw outside the rect it was given.
//
// Asserted against drawSliverLocked directly rather than against the
// composed screen, because the composed screen can no longer show
// this. Cards are packed contiguously across the full viewport now,
// so a sliver's overflow either lands in the next card's region and
// is painted over when that card draws, or runs off the right edge
// and is clipped. Plan 17's version watched the composited output and
// silently stopped discriminating the moment the geometry changed --
// it passed against deliberately broken truncation.
func TestSliverTitleIsTruncatedByWidthNotRunes(t *testing.T) {
	const cols, rows = 60, 12
	cli := NewClient(transport.NewInProcChannel(16), cols, rows, "C-b")

	// A 15-cell sliver parked in the middle of a wide blank surface,
	// so anything it writes outside its bounds is visible.
	p := &protocol.PlacementData{
		PaneId: 7,
		Src:    protocol.EncodeRectangle(image.Rect(0, 0, 15, 10)),
		Dst:    protocol.EncodeRectangle(image.Rect(20, 1, 35, 11)),
		Kind:   protocol.PlacementKind_PLACEMENT_SLIVER,
	}
	st := frameState{
		placements:   []*protocol.PlacementData{p},
		focusPaneID:  1,
		paneStatuses: map[int]string{7: "»"},
		// 12 double-width runes: 12 runes but 24 cells, against 15.
		paneTitles: map[int]string{7: "日本語日本語日本語日本語"},
	}

	scr := newFakeHostScreen(cols, rows)
	cli.mu.Lock()
	cli.drawSliverLocked(scr, p, st)
	cli.mu.Unlock()

	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			inside := x >= p.Dst.Decode().Min.X && x < p.Dst.Decode().Max.X && y >= p.Dst.Decode().Min.Y && y < p.Dst.Decode().Max.Y
			if inside {
				continue
			}
			c := scr.CellAt(x, y)
			if c != nil && strings.TrimSpace(c.Content) != "" {
				t.Fatalf("sliver wrote %q at (%d,%d)), outside its rect %v",
					c.Content, x, y, p.Dst.Decode())
			}
		}
	}

	// And it must have drawn something inside.
	if !strings.ContainsAny(regionText(scr, p.Dst.Decode()), "日") {
		t.Error("the sliver drew none of the title at all")
	}
}

func TestHeaderTitleIsTruncatedByWidthNotRunes(t *testing.T) {
	const cols, rows = 60, 12
	cli := NewClient(transport.NewInProcChannel(16), cols, rows, "C-b")

	p := &protocol.PlacementData{
		PaneId: 7,
		Src:    protocol.EncodeRectangle(image.Rect(0, 0, 15, 10)),
		Dst:    protocol.EncodeRectangle(image.Rect(20, 1, 35, 11)),
		Kind:   protocol.PlacementKind_PLACEMENT_FULL,
		Z:      1,
	}
	st := frameState{
		placements:   []*protocol.PlacementData{p},
		focusPaneID:  7,
		paneStatuses: map[int]string{7: "»"},
		// 12 double-width runes: 12 runes but 24 cells, against 15.
		paneTitles: map[int]string{7: "日本語日本語日本語日本語"},
	}

	scr := newFakeHostScreen(cols, rows)
	cli.mu.Lock()
	cli.composeFrameLocked(scr, st)
	cli.mu.Unlock()

	// Check row 0 (the header row). Columns outside [20..35) must be blank.
	for x := 0; x < cols; x++ {
		inside := x >= 20 && x < 35
		if !inside {
			if c := scr.CellAt(x, 0); c != nil && c.Content != "" && c.Content != " " {
				t.Fatalf("header wrote %q at (%d,0)), outside its rect [20..35)", c.Content, x)
			}
		}
	}
}

// manyColumns builds n panes. Overflow now depends on the share
// falling below MinSliverWidth rather than on a fixed width running
// off the edge, so a fixture that overflows needs enough columns for
// the division to round under the floor -- see the callers.
func manyColumns(n int) []*protocol.ColumnData {
	out := make([]*protocol.ColumnData, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, &protocol.ColumnData{PaneId: int32(i), Width: 30, Height: 10})
	}
	return out
}

func cardClientWithColumns(t *testing.T, cols, rows, n, focus int) *Client {
	t.Helper()
	cli := NewClient(transport.NewInProcChannel(16), cols, rows, "C-b")
	cli.HandleServerMsg(&protocol.ServerEnvelope{Payload: &protocol.ServerEnvelope_LayoutSnapshot{LayoutSnapshot: &protocol.MsgLayoutSnapshot{
		Columns:     manyColumns(n),
		FocusPaneId: int32(focus),
		Layout:      protocol.LayoutMode_LAYOUT_CARDS,
	}}})
	return cli
}

// CardStrategy shows only a window of the cards when they do not all
// fit. A pane that exists and is invisible with no indication of it is the
// kind of thing that quietly erodes trust in the layout -- and with
// 10-cell slivers this is reachable, not hypothetical.
func TestHiddenCardsAreCounted(t *testing.T) {
	// 14 columns in 60 cells: 30 remain after the focused pane, so
	// 13 slivers would get 2 each -- below the 4-cell floor. Seven
	// clear it, six are dropped.
	cli := cardClientWithColumns(t, 60, 16, 14, 1)

	cli.mu.Lock()
	left, right := cli.hiddenCountsLocked(cli.frameStateLocked())
	placed := len(cli.placements)
	cli.mu.Unlock()

	if placed >= 14 {
		t.Fatalf("all %d columns were placed; this fixture is supposed to overflow", placed)
	}
	if left+right != 14-placed {
		t.Errorf("hidden counts %d+%d do not account for %d unplaced columns",
			left, right, 14-placed)
	}
	if right == 0 {
		t.Errorf("focus is on the leftmost column, so the overflow must be on the right; got left=%d right=%d", left, right)
	}
}

func TestHiddenCardMarkerIsRendered(t *testing.T) {
	const cols, rows = 60, 16
	cli := cardClientWithColumns(t, cols, rows, 14, 1)

	scr := newFakeHostScreen(cols, rows)
	cli.Draw(scr)

	header := strings.Join(compose.Text(scr, image.Rect(0, 0, cols, 1)), "")
	if !strings.Contains(header, "+") {
		t.Errorf("no overflow marker on the header row: %q", header)
	}
}

func TestNoMarkerWhenEverythingFits(t *testing.T) {
	const cols, rows = 120, 16
	cli := newCardClient(t, cols, rows, map[int32]string{})

	scr := newFakeHostScreen(cols, rows)
	cli.Draw(scr)

	header := strings.Join(compose.Text(scr, image.Rect(0, 0, cols, 1)), "")
	if strings.Contains(header, "+") {
		t.Errorf("overflow marker drawn when every card fits: %q", header)
	}
}

// Scroll mode drops columns too: ScrollStrategy skips any whose Dst
// is empty, so a pane scrolled fully out of view has no placement at
// all. Without a marker it is exactly the silent-invisible-pane problem
// card mode had -- issue #48.
func TestScrollModeMarksOffScreenPanes(t *testing.T) {
	// 14 columns of 30 cells, 31 apiece with the divider, in 60 cells:
	// focus on the first leaves about twelve fully off the right edge.
	const cols, rows = 60, 16
	cli := NewClient(transport.NewInProcChannel(16), cols, rows, "C-b")
	cli.HandleServerMsg(&protocol.ServerEnvelope{Payload: &protocol.ServerEnvelope_LayoutSnapshot{LayoutSnapshot: &protocol.MsgLayoutSnapshot{
		Columns:     manyColumns(14),
		FocusPaneId: 1,
		Layout:      protocol.LayoutMode_LAYOUT_SCROLL,
	}}})

	cli.mu.Lock()
	_, right := cli.hiddenCountsLocked(cli.frameStateLocked())
	cli.mu.Unlock()
	if right == 0 {
		t.Fatal("fixture should leave panes fully off-screen to the right")
	}

	scr := newFakeHostScreen(cols, rows)
	cli.Draw(scr)

	header := strings.Join(compose.Text(scr, image.Rect(0, 0, cols, 1)), "")
	// Ending one cell short of the edge, not at it: ultraviolet wraps a
	// write to the last column in autowrap-toggle escapes, which would
	// split the marker on the wire (docs/LESSONS.md).
	want := fmt.Sprintf("+%d", right)
	if got := header[len(header)-1-len(want) : len(header)-1]; got != want {
		t.Errorf("want %q just inside the right edge, got %q in %q", want, got, header)
	}
}

// Nothing off-screen, nothing marked: the default layout's chrome is
// unchanged for anyone whose panes fit.
func TestScrollModeNoMarkerWhenEverythingFits(t *testing.T) {
	const cols, rows = 120, 16
	cli := NewClient(transport.NewInProcChannel(16), cols, rows, "C-b")
	cli.HandleServerMsg(&protocol.ServerEnvelope{Payload: &protocol.ServerEnvelope_LayoutSnapshot{LayoutSnapshot: &protocol.MsgLayoutSnapshot{
		Columns:     manyColumns(3),
		FocusPaneId: 1,
		Layout:      protocol.LayoutMode_LAYOUT_SCROLL,
	}}})

	scr := newFakeHostScreen(cols, rows)
	cli.Draw(scr)

	header := strings.Join(compose.Text(scr, image.Rect(0, 0, cols, 1)), "")
	if strings.Contains(header, "+") {
		t.Errorf("marker drawn when every pane fits: %q", header)
	}
}

// When the last pane closes the server broadcasts a snapshot with no
// columns. The client used to skip its strip sync entirely on that
// path, leaving stale columns behind -- so hiddenCountsLocked counted
// panes that no longer exist and card mode drew a "+N" for them.
func TestEmptySnapshotClearsHiddenCardMarker(t *testing.T) {
	const cols, rows = 60, 16
	cli := cardClientWithColumns(t, cols, rows, 14, 1)

	scr := newFakeHostScreen(cols, rows)
	cli.Draw(scr)
	if !strings.Contains(strings.Join(compose.Text(scr, image.Rect(0, 0, cols, 1)), ""), "+") {
		t.Fatal("fixture should overflow and show a marker before the empty snapshot")
	}

	cli.HandleServerMsg(&protocol.ServerEnvelope{Payload: &protocol.ServerEnvelope_LayoutSnapshot{LayoutSnapshot: &protocol.MsgLayoutSnapshot{
		Columns: nil, FocusPaneId: 0, Layout: protocol.LayoutMode_LAYOUT_CARDS,
	}}})

	scr2 := newFakeHostScreen(cols, rows)
	cli.Draw(scr2)
	header := strings.Join(compose.Text(scr2, image.Rect(0, 0, cols, 1)), "")
	if strings.Contains(header, "+") {
		t.Errorf("marker survived an empty snapshot, counting panes that no longer exist: %q", header)
	}
}

// A mode change must land even while the session has no panes, or the
// next snapshot renders under the previous strategy.
func TestEmptySnapshotStillAppliesLayoutMode(t *testing.T) {
	cli := NewClient(transport.NewInProcChannel(16), 60, 16, "C-b")
	cli.HandleServerMsg(&protocol.ServerEnvelope{Payload: &protocol.ServerEnvelope_LayoutSnapshot{LayoutSnapshot: &protocol.MsgLayoutSnapshot{Columns: nil, Layout: protocol.LayoutMode_LAYOUT_CARDS}}})

	cli.mu.Lock()
	got := cli.layoutMode
	cli.mu.Unlock()
	if got != protocol.LayoutMode_LAYOUT_CARDS {
		t.Errorf("layoutMode = %v after an empty card-mode snapshot, want %v", got, protocol.LayoutMode_LAYOUT_CARDS)
	}
}

// Cards are laid out contiguously, so a divider at one card's right
// edge is the next card's first column and gets painted over the
// moment that card draws -- which is why every divider but the last
// was invisible. The sliver spine at each card's left edge already
// Cards now overlap and use borders to separate them visually.
func TestCardsDrawDividers(t *testing.T) {
	const cols, rows = 90, 16
	cli := NewClient(transport.NewInProcChannel(16), cols, rows, "C-b")
	cli.HandleServerMsg(&protocol.ServerEnvelope{Payload: &protocol.ServerEnvelope_LayoutSnapshot{LayoutSnapshot: &protocol.MsgLayoutSnapshot{
		Columns: []*protocol.ColumnData{
			{PaneId: 1, Width: 60, Height: 10},
			{PaneId: 2, Width: 60, Height: 10},
			{PaneId: 3, Width: 60, Height: 10},
		},
		FocusPaneId: 2, Layout: protocol.LayoutMode_LAYOUT_CARDS,
	}}})

	scr := newFakeHostScreen(cols, rows)
	cli.Draw(scr)

	whole := strings.Join(scr.text(), "\n")
	if !strings.Contains(whole, "│") && !strings.Contains(whole, "┃") {
		t.Errorf("card mode failed to draw a divider:\n%s", whole)
	}
}

// Scroll mode still gets them: it reserves a column for the divider,
// so there is somewhere for one to live.
func TestScrollModeStillDrawsDividers(t *testing.T) {
	const cols, rows = 90, 16
	cli := NewClient(transport.NewInProcChannel(16), cols, rows, "C-b")
	cli.HandleServerMsg(&protocol.ServerEnvelope{Payload: &protocol.ServerEnvelope_LayoutSnapshot{LayoutSnapshot: &protocol.MsgLayoutSnapshot{
		Columns: []*protocol.ColumnData{
			{PaneId: 1, Width: 30, Height: 10},
			{PaneId: 2, Width: 30, Height: 10},
		},
		FocusPaneId: 1, Layout: protocol.LayoutMode_LAYOUT_SCROLL,
	}}})

	scr := newFakeHostScreen(cols, rows)
	cli.Draw(scr)

	whole := strings.Join(scr.text(), "\n")
	if !strings.Contains(whole, "│") && !strings.Contains(whole, "┃") {
		t.Errorf("scroll mode drew no divider:\n%s", whole)
	}
}
