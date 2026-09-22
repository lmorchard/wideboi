package client

import (
	"context"
	"image"
	"sort"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

// wheelStep is rows per wheel notch, matching common terminals.
// Positive scrolls into history, the same sign as the "k" scroll-up
// binding.
const wheelStep = 3

// hitTestLocked returns the topmost placement under pt, or nil.
//
// It tests what is on screen right now -- interpolated rects mid-
// animation -- because that is what the user aimed at. The walk is
// composeFrameLocked's paint order reversed: the placement painted last
// is the one visible at a contested cell. Row 0 is the header row,
// which sits above Dst but belongs to the placement whose columns it
// spans. c.mu must be held.
func (c *Client) hitTestLocked(pt image.Point) *protocol.PlacementData {
	ps := c.currentPlacementsLocked()
	sorted := make([]protocol.PlacementData, len(ps))
	copy(sorted, ps)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Z < sorted[j].Z })
	for i := len(sorted) - 1; i >= 0; i-- {
		p := sorted[i]
		if pt.X >= p.Dst.Min.X && pt.X < p.Dst.Max.X && pt.Y >= 0 && pt.Y < p.Dst.Max.Y {
			return &p
		}
	}
	return nil
}

// visibleRectLocked is the part of p's Dst not painted over by a
// placement drawn after it, narrowed toward pt.
//
// In the card fan a lower card's rect runs underneath the card above
// it, so its visible part is a strip. Cards overlap horizontally and
// span the same rows, so trimming whole sides keeps it a rectangle;
// pt, which hit p, decides which side of an overlapping card to keep.
// c.mu must be held.
func (c *Client) visibleRectLocked(p *protocol.PlacementData, pt image.Point) image.Rectangle {
	ps := c.currentPlacementsLocked()
	sorted := make([]protocol.PlacementData, len(ps))
	copy(sorted, ps)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Z < sorted[j].Z })
	r := p.Dst
	above := false
	for _, q := range sorted {
		if q.PaneID == p.PaneID {
			above = true
			continue
		}
		if !above || !q.Dst.Overlaps(r) {
			continue
		}
		if pt.X < q.Dst.Min.X {
			r.Max.X = min(r.Max.X, q.Dst.Min.X)
		} else {
			r.Min.X = max(r.Min.X, q.Dst.Max.X)
		}
	}
	return r
}

// contentHitLocked is hitTestLocked restricted to a pane's own content:
// nil over a header, a sliver, or empty space. c.mu must be held.
func (c *Client) contentHitLocked(pt image.Point) *protocol.PlacementData {
	p := c.hitTestLocked(pt)
	if p == nil || p.Kind != protocol.PlacementFull || !pt.In(p.Dst) {
		return nil
	}
	return p
}

