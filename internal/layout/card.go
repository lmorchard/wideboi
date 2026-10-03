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
// divides the remainder among the others. Pinned columns are anchored
// to the left edge of the viewport.
func (cs CardStrategy) ComputePlacements(s *Strip, viewportWidth, viewportHeight int) []Placement {
	if len(s.columns) == 0 || viewportWidth <= 0 || viewportHeight <= 0 {
		return nil
	}

	availHeight := AvailHeight(viewportHeight)

	// Partition pinned and unpinned columns.
	var pinnedCols []Column
	var unpinnedCols []Column
	for _, c := range s.columns {
		if c.Pinned {
			pinnedCols = append(pinnedCols, c)
		} else {
			unpinnedCols = append(unpinnedCols, c)
		}
	}

	placements := make([]Placement, 0, len(s.columns))
	focusedID := s.FocusedPaneID()

	// 1. Place pinned columns statically on the left.
	pinnedWidth := 0
	for _, c := range pinnedCols {
		dstX := pinnedWidth
		dstW := min(c.Width, max(0, viewportWidth-dstX))
		pinnedWidth += c.Width + 1
		if dstW <= 0 {
			continue
		}
		maxSrcY := max(0, c.Height-availHeight)
		z := 0
		if c.PaneID == focusedID {
			z = 1
		}
		placements = append(placements, Placement{
			PaneID: c.PaneID,
			Src:    image.Rect(0, maxSrcY, dstW, maxSrcY+availHeight),
			Dst:    image.Rect(dstX, 1, dstX+dstW, 1+availHeight),
			Frame:  image.Rect(dstX, 1, dstX+dstW, 1+availHeight),
			Z:      z,
			Kind:   protocol.PlacementFull,
		})
	}

	unpinnedStartX := pinnedWidth
	remainingViewport := max(0, viewportWidth-unpinnedStartX)
	if len(unpinnedCols) == 0 || remainingViewport <= 0 {
		return placements
	}

	if len(unpinnedCols) == 1 {
		col := unpinnedCols[0]
		w := min(col.Width, remainingViewport)
		maxSrcY := max(0, col.Height-availHeight)
		dst := image.Rect(unpinnedStartX, 1, unpinnedStartX+w, 1+availHeight)
		placements = append(placements, Placement{
			PaneID: col.PaneID,
			Src:    image.Rect(0, maxSrcY, w, maxSrcY+availHeight),
			Dst:    dst,
			Frame:  dst,
			Z:      1,
			Kind:   protocol.PlacementFull,
		})
		return placements
	}

	// 2. Lay out unpinned columns using the card fan within remainingViewport.
	focusedInUnpinned := false
	unpinnedFocusIdx := 0
	for i, c := range unpinnedCols {
		if c.PaneID == focusedID {
			unpinnedFocusIdx = i
			focusedInUnpinned = true
			break
		}
	}

	if !focusedInUnpinned {
		// A pinned pane is focused. Prefer keeping the active unpinned card
		// based on lastFocusPaneID or the existing cardFirst window.
		if s.lastFocusPaneID != 0 {
			for i, c := range unpinnedCols {
				if c.PaneID == s.lastFocusPaneID {
					unpinnedFocusIdx = i
					break
				}
			}
		}
		if s.lastFocusPaneID == 0 || unpinnedFocusIdx == 0 {
			if s.cardFirst >= 0 && s.cardFirst < len(unpinnedCols) {
				unpinnedFocusIdx = s.cardFirst
			}
		}
	}

	focusedW := min(unpinnedCols[unpinnedFocusIdx].Width, remainingViewport)
	remaining := max(remainingViewport-focusedW, 0)

	showLeft, showRight := cs.visibleSidesForCols(unpinnedCols, unpinnedFocusIdx, s, remaining, focusedInUnpinned)
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

	x := unpinnedStartX
	sliverIdx := 0

	// ownBorder is whether the card's border takes the first cell of
	// its own slot. The focused card's lives in the slot before it.
	place := func(col Column, w int, z int, ownBorder bool) {
		if w <= 0 || x >= viewportWidth {
			return
		}
		left := x
		if ownBorder && left > unpinnedStartX {
			left++ // past the border
		}
		right := min(left+col.Width, viewportWidth)
		dst := image.Rect(left, 1, right, 1+availHeight)
		if dst.Empty() {
			return
		}

		frame := dst
		if left > unpinnedStartX {
			frame = image.Rect(left-1, 1, right, 1+availHeight)
		}

		// With overlapping cards, every pane is rendered as full content.
		// It's the z-order and clipping that handles the "sliver" effect.
		kind := protocol.PlacementFull

		maxSrcY := max(0, col.Height-availHeight)
		placements = append(placements, Placement{
			PaneID: col.PaneID,
			Src:    image.Rect(0, maxSrcY, dst.Dx(), maxSrcY+availHeight),
			Dst:    dst,
			Frame:  frame,
			Z:      z,
			Kind:   kind,
		})
		x += w
	}

	for i := unpinnedFocusIdx - showLeft; i < unpinnedFocusIdx; i++ {
		col := unpinnedCols[i]
		place(col, widthOf(sliverIdx, col), 0, true)
		sliverIdx++
	}

	place(unpinnedCols[unpinnedFocusIdx], focusedW, 1, false)

	for i := unpinnedFocusIdx + 1; i <= unpinnedFocusIdx+showRight; i++ {
		col := unpinnedCols[i]
		place(col, widthOf(sliverIdx, col), 0, true)
		sliverIdx++
	}

	return placements
}

func (cs CardStrategy) visibleSides(s *Strip, remaining int) (left, right int) {
	return cs.visibleSidesForCols(s.columns, s.focusIndex, s, remaining, true)
}

func (cs CardStrategy) visibleSidesForCols(cols []Column, focusedIdx int, s *Strip, remaining int, updateWindow bool) (left, right int) {
	numCols := len(cols)
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
	if updateWindow {
		if lo < first {
			first = lo
		}
		if hi > first+size-1 {
			first = hi - size + 1
		}
		first = max(min(first, numCols-size), 0)
		s.cardFirst = first
	} else {
		first = max(min(first, numCols-size), 0)
	}

	if focusedIdx < first {
		focusedIdx = first
	}
	if focusedIdx > first+size-1 {
		focusedIdx = first + size - 1
	}

	return focusedIdx - first, first + size - 1 - focusedIdx
}
