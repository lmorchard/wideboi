package layout

import (
	"image"

	"github.com/lmorchard/wideboi/internal/protocol"
)

// MinSliverWidth is the narrowest a card can be and still say
// anything: a column of spine, a status glyph, and a space.
//
// This replaces DefaultSliverWidth, which was the layout's governing
// number and knew nothing about the viewport -- a 120-column window
// with three 30-wide panes used 50 columns and left 70 dead. A floor
// is a different kind of number: it decides how many cards fit, not
// how wide they are.
const MinSliverWidth = 4

// CardStrategy fans the unfocused columns into chrome slivers beside
// the focused pane.
//
// The focused pane keeps its own width and the rest divide whatever
// is left, so the fan always spans the viewport.
//
// Every card but the leftmost has a border in the cell just before its
// Dst, and the client draws it at Dst.Min.X-1. It used to be painted
// over the card's own first column, which hid column 0 of every pane
// in the fan and put the border glyph into anything copied from it.
//
// A sliver's border is the first cell of its own slot. The focused
// card's is the last cell of its left neighbour's, so the focused card
// keeps its whole width without costing the slivers any budget: charged
// there, it could decide that no sliver fits at all.
type CardStrategy struct {
	// SliverWidth, when positive, forces every sliver to this width
	// instead of an even share. Tests use it to pin geometry; nothing
	// in production sets it.
	SliverWidth int
}

// shareAt returns the width of the i-th of n cards dividing total
// cells between them.
//
// The first (total % n) cards get one extra cell, so the widths sum
// to exactly total and the fan's right edge lands on the viewport
// edge rather than one or two columns short of it.
func shareAt(total, n, i int) int {
	if n <= 0 || total <= 0 {
		return 0
	}
	w := total / n
	if i < total%n {
		w++
	}
	return w
}

// ComputePlacements lays the focused pane out at its own width and
// divides the remainder among the others.
func (cs CardStrategy) ComputePlacements(s *Strip, viewportWidth, viewportHeight int) []Placement {
	if len(s.columns) == 0 || viewportWidth <= 0 || viewportHeight <= 0 {
		return nil
	}

	availHeight := AvailHeight(viewportHeight)
	numCols := len(s.columns)

	if numCols == 1 {
		col := s.columns[0]
		w := min(col.Width, viewportWidth)
		return []Placement{{
			PaneID: col.PaneID,
			Src:    image.Rect(0, 0, w, availHeight),
			Dst:    image.Rect(0, 1, w, 1+availHeight),
			Z:      1,
			// A lone column is occluded by nothing.
			Kind: protocol.PlacementFull,
		}}
	}

	focusedIdx := s.focusIndex
	focusedW := min(s.columns[focusedIdx].Width, viewportWidth)
	remaining := max(viewportWidth-focusedW, 0)

	showLeft, showRight := cs.visibleSides(s, remaining)
	sliverCount := showLeft + showRight

	// Sliver widths, indexed left to right across the whole fan so the
	// remainder is spread evenly rather than piling onto one side.
	widthOf := func(sliverIdx int, col Column) int {
		share := cs.SliverWidth
		if share <= 0 {
			share = shareAt(remaining, sliverCount, sliverIdx)
		}
		// Never wider than the pane itself: a card given more room
		// than it needs should show content, not a spine.
		return min(share, col.Width)
	}

	placements := make([]Placement, 0, sliverCount+1)
	x := 0
	sliverIdx := 0

	// ownBorder is whether the card's border takes the first cell of
	// its own slot. The focused card's lives in the slot before it.
	place := func(col Column, w int, z int, ownBorder bool) {
		if w <= 0 || x >= viewportWidth {
			return
		}
		left := x
		if ownBorder && left > 0 {
			left++ // past the border
		}
		right := min(left+col.Width, viewportWidth)
		dst := image.Rect(left, 1, right, 1+availHeight)
		if dst.Empty() {
			return
		}

		// With overlapping cards, every pane is rendered as full content.
		// It's the z-order and clipping that handles the "sliver" effect.
		kind := protocol.PlacementFull

		placements = append(placements, Placement{
			PaneID: col.PaneID,
			Src:    image.Rect(0, 0, dst.Dx(), availHeight),
			Dst:    dst,
			Z:      z,
			Kind:   kind,
		})
		x += w
	}

	for i := focusedIdx - showLeft; i < focusedIdx; i++ {
		col := s.columns[i]
		place(col, widthOf(sliverIdx, col), 0, true)
		sliverIdx++
	}

	place(s.columns[focusedIdx], focusedW, 1, false)

	for i := focusedIdx + 1; i <= focusedIdx+showRight; i++ {
		col := s.columns[i]
		place(col, widthOf(sliverIdx, col), 0, true)
		sliverIdx++
	}

	return placements
}

// visibleSides decides how many cards to show on each side of the
// focused pane.
//
// With enough columns the even share rounds below what chrome needs,
// so only as many as clear MinSliverWidth are shown and the rest are
// dropped, where hiddenCountsLocked finds them and the client draws a
// "+N".
//
// Which ones survive is a window over the strip that scrolls the way
// ScrollStrategy's viewport does (issue #20): it is remembered on the
// Strip and moves only when focus would otherwise get too close to an
// edge. Re-centring on every move instead meant each focus change
// reshuffled which cards were on screen, on both sides.
//
// "Too close" is a margin of one card when the window holds at least
// three, so both of the focused pane's neighbours stay visible --
// those are the ones you are most likely to want next.
func (cs CardStrategy) visibleSides(s *Strip, remaining int) (left, right int) {
	numCols := len(s.columns)
	focusedIdx := s.focusIndex
	others := numCols - 1

	budget := others
	if cs.SliverWidth > 0 {
		// Pinned width: as many as fit whole.
		budget = remaining / cs.SliverWidth
	} else if others > 0 && remaining/others < MinSliverWidth {
		budget = remaining / MinSliverWidth
	}
	size := min(budget, others) + 1 // the focused card and its slivers

	margin := 0
	if size >= 3 {
		margin = 1
	}
	lo := max(focusedIdx-margin, 0)
	hi := min(focusedIdx+margin, numCols-1)

	first := s.cardFirst
	if lo < first {
		first = lo
	}
	if hi > first+size-1 {
		first = hi - size + 1
	}
	first = max(min(first, numCols-size), 0)
	s.cardFirst = first

	return focusedIdx - first, first + size - 1 - focusedIdx
}
