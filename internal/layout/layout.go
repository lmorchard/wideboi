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

// DefaultWidthPresets is the default cycle sequence when none is configured.
var DefaultWidthPresets = []int{40, 60, 80}

// Column widths are bounded by the PTY protocol's accepted range.
const (
	MinColumnWidth = 20
	MaxColumnWidth = 4096
)

// Strip manages a horizontal sequence of columns and viewport scrolling.
type Strip struct {
	columns    []Column
	focusIndex int
	scrollX    int
	cardFirst  int // CardStrategy's window: index of its leftmost card
	// lastFocusPaneID is the pane focused before the current one, for
	// FocusLast. 0 means none. Recorded by noteFocusFrom.
	lastFocusPaneID int
	strategy        Strategy
	widthPresets    []int
}

// NewStrip creates an empty column strip.
func NewStrip() *Strip {
	return &Strip{
		columns:      make([]Column, 0),
		focusIndex:   0,
		strategy:     ScrollStrategy{},
		widthPresets: append([]int(nil), DefaultWidthPresets...),
	}
}

// SetWidthPresets configures the sequence of presets used by CycleWidth.
// Values outside the column width range are filtered out. If the resulting slice is empty,
// DefaultWidthPresets is used.
func (s *Strip) SetWidthPresets(presets []int) {
	valid := make([]int, 0, len(presets))
	for _, p := range presets {
		if p >= MinColumnWidth && p <= MaxColumnWidth {
			valid = append(valid, p)
		}
	}
	if len(valid) == 0 {
		s.widthPresets = append([]int(nil), DefaultWidthPresets...)
		return
	}
	s.widthPresets = valid
}

