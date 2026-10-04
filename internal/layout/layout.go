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
	Frame  image.Rectangle
	Z      int
	// Kind distinguishes a pane drawing its own content from an
	// occluded card drawn as chrome. Geometry cannot: see
	// protocol.PlacementKind.
	Kind protocol.PlacementKind
}

// Column represents one vertical column in the strip.
type Column struct {
	PaneID    int
	Width     int  // logical column width in cells
	Height    int  // logical pane height in cells
	Pinned    bool // anchored to left edge of screen
	Collapsed bool // collapsed to narrow strip on right edge of screen
}

// DefaultWidthPresets is the default cycle sequence when none is configured.
var DefaultWidthPresets = []int{40, 60, 80}

// Column widths are bounded by the PTY protocol's accepted range.
const (
	MinColumnWidth       = 20
	MaxColumnWidth       = 4096
	CollapsedColumnWidth = 3
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
				if !s.columns[i].Pinned && s.columns[i-1].Pinned {
					return
				}
				if s.columns[i].Collapsed && !s.columns[i-1].Collapsed {
					return
				}
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
				if s.columns[i].Pinned && !s.columns[i+1].Pinned {
					return
				}
				if !s.columns[i].Collapsed && s.columns[i+1].Collapsed {
					return
				}
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

// PinColumn marks the column containing paneID as pinned to the left edge of the strip.
func (s *Strip) PinColumn(paneID int) bool {
	for i := range s.columns {
		if s.columns[i].PaneID == paneID {
			if s.columns[i].Pinned {
				return true
			}
			s.columns[i].Pinned = true
			s.columns[i].Collapsed = false
			s.reorderPinnedColumns()
			return true
		}
	}
	return false
}

// UnpinColumn unpins the column containing paneID.
func (s *Strip) UnpinColumn(paneID int) bool {
	for i := range s.columns {
		if s.columns[i].PaneID == paneID {
			if !s.columns[i].Pinned {
				return true
			}
			s.columns[i].Pinned = false
			s.reorderPinnedColumns()
			return true
		}
	}
	return false
}

// TogglePinColumn toggles the pinned status of the column containing paneID.
func (s *Strip) TogglePinColumn(paneID int) bool {
	for i := range s.columns {
		if s.columns[i].PaneID == paneID {
			s.columns[i].Pinned = !s.columns[i].Pinned
			if s.columns[i].Pinned {
				s.columns[i].Collapsed = false
			}
			s.reorderPinnedColumns()
			return true
		}
	}
	return false
}

// IsColumnPinned reports whether the column containing paneID is pinned.
func (s *Strip) IsColumnPinned(paneID int) bool {
	for _, c := range s.columns {
		if c.PaneID == paneID {
			return c.Pinned
		}
	}
	return false
}

// CollapseColumn collapses the column containing paneID to a narrow right margin sliver.
func (s *Strip) CollapseColumn(paneID int) bool {
	for i := range s.columns {
		if s.columns[i].PaneID == paneID {
			if s.columns[i].Collapsed {
				return true
			}
			s.columns[i].Collapsed = true
			s.columns[i].Pinned = false
			s.reorderPinnedColumns()
			return true
		}
	}
	return false
}

// UncollapseColumn uncollapses the column containing paneID, restoring it to active layout flow.
func (s *Strip) UncollapseColumn(paneID int) bool {
	for i := range s.columns {
		if s.columns[i].PaneID == paneID {
			if !s.columns[i].Collapsed {
				return true
			}
			s.columns[i].Collapsed = false
			s.reorderPinnedColumns()
			return true
		}
	}
	return false
}

// ToggleCollapseColumn toggles the collapsed status of the column containing paneID.
func (s *Strip) ToggleCollapseColumn(paneID int) bool {
	for i := range s.columns {
		if s.columns[i].PaneID == paneID {
			s.columns[i].Collapsed = !s.columns[i].Collapsed
			if s.columns[i].Collapsed {
				s.columns[i].Pinned = false
			}
			s.reorderPinnedColumns()
			return true
		}
	}
	return false
}

// IsColumnCollapsed reports whether the column containing paneID is collapsed.
func (s *Strip) IsColumnCollapsed(paneID int) bool {
	for _, c := range s.columns {
		if c.PaneID == paneID {
			return c.Collapsed
		}
	}
	return false
}

// reorderPinnedColumns partitions s.columns so that all pinned columns
// appear first (in their existing relative order) followed by all unpinned
// active columns, followed by all collapsed columns.
func (s *Strip) reorderPinnedColumns() {
	if len(s.columns) <= 1 {
		return
	}
	focusedID := s.FocusedPaneID()
	var pinned, unpinned, collapsed []Column
	for _, c := range s.columns {
		if c.Pinned {
			pinned = append(pinned, c)
		} else if c.Collapsed {
			collapsed = append(collapsed, c)
		} else {
			unpinned = append(unpinned, c)
		}
	}
	s.columns = append(pinned, append(unpinned, collapsed...)...)
	if focusedID != 0 {
		s.FocusPaneID(focusedID)
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

	// Partition pinned, unpinned, and collapsed columns.
	var pinnedCols []Column
	var unpinnedCols []Column
	var collapsedCols []Column
	for _, c := range s.columns {
		if c.Pinned {
			pinnedCols = append(pinnedCols, c)
		} else if c.Collapsed {
			collapsedCols = append(collapsedCols, c)
		} else {
			unpinnedCols = append(unpinnedCols, c)
		}
	}

	placements := make([]Placement, 0, len(s.columns))

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
		placements = append(placements, Placement{
			PaneID: c.PaneID,
			Src:    image.Rect(0, maxSrcY, dstW, maxSrcY+availHeight),
			Dst:    image.Rect(dstX, 1, dstX+dstW, 1+availHeight),
			Frame:  image.Rect(dstX, 1, dstX+dstW, 1+availHeight),
			Z:      0,
			Kind:   protocol.PlacementFull,
		})
	}

	// 2. Place collapsed columns statically on the right.
	collapsedTotalWidth := 0
	for range collapsedCols {
		collapsedTotalWidth += CollapsedColumnWidth + 1
	}

	collapsedStartX := max(pinnedWidth, viewportWidth-collapsedTotalWidth)
	currCollapsedX := collapsedStartX
	for _, c := range collapsedCols {
		dstX := currCollapsedX
		dstW := min(CollapsedColumnWidth, max(0, viewportWidth-dstX))
		currCollapsedX += CollapsedColumnWidth + 1
		if dstW <= 0 {
			continue
		}
		maxSrcY := max(0, c.Height-availHeight)
		placements = append(placements, Placement{
			PaneID: c.PaneID,
			Src:    image.Rect(0, maxSrcY, dstW, maxSrcY+availHeight),
			Dst:    image.Rect(dstX, 1, dstX+dstW, 1+availHeight),
			Frame:  image.Rect(dstX, 1, dstX+dstW, 1+availHeight),
			Z:      0,
			Kind:   protocol.PlacementFull,
		})
	}

	unpinnedStartX := pinnedWidth
	unpinnedViewportWidth := max(0, collapsedStartX-unpinnedStartX)
	if len(unpinnedCols) == 0 || unpinnedViewportWidth <= 0 {
		return placements
	}

	// 3. Compute scrolling placements for unpinned columns.
	colX := make([]int, len(unpinnedCols))
	currX := 0
	for i, c := range unpinnedCols {
		colX[i] = currX
		currX += c.Width + 1
	}

	// Adjust scrollX if an unpinned column is focused.
	focusedPaneID := s.FocusedPaneID()
	focusedUnpinnedIdx := -1
	for i, c := range unpinnedCols {
		if c.PaneID == focusedPaneID {
			focusedUnpinnedIdx = i
			break
		}
	}

	if focusedUnpinnedIdx >= 0 {
		focusColX := colX[focusedUnpinnedIdx]
		focusColW := unpinnedCols[focusedUnpinnedIdx].Width

		if focusColX < s.scrollX {
			s.scrollX = focusColX
		} else if focusColX+focusColW > s.scrollX+unpinnedViewportWidth {
			s.scrollX = focusColX + focusColW - unpinnedViewportWidth
		}
	}
	if s.scrollX < 0 {
		s.scrollX = 0
	}

	unpinnedViewportRect := image.Rect(unpinnedStartX, 1, collapsedStartX, 1+availHeight)
	for i, c := range unpinnedCols {
		screenX := unpinnedStartX + colX[i] - s.scrollX
		screenRect := image.Rect(screenX, 1, screenX+c.Width, 1+availHeight)

		dst := screenRect.Intersect(unpinnedViewportRect)
		if dst.Empty() {
			continue
		}

		srcX := dst.Min.X - screenX
		maxSrcY := max(0, c.Height-availHeight)
		srcY := maxSrcY + (dst.Min.Y - 1)
		src := image.Rect(srcX, srcY, srcX+dst.Dx(), srcY+dst.Dy())
		frame := image.Rect(screenX, 1, screenX+c.Width, 1+availHeight).Intersect(unpinnedViewportRect)

		placements = append(placements, Placement{
			PaneID: c.PaneID,
			Src:    src,
			Dst:    dst,
			Frame:  frame,
			Z:      0,
			Kind:   protocol.PlacementFull,
		})
	}

	return placements
}

// ToProtocol converts Placements to protocol.PlacementData for wire transport.
func ToProtocol(placements []Placement) []protocol.PlacementData {
	out := make([]protocol.PlacementData, len(placements))
	for i, p := range placements {
		frame := p.Frame
		if frame.Empty() {
			frame = p.Dst
		}
		out[i] = protocol.PlacementData{
			PaneID: p.PaneID,
			Src:    p.Src,
			Dst:    p.Dst,
			Frame:  frame,
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
			PaneID:    c.PaneID,
			Width:     c.Width,
			Height:    c.Height,
			Pinned:    c.Pinned,
			Collapsed: c.Collapsed,
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
			PaneID:    c.PaneID,
			Width:     c.Width,
			Height:    c.Height,
			Pinned:    c.Pinned,
			Collapsed: c.Collapsed,
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

// HiddenCounts reports how many columns have no placement, split by which
// side of the focused column they sit on.
func (s *Strip) HiddenCounts(placedPaneIDs []int) (left, right int) {
	if len(s.columns) == 0 {
		return 0, 0
	}

	placed := make(map[int]bool, len(placedPaneIDs))
	for _, id := range placedPaneIDs {
		placed[id] = true
	}

	for i, col := range s.columns {
		if placed[col.PaneID] {
			continue
		}
		if i < s.focusIndex {
			left++
		} else {
			right++
		}
	}
	return left, right
}

// ApplyPan shifts the Src crop rectangle of full-content placements horizontally by panX.
func ApplyPan(placements []Placement, panX map[int]int) {
	for i := range placements {
		if placements[i].Kind == protocol.PlacementFull {
			if dx := panX[placements[i].PaneID]; dx != 0 {
				placements[i].Src = placements[i].Src.Add(image.Pt(dx, 0))
			}
		}
	}
}

// VisibleRect calculates the screen rect of p not painted over by a
// placement drawn after it, narrowed toward pt.
func VisibleRect(p protocol.PlacementData, pt image.Point, sorted []protocol.PlacementData) image.Rectangle {
	r := p.Dst
	above := false
	for _, q := range sorted {
		if q.PaneID == p.PaneID {
			above = true
			continue
		}
		qf := q.Frame
		if qf.Empty() {
			qf = q.Dst
		}
		if !above || !qf.Overlaps(r) {
			continue
		}
		if pt.X < qf.Min.X {
			r.Max.X = min(r.Max.X, qf.Min.X)
		} else {
			r.Min.X = max(r.Min.X, qf.Max.X)
		}
	}
	return r
}
