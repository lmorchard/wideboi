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
// is left, so the fan always spans the viewport. No divider columns
// are reserved: every sliver draws a spine at its own left edge,
// which is the separator -- reserving one as ScrollStrategy does
// would cost a cell per card for no visible gain.
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

	showLeft, showRight := cs.visibleSides(focusedIdx, numCols, remaining)
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

	place := func(col Column, w int, z int) {
		if w <= 0 || x >= viewportWidth {
			return
		}
		right := min(x+w, viewportWidth)
		dst := image.Rect(x, 1, right, 1+availHeight)
		if dst.Empty() {
			return
		}
		kind := protocol.PlacementSliver
		if z == 1 || dst.Dx() >= col.Width {
			// Either the focused pane, or a card with room for the
			// whole thing -- nothing is occluded, so draw content.
			kind = protocol.PlacementFull
		}
		placements = append(placements, Placement{
			PaneID: col.PaneID,
			Src:    image.Rect(0, 0, dst.Dx(), availHeight),
			Dst:    dst,
			Z:      z,
			Kind:   kind,
		})
		x = dst.Max.X
	}

	for i := focusedIdx - showLeft; i < focusedIdx; i++ {
		col := s.columns[i]
		place(col, widthOf(sliverIdx, col), 0)
		sliverIdx++
	}

	place(s.columns[focusedIdx], focusedW, 1)

	for i := focusedIdx + 1; i <= focusedIdx+showRight; i++ {
		col := s.columns[i]
		place(col, widthOf(sliverIdx, col), 0)
		sliverIdx++
	}

	return placements
}

// visibleSides decides how many cards to show on each side of the
// focused pane.
//
// With enough columns the even share rounds below what chrome needs,
// so only as many as clear MinSliverWidth are shown. The survivors
// are taken from nearest the focused pane outward -- those are the
// neighbours you are most likely to want next -- and the rest are
// dropped, where hiddenCountsLocked finds them and the client draws a
// "+N".
func (cs CardStrategy) visibleSides(focusedIdx, numCols, remaining int) (left, right int) {
	availLeft := focusedIdx
	availRight := numCols - 1 - focusedIdx
	wanted := availLeft + availRight

	budget := wanted
	if cs.SliverWidth > 0 {
		// Pinned width: as many as fit whole.
		budget = min(wanted, remaining/cs.SliverWidth)
	} else if wanted > 0 && remaining/wanted < MinSliverWidth {
		budget = remaining / MinSliverWidth
	}
	if budget > wanted {
		budget = wanted
	}

	// Alternate outward from the focused pane so both neighbours
	// survive before either side's second card does.
	for left+right < budget {
		grew := false
		if right < availRight {
			right++
			grew = true
			if left+right == budget {
				break
			}
		}
		if left < availLeft {
			left++
			grew = true
		}
		if !grew {
			break
		}
	}
	return left, right
}
