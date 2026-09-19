// Package layout calculates scrolling column geometries and pane placements.
package layout

import (
	"image"

	"github.com/lmorchard/wideboi/internal/protocol"
)

// Placement describes a pane's crop rectangle (Src) and screen destination (Dst).
type Placement struct {
	PaneID int
	Src    image.Rectangle
	Dst    image.Rectangle
	Z      int
}

// Column represents one vertical column in the strip.
type Column struct {
	PaneID int
	Width  int // logical column width in cells
	Height int // logical pane height in cells
}

// Strip manages a horizontal sequence of columns and viewport scrolling.
type Strip struct {
	columns    []Column
	focusIndex int
	scrollX    int
}

// NewStrip creates an empty column strip.
func NewStrip() *Strip {
	return &Strip{
		columns:    make([]Column, 0),
		focusIndex: 0,
	}
}

// ColCount returns the number of columns.
func (s *Strip) ColCount() int {
	return len(s.columns)
}

// FocusedPaneID returns the ID of the focused pane.
func (s *Strip) FocusedPaneID() int {
	if len(s.columns) == 0 || s.focusIndex < 0 || s.focusIndex >= len(s.columns) {
		return 0
	}
	return s.columns[s.focusIndex].PaneID
}

// AddColumn inserts a new column after the current focus and shifts focus to it.
func (s *Strip) AddColumn(paneID int, width, height int) {
	col := Column{PaneID: paneID, Width: width, Height: height}
	if len(s.columns) == 0 {
		s.columns = append(s.columns, col)
		s.focusIndex = 0
	} else {
		idx := s.focusIndex + 1
		s.columns = append(s.columns[:idx], append([]Column{col}, s.columns[idx:]...)...)
		s.focusIndex = idx
	}
}

// FocusLeft moves focus one column to the left.
func (s *Strip) FocusLeft() {
	if s.focusIndex > 0 {
		s.focusIndex--
	}
}

// FocusRight moves focus one column to the right.
func (s *Strip) FocusRight() {
	if s.focusIndex < len(s.columns)-1 {
		s.focusIndex++
	}
}

// CycleWidth cycles the focused column's width preset.
func (s *Strip) CycleWidth() {
	if len(s.columns) == 0 || s.focusIndex >= len(s.columns) {
		return
	}
	cur := s.columns[s.focusIndex].Width
	switch {
	case cur <= 40:
		s.columns[s.focusIndex].Width = 60
	case cur <= 60:
		s.columns[s.focusIndex].Width = 80
	default:
		s.columns[s.focusIndex].Width = 40
	}
}

// KillPane removes the specified pane from the strip and adjusts focus.
func (s *Strip) KillPane(paneID int) {
	for i, c := range s.columns {
		if c.PaneID == paneID {
			s.columns = append(s.columns[:i], s.columns[i+1:]...)
			if s.focusIndex >= len(s.columns) {
				s.focusIndex = max(len(s.columns)-1, 0)
			}
			return
		}
	}
}

// FocusPaneID sets focus to the column containing paneID if it exists.
func (s *Strip) FocusPaneID(paneID int) {
	for i, c := range s.columns {
		if c.PaneID == paneID {
			s.focusIndex = i
			return
		}
	}
}

// ComputePlacements calculates screen destination and source crop rectangles.
func (s *Strip) ComputePlacements(viewportWidth, viewportHeight int) []Placement {
	if len(s.columns) == 0 || viewportWidth <= 0 || viewportHeight <= 0 {
		return nil
	}

	availHeight := max(viewportHeight-1, 1)

	colX := make([]int, len(s.columns))
	currX := 0
	for i, c := range s.columns {
		colX[i] = currX
		currX += c.Width + 1 // 1 cell divider between columns
	}

	focusColX := colX[s.focusIndex]
	focusColW := s.columns[s.focusIndex].Width

	if focusColX < s.scrollX {
		s.scrollX = focusColX
	} else if focusColX+focusColW > s.scrollX+viewportWidth {
		s.scrollX = focusColX + focusColW - viewportWidth
	}

	placements := make([]Placement, 0, len(s.columns))
	viewportRect := image.Rect(0, 0, viewportWidth, availHeight)

	for i, c := range s.columns {
		screenX := colX[i] - s.scrollX
		screenRect := image.Rect(screenX, 0, screenX+c.Width, availHeight)

		dst := screenRect.Intersect(viewportRect)
		if dst.Empty() {
			continue
		}

		srcX := dst.Min.X - screenX
		srcY := dst.Min.Y
		src := image.Rect(srcX, srcY, srcX+dst.Dx(), srcY+dst.Dy())

		placements = append(placements, Placement{
			PaneID: c.PaneID,
			Src:    src,
			Dst:    dst,
			Z:      0,
		})
	}

	return placements
}

// ToProtocol converts Placements to protocol.PlacementData for wire transport.
func ToProtocol(placements []Placement) []protocol.PlacementData {
	out := make([]protocol.PlacementData, len(placements))
	for i, p := range placements {
		out[i] = protocol.PlacementData{
			PaneID: p.PaneID,
			Src:    p.Src,
			Dst:    p.Dst,
			Z:      p.Z,
		}
	}
	return out
}
