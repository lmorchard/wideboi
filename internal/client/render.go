package client

import (
	"fmt"
	"image"
	"sort"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/lmorchard/wideboi/internal/client/compose"
	"github.com/lmorchard/wideboi/internal/protocol"
)

// frameState is the layout-dependent input to one composed frame.
// Everything else Draw needs -- control mode, prefix label,
// detachable -- is chrome that does not participate in a transition.
type frameState struct {
	placements   []protocol.PlacementData
	focusPaneID  int
	paneStatuses map[int]protocol.PaneStatus
	paneTitles   map[int]string
	// positions is each pane's 1-based column position, which is what
	// the digit keys index. A pane missing from it gets no number.
	positions map[int]int
}

// frameStateLocked snapshots the layout state a frame is composed from.
// c.mu must be held.
func (c *Client) frameStateLocked() frameState {
	statuses := make(map[int]protocol.PaneStatus, len(c.paneStatuses))
	for id := range c.paneStatuses {
		statuses[id] = c.displayStatusLocked(id)
	}
	return frameState{
		placements:   c.placements,
		focusPaneID:  c.focusPaneID,
		paneStatuses: statuses,
		paneTitles:   c.paneTitles,
		positions:    c.positionsLocked(),
	}
}

// sortByZ returns a copy of ps sorted by Z-order back-to-front.
func sortByZ(ps []protocol.PlacementData) []protocol.PlacementData {
	sorted := make([]protocol.PlacementData, len(ps))
	copy(sorted, ps)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Z < sorted[j].Z
	})
	return sorted
}

// frameLocked is the screen rect p occupies: its Dst plus, in the card
// fan, the border cell layout.CardStrategy leaves just left of it. Hit
// testing and occlusion use this; content and selection stay in Dst.
// c.mu must be held.
func (c *Client) frameLocked(p protocol.PlacementData) image.Rectangle {
	if !p.Frame.Empty() {
		return p.Frame
	}
	r := p.Dst
	if c.layoutModeLocked() == protocol.LayoutCards && r.Min.X > 0 {
		r.Min.X--
	}
	return r
}

// composeFrameLocked draws pane headers, pane content and column
// dividers for st into dst, and returns the focused placement (nil if
// st has none).
func (c *Client) composeFrameLocked(dst uv.Screen, st frameState) *protocol.PlacementData {
	var focusedPlacement *protocol.PlacementData

	sorted := sortByZ(st.placements)

	for i := range sorted {
		p := &sorted[i]
		if p.PaneID == st.focusPaneID {
			focusedPlacement = p
		}

		// Draw 1-row pane header bar at Y = 0, across the card's border
		// cell too, so the header row has no gap in it.
		frame := c.frameLocked(*p)
		headerW := frame.Dx()
		if headerW > 0 {
			c.drawPaneHeaderLocked(dst, frame.Min.X, 0, headerW, p.PaneID, p.PaneID == st.focusPaneID, st)
		}

		if mirror, ok := c.mirrors[p.PaneID]; ok {
			compose.BlitSource(dst, mirror.Surface, p.Src, p.Dst)
		}
		if pu, ok := c.paneUpdates[p.PaneID]; ok && pu.ScrollOffset > 0 && p.Dst.Dy() > 1 {
			footerY := p.Dst.Max.Y - 1
			footerW := p.Dst.Dx()
			footerText := fmt.Sprintf(" [▲ scroll +%d/%d]", pu.ScrollOffset, pu.ScrollbackLen)
			if pu.UnreadOutput {
				full := fmt.Sprintf(" [▲ scroll +%d/%d  ▼ new output]", pu.ScrollOffset, pu.ScrollbackLen)
				compact := fmt.Sprintf(" [▲ scroll +%d/%d  ⤓ new]", pu.ScrollOffset, pu.ScrollbackLen)
				minimal := fmt.Sprintf(" [▲ +%d/%d ⤓]", pu.ScrollOffset, pu.ScrollbackLen)
				switch {
				case compose.StringWidth(dst, full) <= footerW:
					footerText = full
				case compose.StringWidth(dst, compact) <= footerW:
					footerText = compact
				default:
					footerText = minimal
				}
			}
			footerText = compose.TruncateWidth(dst, footerText, footerW)
			if used := compose.StringWidth(dst, footerText); used < footerW {
				footerText += strings.Repeat(" ", footerW-used)
			}
			compose.WriteStyled(dst, p.Dst.Min.X, footerY, footerText, uv.Style{Attrs: uv.AttrReverse})
		}

		// Draw column divider on right edge if applicable.
		// Bold ┃ if adjacent to focused pane, otherwise │.
		if c.layoutModeLocked() != protocol.LayoutCards && p.Dst.Max.X < c.cols {
			// Find if the adjacent pane in the original slice is focused
			var adjacentFocused bool
			for j, orig := range st.placements {
				if orig.PaneID == p.PaneID {
					if j+1 < len(st.placements) && st.placements[j+1].PaneID == st.focusPaneID {
						adjacentFocused = true
					}
					break
				}
			}

			divider := "│"
			style := c.theme.Divider
			if adjacentFocused {
				divider = "┃"
				style = c.theme.FocusDivider
			}
			for y := p.Dst.Min.Y; y < p.Dst.Max.Y; y++ {
				compose.WriteStyled(dst, p.Dst.Max.X, y, divider, style)
			}
		}

		if c.layoutModeLocked() == protocol.LayoutCards {
			// For overlapping cards, the left edge is the visible boundary
			// that occludes the card to its left. It goes in the border
			// cell layout leaves before Dst, not over the card's content.
			if frame.Min.X < p.Dst.Min.X {
				divider := "│"
				style := c.theme.Divider
				if p.PaneID == st.focusPaneID {
					divider = "┃"
					style = c.theme.FocusDivider
				}
				for y := p.Dst.Min.Y; y < p.Dst.Max.Y; y++ {
					compose.WriteStyled(dst, frame.Min.X, y, divider, style)
				}
			}
			// Additionally, the focused card is the top-most card, so its right edge is also fully visible.
			if p.PaneID == st.focusPaneID && p.Dst.Max.X < c.cols {
				for y := p.Dst.Min.Y; y < p.Dst.Max.Y; y++ {
					compose.WriteStyled(dst, p.Dst.Max.X, y, "│", c.theme.Divider)
				}
			}
		}
	}

	c.drawHiddenMarkersLocked(dst, st)

	return focusedPlacement
}