// HandleMouse routes one host mouse event, and returns text to place on
// the host clipboard, or "" when there is none.
//
// Control mode does not affect the mouse and the mouse does not affect
// control mode: the prefix is a keyboard concept. The help overlay is a
// modal, so it swallows everything.
func (c *Client) HandleMouse(ctx context.Context, ev uv.MouseEvent) string {
	var out []transport.ClientMessage
	var copyText string

	c.mu.Lock()
	if c.helpVisible {
		c.mu.Unlock()
		return ""
	}
	m := ev.Mouse()
	pt := image.Pt(m.X, m.Y)

	// A drag that began in a child's pane belongs to the child until the
	// button comes up, wherever the pointer wanders.
	//
	// A press while a grab is held means its release was lost -- it
	// arrived while the help overlay swallowed it, say. Drop the grab
	// and treat the press as new rather than eat it.
	if _, ok := ev.(uv.MouseClickEvent); ok {
		c.grab = nil
	}
	if g := c.grab; g != nil {
		switch ev.(type) {
		case uv.MouseMotionEvent:
			out = append(out, protocol.EncodeMouse(g.paneID, ev, g.local(pt)))
		case uv.MouseReleaseEvent:
			out = append(out, protocol.EncodeMouse(g.paneID, ev, g.local(pt)))
			c.grab = nil
		}
		c.mu.Unlock()
		for _, msg := range out {
			c.transport.SendClient(ctx, msg)
		}
		return ""
	}

	switch ev.(type) {
	case uv.MouseClickEvent:
		// Any press ends the previous selection, including one that
		// lands on nothing: clicking away is how you dismiss it.
		c.sel = nil
		p := c.hitTestLocked(pt)
		if p == nil {
			break
		}
		cp := c.contentHitLocked(pt)
		tracking := cp != nil && c.mouseTracking[cp.PaneID]

		// A press on the focused child's content is the child's, any
		// button. A press that changes focus is never forwarded: the
		// child would see a release with no press, or a click it was
		// not the target of when the user aimed.
		if tracking && p.PaneID == c.focusPaneID {
			g := &mouseGrab{paneID: cp.PaneID, dst: cp.Dst, src: cp.Src.Min}
			out = append(out, protocol.EncodeMouse(g.paneID, ev, g.local(pt)))
			c.grab = g
			break
		}
		if m.Button != uv.MouseLeft {
			break
		}
		if p.PaneID != c.focusPaneID {
			out = append(out, protocol.MsgFocusPane{PaneID: p.PaneID})
		}
		// No wideboi selection over a child that wants the mouse; the
		// terminal's own bypass modifier still selects natively there.
		if cp != nil && !tracking {
			c.sel = &selection{paneID: cp.PaneID, dst: cp.Dst, bounds: c.visibleRectLocked(cp, pt), anchor: pt, cursor: pt, dragging: true}
		}

	case uv.MouseMotionEvent:
		if c.sel != nil && c.sel.dragging {
			c.sel.cursor = clampPt(pt, c.sel.bounds)
		}

	case uv.MouseReleaseEvent:
		if c.sel == nil || !c.sel.dragging {
			break
		}
		c.sel.cursor = clampPt(pt, c.sel.bounds)
		c.sel.dragging = false
		switch {
		case c.sel.anchor == c.sel.cursor:
			// A click, not a drag. Copying one character on every
			// click-to-focus would clobber the clipboard constantly.
			c.sel = nil
		case c.lastRenderedScreen != nil:
			copyText = c.sel.text(c.lastRenderedScreen)
		}

	case uv.MouseWheelEvent:
		// The pane under the pointer, not the focused one, and focus
		// stays put: reading one pane's history while typing into
		// another is the point.
		p := c.contentHitLocked(pt)
		switch {
		case p == nil:
		case c.mouseTracking[p.PaneID]:
			g := mouseGrab{paneID: p.PaneID, dst: p.Dst, src: p.Src.Min}
			out = append(out, protocol.EncodeMouse(p.PaneID, ev, g.local(pt)))
		default:
			switch m.Button {
			case uv.MouseWheelUp:
				out = append(out, protocol.MsgScroll{PaneID: p.PaneID, Delta: wheelStep})
			case uv.MouseWheelDown:
				out = append(out, protocol.MsgScroll{PaneID: p.PaneID, Delta: -wheelStep})
			}
		}
	}
	c.mu.Unlock()

	for _, msg := range out {
		c.transport.SendClient(ctx, msg)
	}
	return copyText
}

// ClearSelection drops any highlighted selection. main calls it on
// every key press: typing means the user has moved on.
func (c *Client) ClearSelection() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sel = nil
}

// selection is a drag in progress, or a finished one still highlighted.
//
// Coordinates are screen cells, clamped to bounds -- the pane's content
// rect when the drag began -- so a selection never reaches into a
// neighbour. What gets copied is read from the composed screen: what
// you see is what you copy, in-process and attached alike.
type selection struct {
	paneID int
	// dst is the pane's Dst when the drag began; bounds is the part of
	// it that was visible, which is narrower for an overlapped card.
	dst, bounds    image.Rectangle
	anchor, cursor image.Point
	dragging       bool
}

