package layout

import (
	"image"
)

const DefaultSliverWidth = 4

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
				Dst:    image.Rect(0, 0, w, availHeight),
				Z:      1,
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

	placements := make([]Placement, 0, numCols)

	// Left cards (Z = 0)
	for i := 0; i < focusedIdx; i++ {
		c := s.columns[i]
		dstX := i * sliverWidth
		if dstX+sliverWidth > viewportWidth {
			continue
		}
		w := min(sliverWidth, c.Width)
		dst := image.Rect(dstX, 0, min(dstX+w, viewportWidth), availHeight)
		if dst.Empty() {
			continue
		}
		placements = append(placements, Placement{
			PaneID: c.PaneID,
			Src:    image.Rect(0, 0, dst.Dx(), availHeight),
			Dst:    dst,
			Z:      0,
		})
	}

	// Focused card (Z = 1)
	focusedDst := image.Rect(focusedX, 0, min(focusedX+focusedW, viewportWidth), availHeight)
	if !focusedDst.Empty() {
		placements = append(placements, Placement{
			PaneID: focusedCol.PaneID,
			Src:    image.Rect(0, 0, focusedDst.Dx(), availHeight),
			Dst:    focusedDst,
			Z:      1,
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
		dst := image.Rect(dstX, 0, min(dstX+w, viewportWidth), availHeight)
		if dst.Empty() {
			continue
		}
		placements = append(placements, Placement{
			PaneID: c.PaneID,
			Src:    image.Rect(0, 0, dst.Dx(), availHeight),
			Dst:    dst,
			Z:      0,
		})
	}

	return placements
}