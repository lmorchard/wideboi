package client

import (
	"image"

	"github.com/lmorchard/wideboi/internal/client/compose"
	"github.com/lmorchard/wideboi/internal/layout"
	"github.com/lmorchard/wideboi/internal/protocol"
)

func (c *Client) clampPanLocked(id int) {
	c.panX[id] = max(0, min(c.panX[id], c.ptyWidths[id]-c.displayWidths[id]))
}

func (c *Client) computePlacementsLocked() []protocol.PlacementData {
	ps := layout.ToProtocol(c.strip.ComputePlacements(c.cols, c.rows))
	for i := range ps {
		if ps[i].Kind == protocol.PlacementFull {
			ps[i].Src = ps[i].Src.Add(image.Pt(c.panX[ps[i].PaneID], 0))
		}
	}
	return ps
}

func (c *Client) revealCursorLocked(id int) {
	info, ok := c.cursorInfos[id]
	if !ok || c.displayWidths[id] <= 0 {
		return
	}
	old := c.panX[id]
	x := info.pt.X
	if x < old {
		c.panX[id] = x
	}
	if x >= old+c.displayWidths[id] {
		c.panX[id] = x - c.displayWidths[id] + 1
	}
	c.clampPanLocked(id)
	if old != c.panX[id] {
		c.updatePlacementsLocked()
	}
}

func (c *Client) hiddenCountsLocked(st frameState) (left, right int) {
	placed := make([]int, len(st.placements))
	for i, p := range st.placements {
		placed[i] = p.PaneID
	}
	return c.strip.HiddenCounts(placed)
}

func (c *Client) openPaneIDsLocked() []int {
	var ids []int
	if c.strip != nil {
		ids = c.strip.PaneIDs()
	}
	if len(ids) == 0 {
		for _, p := range c.placements {
			ids = append(ids, p.PaneID)
		}
	}
	if len(ids) == 0 && c.focusPaneID > 0 {
		ids = []int{c.focusPaneID}
	}
	return ids
}

// currentPlacementsLocked is what is on screen right now: the
// interpolated rects if an animation is running, otherwise the
// settled ones.
func (c *Client) currentPlacementsLocked() []protocol.PlacementData {
	if c.motion != nil {
		return c.motion.at()
	}
	return c.placements
}

func (c *Client) ensureMirrorLocked(paneID, cols, rows int) *PaneMirror {
	mirror, ok := c.mirrors[paneID]
	if !ok || mirror.Cols != cols || mirror.Rows != rows {
		w := cols
		h := rows
		if w <= 0 {
			w = 40
		}
		if h <= 0 {
			h = 20
		}
		newSurf := compose.NewSurface(w, h)
		if ok && mirror.Cols > 0 && mirror.Rows > 0 {
			minW := min(w, mirror.Cols)
			minH := min(h, mirror.Rows)
			for y := 0; y < minH; y++ {
				for x := 0; x < minW; x++ {
					if cell := mirror.Surface.CellAt(x, y); cell != nil {
						newSurf.SetCell(x, y, cell)
					}
				}
			}
		}
		mirror = &PaneMirror{
			ID:      paneID,
			Surface: newSurf,
			Cols:    w,
			Rows:    h,
		}
		c.mirrors[paneID] = mirror
	}
	return mirror
}

func (c *Client) applySnapshotLocked(m protocol.MsgLayoutSnapshot) {
	prevOnScreen := c.currentPlacementsLocked()
	prevTarget := c.placements

	for _, col := range m.Columns {
		if col.PaneID == c.pendingFocusPaneID {
			c.focusPaneID = c.pendingFocusPaneID
			c.pendingFocusPaneID = 0
			break
		}
	}
	if c.focusPaneID == 0 && len(m.Columns) > 0 {
		c.focusPaneID = m.Columns[0].PaneID
	}
	displayColumns := make([]protocol.ColumnData, len(m.Columns))
	copy(displayColumns, m.Columns)
	for i, col := range displayColumns {
		c.ptyWidths[col.PaneID] = col.Width
		if c.displayWidths[col.PaneID] == 0 || c.followPTY {
			c.displayWidths[col.PaneID] = col.Width
		}
		col.Width = c.displayWidths[col.PaneID]
		displayColumns[i] = col
		c.clampPanLocked(col.PaneID)
	}
	c.strip.SyncColumns(displayColumns, c.focusPaneID)
	c.focusPaneID = c.strip.FocusedPaneID()

	c.placements = c.computePlacementsLocked()
	c.paneStatuses = m.PaneStatuses
	c.paneTitles = m.PaneTitles
	if c.sel != nil && !c.selectionStillPlacedLocked() {
		c.sel = nil
	}

	if len(prevTarget) > 0 && c.focusPaneID != 0 && !placementsEqual(prevTarget, c.placements) {
		c.motion = &motion{from: prevOnScreen, to: c.placements, total: motionFrames}
	}

	for _, p := range c.placements {
		if _, ok := c.mirrors[p.PaneID]; !ok {
			w := max(p.Src.Dx(), 40)
			h := max(p.Src.Dy(), 20)
			c.ensureMirrorLocked(p.PaneID, w, h)
		}
	}

	live := make(map[int]bool, len(m.Columns))
	for _, col := range m.Columns {
		live[col.PaneID] = true
	}
	for id := range c.mirrors {
		if !live[id] {
			delete(c.mirrors, id)
			delete(c.paneUpdates, id)
		}
	}
	for id := range c.mouseTracking {
		if !live[id] {
			delete(c.mouseTracking, id)
		}
	}
	for id := range c.paneMetadata {
		if !live[id] {
			delete(c.paneMetadata, id)
		}
	}
	for id := range c.displayWidths {
		if !live[id] {
			delete(c.displayWidths, id)
			delete(c.ptyWidths, id)
			delete(c.panX, id)
			delete(c.pendingReveal, id)
		}
	}
}