func clampPt(pt image.Point, r image.Rectangle) image.Point {
	return image.Pt(
		min(max(pt.X, r.Min.X), r.Max.X-1),
		min(max(pt.Y, r.Min.Y), r.Max.Y-1),
	)
}

// ordered returns the two ends in reading order, so a drag up and to
// the left selects the same cells as the drag down and to the right.
func (s *selection) ordered() (start, end image.Point) {
	a, b := s.anchor, s.cursor
	if b.Y < a.Y || (b.Y == a.Y && b.X < a.X) {
		a, b = b, a
	}
	return a, b
}

// rowSpan is the inclusive range of cells selected on row y, or
// ok=false outside the selection. Stream selection, like a terminal's:
// the first row from the start onward, the last up to the end, and
// every row between them across the pane's full width.
func (s *selection) rowSpan(y int) (x0, x1 int, ok bool) {
	start, end := s.ordered()
	if y < start.Y || y > end.Y {
		return 0, 0, false
	}
	x0, x1 = s.bounds.Min.X, s.bounds.Max.X-1
	if y == start.Y {
		x0 = start.X
	}
	if y == end.Y {
		x1 = end.X
	}
	return x0, x1, true
}

// text reads the selected cells from scr: one line per row, trailing
// blanks trimmed, joined with "\n".
//
// Not compose.Text, which emits one rune per cell. This advances by
// each cell's Width, so a wide glyph contributes its content once and
// its placeholder cell nothing.
func (s *selection) text(scr uv.Screen) string {
	start, end := s.ordered()
	lines := make([]string, 0, end.Y-start.Y+1)
	for y := start.Y; y <= end.Y; y++ {
		x0, x1, _ := s.rowSpan(y)
		var b strings.Builder
		for x := x0; x <= x1; {
			c := scr.CellAt(x, y)
			if c == nil || c.Content == "" {
				b.WriteByte(' ')
				x++
				continue
			}
			b.WriteString(c.Content)
			x += max(c.Width, 1)
		}
		lines = append(lines, strings.TrimRight(b.String(), " "))
	}
	return strings.Join(lines, "\n")
}

// drawSelectionLocked inverts the selected cells over a composed frame.
//
// It writes a copy of each cell, never the pointer CellAt hands back --
// that points into the live buffer (docs/LESSONS.md). And it steps by
// each cell's Width: writing a wide glyph's placeholder cell trips
// uv.Line.Set's partial-overwrite protection and blanks the glyph.
// c.mu must be held.
func (c *Client) drawSelectionLocked(scr uv.Screen) {
	if c.sel == nil {
		return
	}
	start, end := c.sel.ordered()
	for y := start.Y; y <= end.Y; y++ {
		x0, x1, _ := c.sel.rowSpan(y)
		for x := x0; x <= x1; {
			cp := scr.CellAt(x, y)
			if cp == nil || cp.Width == 0 {
				x++
				continue
			}
			cc := *cp
			cc.Style.Attrs ^= uv.AttrReverse
			scr.SetCell(x, y, &cc)
			x += cc.Width
		}
	}
}

// selectionStillPlacedLocked reports whether the selected pane still
// sits exactly where it did when the selection was made. Once it has
// moved, the highlighted cells no longer hold the selected text.
// c.mu must be held.
func (c *Client) selectionStillPlacedLocked() bool {
	for _, p := range c.placements {
		if p.PaneID == c.sel.paneID && p.Dst == c.sel.dst {
			return true
		}
	}
	return false
}

// mouseGrab is a forwarded drag: once a press reaches a child, its
// motion and release follow it there even if the pointer leaves the
// pane.
type mouseGrab struct {
	paneID int
	dst    image.Rectangle
	src    image.Point // the placement's Src.Min
}

// local translates a screen cell into the pane's own coordinates,
// clamped to the pane so the child never hears about a cell it does
// not have.
func (g *mouseGrab) local(pt image.Point) image.Point {
	pt = clampPt(pt, g.dst)
	return image.Pt(pt.X-g.dst.Min.X+g.src.X, pt.Y-g.dst.Min.Y+g.src.Y)
}
