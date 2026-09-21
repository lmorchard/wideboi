package layout

import (
	"image"

	"github.com/lmorchard/wideboi/internal/protocol"
)

// DefaultSliverWidth is how wide an occluded card is.
//
// BEYOND-V1 section 2 budgeted 4 cells, but that was on the
// assumption that a sliver shows a peek at pane content. It shows
// chrome instead -- status glyph, title, activity spine -- which makes
// the width a free parameter, and 10 cells is what fits a readable
// horizontal title. The cost is fan size: roughly 10-12 cards at 200
// columns rather than 25, which is why cards that do not fit get a
// marker rather than vanishing.
const DefaultSliverWidth = 10

// CardStrategy arranges off-screen/peripheral columns as overlapping card slivers.
type CardStrategy struct {
	SliverWidth int
}

// ComputePlacements calculates card placements with z-ordering.
func (cs CardStrategy) ComputePlacements(s *Strip, viewportWidth, viewportHeight int) []Placement {
	if len(s.columns) == 0 || viewportWidth <= 0 || viewportHeight <= 0 {
		return nil
	}

	sliverWidth := cs.SliverWidth
	if sliverWidth <= 0 {
		sliverWidth = DefaultSliverWidth
	}

	availHeight := AvailHeight(viewportHeight)
	numCols := len(s.columns)

	if numCols == 1 {
		col := s.columns[0]
		w := min(col.Width, viewportWidth)
		return []Placement{
			{
				PaneID: col.PaneID,
				Src:    image.Rect(0, 0, w, availHeight),
				Dst:    image.Rect(0, 1, w, 1+availHeight),
				Z:      1,
				// A lone column is occluded by nothing.
				Kind: protocol.PlacementFull,
			},
		}
	}

	focusedIdx := s.focusIndex
	focusedCol := s.columns[focusedIdx]
	focusedW := min(focusedCol.Width, viewportWidth)

	leftSlivers := focusedIdx
	rightSlivers := numCols - 1 - focusedIdx

	leftTotal := leftSlivers * sliverWidth
	rightTotal := rightSlivers * sliverWidth

	var focusedX int
	if leftTotal+focusedW+rightTotal <= viewportWidth {
		focusedX = leftTotal
	} else {
		focusedX = leftTotal
		if focusedX+focusedW+rightTotal > viewportWidth {
			focusedX = max(leftTotal, viewportWidth-focusedW-rightTotal)
		}
	}
	if focusedX+focusedW > viewportWidth {
		focusedX = max(0, viewportWidth-focusedW)
	}
	if focusedX < 0 {
		focusedX = 0
	}

	placements := make([]Placement, 0, numCols)

	// Left cards (Z = 0)
	for i := 0; i < focusedIdx; i++ {
		c := s.columns[i]
		dstX := i * sliverWidth
		if dstX+sliverWidth > viewportWidth {
			continue
		}
		w := min(sliverWidth, c.Width)
		dst := image.Rect(dstX, 1, min(dstX+w, viewportWidth), 1+availHeight)
		if dst.Empty() {
			continue
		}
		placements = append(placements, Placement{
			PaneID: c.PaneID,
			Src:    image.Rect(0, 0, dst.Dx(), availHeight),
			Dst:    dst,
			Z:      0,
			Kind:   protocol.PlacementSliver,
		})
	}

	// Focused card (Z = 1)
	focusedDst := image.Rect(focusedX, 1, min(focusedX+focusedW, viewportWidth), 1+availHeight)
	if !focusedDst.Empty() {
		placements = append(placements, Placement{
			PaneID: focusedCol.PaneID,
			Src:    image.Rect(0, 0, focusedDst.Dx(), availHeight),
			Dst:    focusedDst,
			Z:      1,
			// The focused card is the one showing real content.
			Kind: protocol.PlacementFull,
		})
	}

	// Right cards (Z = 0)
	rightStartX := focusedDst.Max.X
	for i := focusedIdx + 1; i < numCols; i++ {
		c := s.columns[i]
		idxOffset := i - (focusedIdx + 1)
		dstX := rightStartX + idxOffset*sliverWidth
		if dstX >= viewportWidth {
			continue
		}
		w := min(sliverWidth, c.Width)
		dst := image.Rect(dstX, 1, min(dstX+w, viewportWidth), 1+availHeight)
		if dst.Empty() {
			continue
		}
		placements = append(placements, Placement{
			PaneID: c.PaneID,
			Src:    image.Rect(0, 0, dst.Dx(), availHeight),
			Dst:    dst,
			Z:      0,
			Kind:   protocol.PlacementSliver,
		})
	}

	return placements
}