func (c *Client) drawPaneHeaderLocked(dst uv.Screen, x, y, width, id int, isFocus bool, st frameState) {
	headerStyle := c.theme.Header
	if isFocus {
		headerStyle = c.theme.HeaderFocus
	}
	// Pre-fill the header row across width with the header background style.
	compose.WriteStyled(dst, x, y, strings.Repeat(" ", width), headerStyle)

	curX := x
	maxX := x + width

	writeCell := func(r string, s uv.Style) {
		w := compose.StringWidth(dst, r)
		if curX+w <= maxX {
			compose.WriteStyled(dst, curX, y, r, s)
			curX += w
		}
	}

	// Leading prefix (position or space).
	if pos := st.positions[id]; pos > 0 {
		for _, r := range fmt.Sprintf(" %d ", pos) {
			writeCell(string(r), headerStyle)
		}
	} else {
		writeCell(" ", headerStyle)
	}

	// Status capsule.
	curX = drawCapsule(dst, curX, y, maxX, id, isFocus, st.paneStatuses[id], c.theme, headerStyle.Bg)

	// Title.
	title := st.paneTitles[id]
	if title != "" && curX < maxX {
		writeCell(" ", headerStyle)
		for _, r := range title {
			writeCell(string(r), headerStyle)
		}
	}
}

func drawCapsule(dst uv.Screen, x, y, maxX, id int, isFocus bool, status protocol.PaneStatus, theme Theme, bg ansi.Color) int {
	focusStr, idStr, statusStr := BadgeComponents(id, isFocus, status)

	applyBg := func(s uv.Style) uv.Style {
		if bg != nil {
			s.Bg = bg
		}
		return s
	}
	baseStyle := applyBg(uv.Style{})

	write := func(s string, style uv.Style) {
		w := compose.StringWidth(dst, s)
		if x+w <= maxX {
			compose.WriteStyled(dst, x, y, s, style)
			x += w
		}
	}

	// "["
	write("[", applyBg(theme.Dim))

	// focus slot ("●" or " ")
	if isFocus {
		write(focusStr, applyBg(theme.Focus))
	} else {
		write(" ", baseStyle)
	}

	// " "
	spaceStyle := baseStyle
	idStyle := baseStyle
	if isFocus {
		spaceStyle = applyBg(theme.Focus)
		idStyle = applyBg(theme.Focus)
	}
	write(" ", spaceStyle)

	// idStr
	write(idStr, idStyle)

	// " "
	write(" ", baseStyle)

	// status slot
	if status != protocol.StatusIdle {
		write(statusStr, applyBg(theme.StatusStyle(status)))
	} else {
		write(" ", baseStyle)
	}

	// "]"
	write("]", applyBg(theme.Dim))

	return x
}

func (c *Client) writeBadgeLocked(scr uv.Screen, x, y int, id int, isFocus bool, status protocol.PaneStatus) {
	drawCapsule(scr, x, y, c.cols, id, isFocus, status, c.theme, nil)
}

func (c *Client) drawHiddenMarkersLocked(dst uv.Screen, st frameState) {
	left, right := c.hiddenCountsLocked(st)
	if left > 0 {
		compose.WriteStyled(dst, 0, 0, fmt.Sprintf("+%d", left), uv.Style{Attrs: uv.AttrBold})
	}
	if right > 0 {
		s := fmt.Sprintf("+%d", right)
		x := c.cols - 1 - runeLen(s)
		if x < 0 {
			x = 0
		}
		compose.WriteStyled(dst, x, 0, s, uv.Style{Attrs: uv.AttrBold})
	}
}