// WidthPresets returns a copy of the current width presets.
func (s *Strip) WidthPresets() []int {
	if len(s.widthPresets) == 0 {
		return append([]int(nil), DefaultWidthPresets...)
	}
	return append([]int(nil), s.widthPresets...)
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

// AddColumn inserts a new column after the specified pane and shifts focus to it.
func (s *Strip) AddColumn(paneID int, width, height int, afterPaneID int) {
	prev := s.FocusedPaneID()
	defer s.noteFocusFrom(prev)
	col := Column{PaneID: paneID, Width: width, Height: height}

	insertIdx := len(s.columns)
	for i, c := range s.columns {
		if c.PaneID == afterPaneID {
			insertIdx = i + 1
			break
		}
	}

	if len(s.columns) == 0 {
		s.columns = append(s.columns, col)
		s.focusIndex = 0
	} else {
		s.columns = append(s.columns[:insertIdx], append([]Column{col}, s.columns[insertIdx:]...)...)
		s.focusIndex = insertIdx
	}
}

// FocusLeft moves focus one column to the left.
func (s *Strip) FocusLeft() {
	prev := s.FocusedPaneID()
	defer s.noteFocusFrom(prev)
	if s.focusIndex > 0 {
		s.focusIndex--
	}
}

// FocusRight moves focus one column to the right.
func (s *Strip) FocusRight() {
	prev := s.FocusedPaneID()
	defer s.noteFocusFrom(prev)
	if s.focusIndex < len(s.columns)-1 {
		s.focusIndex++
	}
}

// CycleWidth cycles the specified pane's column's width preset.
// Custom spawn widths transition to the next higher preset
// or wrap back to the first preset when at or above the maximum preset.
func (s *Strip) CycleWidth(paneID int) {
	for i := range s.columns {
		if s.columns[i].PaneID == paneID {
			presets := s.WidthPresets()
			cur := s.columns[i].Width
			for _, p := range presets {
				if p > cur {
					s.columns[i].Width = p
					return
				}
			}
			s.columns[i].Width = presets[0]
			return
		}
	}
}

// GrowWidth increases the specified pane's column's width by delta cells.
func (s *Strip) GrowWidth(paneID int, delta int) {
	if delta <= 0 {
		return
	}
	for i := range s.columns {
		if s.columns[i].PaneID == paneID {
			s.columns[i].Width = min(s.columns[i].Width+delta, MaxColumnWidth)
			return
		}
	}
}

// ShrinkWidth decreases the specified pane's column's width by delta cells,
// bounded from below by MinColumnWidth.
func (s *Strip) ShrinkWidth(paneID int, delta int) {
	if delta <= 0 {
		return
	}
	for i := range s.columns {
		if s.columns[i].PaneID == paneID {
			cur := s.columns[i].Width
			if cur-delta < MinColumnWidth {
				s.columns[i].Width = MinColumnWidth
			} else {
				s.columns[i].Width = cur - delta
			}
			return
		}
	}
}

// MoveLeft swaps the specified pane's column with its left neighbour.
func (s *Strip) MoveLeft(paneID int) {
	for i := range s.columns {
		if s.columns[i].PaneID == paneID {
			if i > 0 {
				s.columns[i-1], s.columns[i] = s.columns[i], s.columns[i-1]
				if s.focusIndex == i {
					s.focusIndex = i - 1
				} else if s.focusIndex == i-1 {
					s.focusIndex = i
				}
			}
			return
		}
	}
}

// MoveRight is MoveLeft's mirror.
func (s *Strip) MoveRight(paneID int) {
	for i := range s.columns {
		if s.columns[i].PaneID == paneID {
			if i < len(s.columns)-1 {
				s.columns[i+1], s.columns[i] = s.columns[i], s.columns[i+1]
				if s.focusIndex == i {
					s.focusIndex = i + 1
				} else if s.focusIndex == i+1 {
					s.focusIndex = i
				}
			}
			return
		}
	}
}

// noteFocusFrom records prev as the last-focused pane if focus has
// actually moved off it. Every focus mutation calls it, so FocusLast
// covers keys, clicks, attention jumps and digit jumps alike -- all of
// them end in one of those methods.
func (s *Strip) noteFocusFrom(prev int) {
	if prev != 0 && prev != s.FocusedPaneID() {
		s.lastFocusPaneID = prev
	}
}

// FocusLast focuses the previously focused pane, which makes the pane
// being left the new previous one: doing it twice is a round trip.
func (s *Strip) FocusLast() {
	if s.lastFocusPaneID != 0 {
		s.FocusPaneID(s.lastFocusPaneID)
	}
}

// LastFocusPaneID reports the previously focused pane, or 0 for none.
func (s *Strip) LastFocusPaneID() int {
	return s.lastFocusPaneID
}

// KillPane removes the specified pane from the strip and adjusts focus.
// The focus shift a removal causes is not recorded for FocusLast, and a
// record naming the dead pane is forgotten: there is nothing to go back to.
func (s *Strip) KillPane(paneID int) {
	if s.lastFocusPaneID == paneID {
		s.lastFocusPaneID = 0
	}
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

// SetColumnWidth changes one column's logical width. Callers decide whether
// that column is a PTY size or a client-local display width.
func (s *Strip) SetColumnWidth(paneID, width int) bool {
	if width < MinColumnWidth || width > MaxColumnWidth {
		return false
	}
	for i := range s.columns {
		if s.columns[i].PaneID == paneID {
			s.columns[i].Width = width
			return true
		}
	}
	return false
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

// SetAllColumnHeights updates the logical height of every column in the strip.
func (s *Strip) SetAllColumnHeights(height int) {
	for i := range s.columns {
		s.columns[i].Height = height
	}
}

// AvailHeight is the row count available to every pane in a viewport of
// the given height: the viewport minus 2 rows (1 row header bar + 1 row bottom
// status bar).
func AvailHeight(viewportHeight int) int {
	return max(viewportHeight-2, 1)
}

// FocusPaneID sets focus to the column containing paneID if it exists.
func (s *Strip) FocusPaneID(paneID int) {
	prev := s.FocusedPaneID()
	defer s.noteFocusFrom(prev)
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
		maxSrcY := max(0, c.Height-availHeight)
		srcY := maxSrcY + (dst.Min.Y - 1)
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

// SyncColumns updates the columns while retaining the requested local focus.
// If that pane has closed, focus the preceding column (or the first one).
func (s *Strip) SyncColumns(cols []protocol.ColumnData, focusPaneID int) {
	oldIndex := s.focusIndex
	s.columns = make([]Column, len(cols))
	for i, c := range cols {
		s.columns[i] = Column{
			PaneID: c.PaneID,
			Width:  c.Width,
			Height: c.Height,
		}
	}
	if len(s.columns) == 0 {
		s.focusIndex = 0
		s.lastFocusPaneID = 0
		return
	}
	newIndex := -1
	for i, c := range s.columns {
		if c.PaneID == focusPaneID {
			newIndex = i
			break
		}
	}
	if newIndex < 0 {
		newIndex = min(max(oldIndex-1, 0), len(s.columns)-1)
	}
	s.focusIndex = newIndex
	for _, c := range s.columns {
		if c.PaneID == s.lastFocusPaneID {
			return
		}
	}
	s.lastFocusPaneID = 0
}
