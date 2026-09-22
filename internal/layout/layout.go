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
	// Kind distinguishes a pane drawing its own content from an
	// occluded card drawn as chrome. Geometry cannot: see
	// protocol.PlacementKind.
	Kind protocol.PlacementKind
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
	strategy   Strategy
}

// NewStrip creates an empty column strip.
func NewStrip() *Strip {
	return &Strip{
		columns:    make([]Column, 0),
		focusIndex: 0,
		strategy:   ScrollStrategy{},
	}
}

// ApplyMode installs the strategy for mode.
//
// Both the server's strip and each client's strip go through here, so
// the two halves cannot end up disagreeing about what a mode means.
func ApplyMode(s *Strip, mode protocol.LayoutMode) {
	switch mode {
	case protocol.LayoutCards:
		s.SetStrategy(CardStrategy{})
	default:
		s.SetStrategy(ScrollStrategy{})
	}
}

// SetStrategy updates the layout strategy used by ComputePlacements.
func (s *Strip) SetStrategy(st Strategy) {
	s.strategy = st
}

// Strategy returns the current layout strategy.
func (s *Strip) Strategy() Strategy {
	if s.strategy == nil {
		return ScrollStrategy{}
	}
	return s.strategy
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

// CycleWidth cycles the focused column's width preset (40 -> 60 -> 80 -> 40).
// Custom spawn widths transition to the next higher preset (e.g. 50 -> 80)
// or cycle back to 40 (e.g. 99 -> 40).
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

// ColumnWidth reports paneID's own column width -- its logical width, per
// invariant 4 of the layout spec ("a pane's logical width equals its column
// width, independent of what is visible"). This is NOT the same as a
// Placement's Dst width, which is the post-clip crop: a column scrolled
// partly off-screen still has its full column width here.
func (s *Strip) ColumnWidth(paneID int) (int, bool) {
	for _, c := range s.columns {
		if c.PaneID == paneID {
			return c.Width, true
		}
	}
	return 0, false
}

// PaneIDs returns every pane currently in the strip, whether or not
// ComputePlacements would give it a Placement. A column scrolled fully
// off-screen has no Placement at all (ComputePlacements drops it via
// dst.Empty()), but it still exists and still needs its logical size kept
// current -- callers that only walk Placements silently skip it.
func (s *Strip) PaneIDs() []int {
	ids := make([]int, len(s.columns))
	for i, c := range s.columns {
		ids[i] = c.PaneID
	}
	return ids
}

// AvailHeight is the row count available to every pane in a viewport of
// the given height: the viewport minus 2 rows (1 row header bar + 1 row bottom
// status bar).
func AvailHeight(viewportHeight int) int {
	return max(viewportHeight-2, 1)
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

// Strategy calculates screen destination and source crop rectangles for a strip.
type Strategy interface {
	ComputePlacements(s *Strip, viewportWidth, viewportHeight int) []Placement
}

// ScrollStrategy calculates placements for a scrolling horizontal strip of columns.
type ScrollStrategy struct{}

// ComputePlacements calculates screen destination and source crop rectangles.
func (s *Strip) ComputePlacements(viewportWidth, viewportHeight int) []Placement {
	return s.Strategy().ComputePlacements(s, viewportWidth, viewportHeight)
}

// ComputePlacements calculates screen destination and source crop rectangles.
func (ScrollStrategy) ComputePlacements(s *Strip, viewportWidth, viewportHeight int) []Placement {
	if len(s.columns) == 0 || viewportWidth <= 0 || viewportHeight <= 0 {
		return nil
	}

	availHeight := AvailHeight(viewportHeight)

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
	viewportRect := image.Rect(0, 1, viewportWidth, 1+availHeight)

	for i, c := range s.columns {
		screenX := colX[i] - s.scrollX
		screenRect := image.Rect(screenX, 1, screenX+c.Width, 1+availHeight)

		dst := screenRect.Intersect(viewportRect)
		if dst.Empty() {
			continue
		}

		srcX := dst.Min.X - screenX
		srcY := dst.Min.Y - 1
		src := image.Rect(srcX, srcY, srcX+dst.Dx(), srcY+dst.Dy())

		placements = append(placements, Placement{
			PaneID: c.PaneID,
			Src:    src,
			Dst:    dst,
			Z:      0,
			// Always Full. This strategy clips panes at the viewport
			// edge, and a clipped pane is still showing its own
			// content -- the no-shrink premise depends on it.
			Kind: protocol.PlacementFull,
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
			Kind:   p.Kind,
		}
	}
	return out
}

// ToColumnData converts columns to protocol.ColumnData for wire transport.
func ToColumnData(columns []Column) []protocol.ColumnData {
	out := make([]protocol.ColumnData, len(columns))
	for i, c := range columns {
		out[i] = protocol.ColumnData{
			PaneID: c.PaneID,
			Width:  c.Width,
			Height: c.Height,
		}
	}
	return out
}

// Columns returns a copy of the strip's columns.
func (s *Strip) Columns() []Column {
	cols := make([]Column, len(s.columns))
	copy(cols, s.columns)
	return cols
}

// SyncColumns updates the strip's columns and focused pane from protocol ColumnData.
func (s *Strip) SyncColumns(cols []protocol.ColumnData, focusPaneID int) {
	s.columns = make([]Column, len(cols))
	for i, c := range cols {
		s.columns[i] = Column{
			PaneID: c.PaneID,
			Width:  c.Width,
			Height: c.Height,
		}
	}
	s.FocusPaneID(focusPaneID)
}
