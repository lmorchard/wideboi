// Package client owns the host terminal rendering, composition, and input routing.
package client

import (
	"context"
	"fmt"
	"image"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/client/compose"
	"github.com/lmorchard/wideboi/internal/keys"
	"github.com/lmorchard/wideboi/internal/layout"
	"github.com/lmorchard/wideboi/internal/logger"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

// PaneMirror holds client-side surface and dirty state for a single pane.
type PaneMirror struct {
	ID      int
	Surface compose.Surface
	Cols    int
	Rows    int
}

type cursorPos struct {
	pt      image.Point
	visible bool
}

// frameState is the layout-dependent input to one composed frame.
// Everything else Draw needs -- control mode, prefix label,
// detachable -- is chrome that does not participate in a transition.
type frameState struct {
	placements   []protocol.PlacementData
	focusPaneID  int
	paneStatuses map[int]string
	paneTitles   map[int]string
	// positions is each pane's 1-based column position, which is what
	// the digit keys index. A pane missing from it gets no number.
	positions map[int]int
}

// Client manages screen rendering, off-screen mirrors, and input forwarding.
type Client struct {
	mu           sync.Mutex
	transport    transport.Transport
	cols         int
	rows         int
	strip        *layout.Strip
	placements   []protocol.PlacementData
	focusPaneID  int
	paneStatuses map[int]string
	paneTitles   map[int]string
	layoutMode   protocol.LayoutMode
	mirrors      map[int]*PaneMirror
	cursorInfos  map[int]cursorPos
	prefixLabel  string
	controlMode  bool
	helpVisible  bool
	detachable   bool
	bindings     []keys.Binding
	motion       *motion
	sel          *selection
	// mouseTracking is which panes' children have asked for mouse
	// events, from MsgPaneUpdate. grab is a drag being forwarded to one.
	mouseTracking map[int]bool
	grab          *mouseGrab

	stagingScreen      *offscreenHostScreen
	lastRenderedScreen *offscreenHostScreen
	lastHostScreen     HostScreen
}

// frameStateLocked snapshots the layout state a frame is composed from.
// c.mu must be held.
func (c *Client) frameStateLocked() frameState {
	return frameState{
		placements:   c.placements,
		focusPaneID:  c.focusPaneID,
		paneStatuses: c.paneStatuses,
		paneTitles:   c.paneTitles,
		positions:    c.positionsLocked(),
	}
}

// positionsLocked maps each pane to its 1-based column position.
// c.mu must be held.
func (c *Client) positionsLocked() map[int]int {
	ids := c.strip.PaneIDs()
	pos := make(map[int]int, len(ids))
	for i, id := range ids {
		pos[id] = i + 1
	}
	return pos
}

// NewClient initializes a Client instance. prefixLabel is the short
// display form of the configured prefix key ("C-b"), used in the
// normal-mode hint -- the client never sees the key itself, only how to
// name it.
func NewClient(tp transport.Transport, cols, rows int, prefixLabel string) *Client {
	return &Client{
		transport:   tp,
		cols:        cols,
		rows:        rows,
		strip:       layout.NewStrip(),
		prefixLabel: prefixLabel,
		mirrors:     make(map[int]*PaneMirror),
		cursorInfos: make(map[int]cursorPos),
	}
}

func (c *Client) SetTransport(tp transport.Transport) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.transport = tp
}

// Attach sends the initial MsgAttach protocol message to the server.
func (c *Client) Attach(ctx context.Context) {
	c.transport.SendClient(ctx, protocol.MsgAttach{Cols: c.cols, Rows: c.rows})
}

// HandleServerMsg processes messages received from the server.
func (c *Client) HandleServerMsg(msg transport.ServerMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch m := msg.(type) {
	case protocol.MsgLayoutSnapshot:
		slog.Debug("received MsgLayoutSnapshot", "cols", len(m.Columns), "focusPaneID", m.FocusPaneID)
		oldFocus := c.focusPaneID
		// Two different "previous" values, and the distinction
		// matters.
		//
		// prevTarget is the layout we were heading to, and is what
		// decides whether anything moved. prevOnScreen is where the
		// pixels are right now, and is where a new animation starts
		// so a change mid-flight continues rather than jumping.
		//
		// Comparing against prevOnScreen instead would re-arm on
		// every frame of a running animation, because an
		// interpolated layout never equals its target -- and
		// broadcastLayoutIfStatusChanged sends a snapshot every time
		// a busy pane's glyph changes, so the motion would reset its
		// step counter repeatedly and never settle.
		prevTarget := c.placements
		prevOnScreen := c.currentPlacementsLocked()
		// Unconditionally, before anything below reads it: the strip
		// is what hiddenCountsLocked compares placements against.
		//
		// It used to be inside the len(m.Columns) > 0 branch, which
		// meant the snapshot sent when the last pane closes left a
		// stale strip behind -- so card mode drew a "+N" counting
		// panes that no longer existed.
		//
		// The layout mode is not read from the snapshot: it is this
		// client's own (#92), set by SetLayoutMode and ToggleLayout.
		c.strip.SyncColumns(m.Columns, m.FocusPaneID)

		// With no columns ComputePlacements returns nil, which is what
		// an empty session should draw. The server sent its own
		// placements for that case until #47; they were always nil.
		c.placements = layout.ToProtocol(c.strip.ComputePlacements(c.cols, c.rows))
		c.focusPaneID = m.FocusPaneID
		c.paneStatuses = m.PaneStatuses
		c.paneTitles = m.PaneTitles
		if c.sel != nil && !c.selectionStillPlacedLocked() {
			c.sel = nil
		}

		// Animate whenever the geometry moved, not only on a focus
		// change: opening and killing a column re-deal the fan too.
		// Starting from what is currently on screen rather than from
		// the pre-animation layout is what lets a second change
		// mid-flight continue rather than jump.
		if oldFocus != 0 && !placementsEqual(prevTarget, c.placements) {
			c.motion = &motion{from: prevOnScreen, to: c.placements, total: motionFrames}
		}

		for _, p := range c.placements {
			m, ok := c.mirrors[p.PaneID]
			if !ok {
				// Allocate surface sized to logical width/height
				w := p.Src.Dx()
				h := p.Src.Dy()
				if w <= 0 {
					w = 40
				}
				if h <= 0 {
					h = 20
				}
				c.mirrors[p.PaneID] = &PaneMirror{
					ID:      p.PaneID,
					Surface: compose.NewSurface(w, h),
					Cols:    w,
					Rows:    h,
				}
			} else if p.Src.Dx() > m.Cols || p.Src.Dy() > m.Rows {
				m.Cols = max(m.Cols, p.Src.Dx())
				m.Rows = max(m.Rows, p.Src.Dy())
				m.Surface = compose.NewSurface(m.Cols, m.Rows)
			}
		}

		// Prune against the panes that exist, not the ones placed. A
		// local ToggleLayout changes placements with no snapshot and
		// no resend, so a mirror pruned for being off-screen would come
		// back blank until its pane next changed (#92).
		live := make(map[int]bool, len(m.Columns))
		for _, col := range m.Columns {
			live[col.PaneID] = true
		}
		for id := range c.mirrors {
			if !live[id] {
				delete(c.mirrors, id)
			}
		}
		for id := range c.mouseTracking {
			if !live[id] {
				delete(c.mouseTracking, id)
			}
		}

	case protocol.MsgPaneUpdate:
		// Trace, not Debug: busy panes still send one of these per
		// server frame.
		slog.Log(context.Background(), logger.LevelTrace, "received MsgPaneUpdate", "paneID", m.PaneID, "cols", m.Cols, "rows", m.Rows)
		mirror, ok := c.mirrors[m.PaneID]
		if !ok || mirror.Cols != m.Cols || mirror.Rows != m.Rows {
			mirror = &PaneMirror{
				ID:      m.PaneID,
				Surface: compose.NewSurface(m.Cols, m.Rows),
				Cols:    m.Cols,
				Rows:    m.Rows,
			}
			c.mirrors[m.PaneID] = mirror
		}
		for y, line := range m.Lines {
			currX := 0
			for _, cell := range line {
				uvCell := uv.NewCell(mirror.Surface.WidthMethod(), cell.Content)
				uvCell.Style = cell.Style.Decode()
				mirror.Surface.SetCell(currX, y, uvCell)
				w := cell.Width
				if w <= 0 {
					w = 1
				}
				currX += w
			}
		}
		if c.cursorInfos == nil {
			c.cursorInfos = make(map[int]cursorPos)
		}
		c.cursorInfos[m.PaneID] = cursorPos{
			pt:      image.Pt(m.CursorX, m.CursorY),
			visible: m.CursorVisible,
		}
		if c.mouseTracking == nil {
			c.mouseTracking = make(map[int]bool)
		}
		c.mouseTracking[m.PaneID] = m.MouseTracking
	}
}

// drawLayer names what Draw paints this frame.
//
// Two layers now, not three. The wipe had its own because it took
// over the whole screen and drew from composed snapshots; motion
// does not -- it feeds interpolated rects through the ordinary pane
// path, so there is nothing to arbitrate between.
//
// Kept as a named type rather than a bare bool because the help
// overlay's precedence is the thing worth being able to assert: as
// inline statement order it was observable only through a real
// terminal.
type drawLayer int

const (
	layerPanes drawLayer = iota
	layerHelp
)

// layerLocked reports which layer owns this frame. c.mu must be held.
//
// Help outranks everything: an animation is decorative, a modal is
// not.
func (c *Client) layerLocked() drawLayer {
	if c.helpVisible {
		return layerHelp
	}
	return layerPanes
}

// HostScreen is everything Draw needs from the host terminal: a cell
// surface, plus cursor control.
//
// Draw used to take *uv.TerminalScreen concretely. That is a type a unit
// test cannot cheaply build, which is why nothing ever called Draw from
// a test -- and why a wipe that interpolated two blank frames shipped
// green. uv.ScreenBuffer satisfies uv.Screen but has no cursor methods,
// so the three Draw actually uses are named here and a test double
// supplies them.
type HostScreen interface {
	uv.Screen
	HideCursor()
	ShowCursor()
	SetCursorPosition(x, y int)
}

type offscreenHostScreen struct {
	compose.Surface
	cursorShown bool
	cursorX     int
	cursorY     int
}

func newOffscreenHostScreen(cols, rows int) *offscreenHostScreen {
	return &offscreenHostScreen{
		Surface: compose.NewSurface(cols, rows),
	}
}

func (s *offscreenHostScreen) HideCursor()                { s.cursorShown = false }
func (s *offscreenHostScreen) ShowCursor()                { s.cursorShown = true }
func (s *offscreenHostScreen) SetCursorPosition(x, y int) { s.cursorX, s.cursorY = x, y }

func (s *offscreenHostScreen) clear() {
	s.Surface.Clear()
	s.cursorShown = false
	s.cursorX = 0
	s.cursorY = 0
}

func (s *offscreenHostScreen) equal(other *offscreenHostScreen) bool {
	if s == nil || other == nil {
		return false
	}
	if s.cursorShown != other.cursorShown {
		return false
	}
	if s.cursorShown && (s.cursorX != other.cursorX || s.cursorY != other.cursorY) {
		return false
	}
	b1 := s.Bounds()
	b2 := other.Bounds()
	if b1 != b2 {
		return false
	}
	w, h := b1.Dx(), b1.Dy()
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c1 := s.CellAt(x, y)
			c2 := other.CellAt(x, y)
			if c1 == c2 {
				continue
			}
			if c1 == nil || c2 == nil {
				return false
			}
			if !c1.Equal(c2) {
				return false
			}
		}
	}
	return true
}

func copyToHostScreen(src *offscreenHostScreen, dst HostScreen) {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	for y := 0; y < h; y++ {
		for x := 0; x < w; {
			c := src.CellAt(x, y)
			if c == nil {
				x++
				continue
			}
			dst.SetCell(x, y, c)
			width := c.Width
			if width <= 0 {
				width = 1
			}
			x += width
		}
	}
	if src.cursorShown {
		dst.SetCursorPosition(src.cursorX, src.cursorY)
		dst.ShowCursor()
	} else {
		dst.HideCursor()
	}
}

// Draw composites active pane surfaces, dividers, host cursor, and status bar onto host screen scr.
// It returns true if the frame changed and was written to scr, or false if unchanged.
func (c *Client) Draw(scr HostScreen) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.stagingScreen == nil || c.stagingScreen.Bounds().Dx() != c.cols || c.stagingScreen.Bounds().Dy() != c.rows {
		c.stagingScreen = newOffscreenHostScreen(c.cols, c.rows)
	}
	c.stagingScreen.clear()

	c.drawToScreenLocked(c.stagingScreen)

	targetChanged := scr != c.lastHostScreen
	if !targetChanged && c.stagingScreen.equal(c.lastRenderedScreen) {
		return false
	}

	copyToHostScreen(c.stagingScreen, scr)
	c.lastHostScreen = scr

	if c.lastRenderedScreen == nil || c.lastRenderedScreen.Bounds().Dx() != c.cols || c.lastRenderedScreen.Bounds().Dy() != c.rows {
		c.lastRenderedScreen = newOffscreenHostScreen(c.cols, c.rows)
	}
	copyToHostScreen(c.stagingScreen, c.lastRenderedScreen)

	return true
}

func (c *Client) drawToScreenLocked(scr HostScreen) {
	if c.layerLocked() == layerHelp {
		drawHelpOverlay(scr, c.cols, c.rows, c.prefixLabel, c.detachable, c.bindings)
		scr.HideCursor()
		return
	}

	st := c.frameStateLocked()
	animating := c.motion != nil
	if animating {
		st.placements = c.motion.at()
		c.motion.step++
		if c.motion.done() {
			c.motion = nil
		}
	}

	focusedPlacement := c.composeFrameLocked(scr, st)
	c.drawSelectionLocked(scr)
	c.drawStatusBarLocked(scr)

	// The cursor belongs to a pane that is sliding, so its position
	// is meaningless mid-flight. The wipe hid it too; that part was
	// right.
	if animating {
		scr.HideCursor()
		return
	}

	// Host cursor position and visibility.
	//
	// In control mode the cursor is hidden outright. Keystrokes are not
	// reaching the pane, so a blinking pane cursor would be claiming
	// otherwise -- and unlike the bar's inversion, cursor visibility is
	// a DECTCEM escape on the wire, which is what lets smoke.py assert
	// that the mode was entered at all.
	switch {
	case c.controlMode:
		scr.HideCursor()
	case focusedPlacement != nil:
		var cp image.Point
		var visible bool
		if info, ok := c.cursorInfos[c.focusPaneID]; ok {
			cp, visible = info.pt, info.visible
		}
		fx := focusedPlacement.Dst.Min.X + cp.X - focusedPlacement.Src.Min.X
		fy := focusedPlacement.Dst.Min.Y + cp.Y - focusedPlacement.Src.Min.Y
		if visible && fx >= focusedPlacement.Dst.Min.X && fx <= focusedPlacement.Dst.Max.X &&
			fy >= focusedPlacement.Dst.Min.Y && fy <= focusedPlacement.Dst.Max.Y {
			fx = min(fx, max(focusedPlacement.Dst.Max.X-1, 0))
			fy = min(fy, max(focusedPlacement.Dst.Max.Y-1, 0))
			scr.SetCursorPosition(fx, fy)
			scr.ShowCursor()
		} else {
			scr.HideCursor()
		}
	default:
		scr.HideCursor()
	}
}

// composeFrameLocked draws pane headers, pane content and column
// dividers for st into dst, and returns the focused placement (nil if
// st has none).
//
// It deliberately stops short of the status bar and the cursor. The bar
// is chrome that should not dissolve mid-transition, and the cursor is
// hidden for a wipe's duration anyway, so a composed frame covers rows
// 0..rows-2 only -- headers at row 0, panes and dividers from
// Dst.Min.Y = 1 up to Dst.Max.Y = rows-1 exclusive, since
// layout.AvailHeight is rows-2.
//
// Taking st rather than reading c directly is what lets a wipe compose
// the frame it is animating away from. c.mu must be held.
func (c *Client) composeFrameLocked(dst uv.Screen, st frameState) *protocol.PlacementData {
	var focusedPlacement *protocol.PlacementData

	// Sort placements by Z-order back-to-front for rendering.
	// We copy the slice so we don't mutate the frameState's slice,
	// which might be expected to remain in layout order elsewhere.
	sorted := make([]protocol.PlacementData, len(st.placements))
	copy(sorted, st.placements)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Z < sorted[j].Z
	})

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
			glyph := st.paneStatuses[p.PaneID]
			header := fmt.Sprintf(" [%d]", p.PaneID)
			if pos := st.positions[p.PaneID]; pos > 0 {
				header = fmt.Sprintf(" %d [%d]", pos, p.PaneID)
			}
			if glyph != "" && glyph != " " {
				header += " " + glyph
			}
			title := st.paneTitles[p.PaneID]
			if title != "" {
				header += " " + title
			}
			if p.PaneID == st.focusPaneID {
				header += " ★"
			}
			header = compose.TruncateWidth(dst, header, headerW)
			if used := compose.StringWidth(dst, header); used < headerW {
				header += strings.Repeat(" ", headerW-used)
			}

			if p.PaneID == st.focusPaneID {
				compose.WriteStyled(dst, frame.Min.X, 0, header, uv.Style{Attrs: uv.AttrReverse})
			} else {
				compose.WriteString(dst, frame.Min.X, 0, header)
			}
		}

		switch {
		case p.Kind == protocol.PlacementSliver:
			c.drawSliverLocked(dst, p, st)
		default:
			if mirror, ok := c.mirrors[p.PaneID]; ok {
				compose.Blit(dst, mirror.Surface, p.Dst)
			}
		}

		// Draw column divider on right edge if applicable.
		// Bold ┃ if adjacent to focused pane, otherwise │.
		if c.layoutMode != protocol.LayoutCards && p.Dst.Max.X < c.cols {
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
			if p.PaneID == st.focusPaneID || adjacentFocused {
				divider = "┃"
			}
			for y := p.Dst.Min.Y; y < p.Dst.Max.Y; y++ {
				compose.WriteString(dst, p.Dst.Max.X, y, divider)
			}
		}

		if c.layoutMode == protocol.LayoutCards {
			// For overlapping cards, the left edge is the visible boundary
			// that occludes the card to its left. It goes in the border
			// cell layout leaves before Dst, not over the card's content.
			if frame.Min.X < p.Dst.Min.X {
				divider := "│"
				if p.PaneID == st.focusPaneID {
					divider = "┃"
				}
				for y := p.Dst.Min.Y; y < p.Dst.Max.Y; y++ {
					compose.WriteString(dst, frame.Min.X, y, divider)
				}
			}
			// Additionally, the focused card is the top-most card, so its right edge is also fully visible.
			if p.PaneID == st.focusPaneID && p.Dst.Max.X < c.cols {
				for y := p.Dst.Min.Y; y < p.Dst.Max.Y; y++ {
					compose.WriteString(dst, p.Dst.Max.X, y, "┃")
				}
			}
		}
	}

	// Takes st, not c: composeFrameLocked also builds a wipe's
	// before-frame, and a marker counted from the current placements
	// would be wrong there for the same reason stale placements would
	// be.
	c.drawHiddenMarkersLocked(dst, st)

	return focusedPlacement
}

// drawSliverLocked renders an occluded card as chrome rather than a
// peek at its content.
//
// Four columns of someone else's terminal output is visual noise --
// issue #21 and the card design notes -- so a sliver shows what is worth
// knowing about a pane you are not looking at: whether it wants you,
// what it is doing, and whether it is doing anything at all.
//
// The title is the good part. An agent harness keeps it current --
// Claude Code writes a spinner and a summary of the turn into it, and
// emits no OSC 133 at all -- so for the workload this project exists
// for, the title is the status signal that actually exists.
//
// Row 0 is the pane header, drawn by the caller. This fills the rest.
// c.mu must be held.
func (c *Client) drawSliverLocked(dst uv.Screen, p *protocol.PlacementData, st frameState) {
	w := p.Dst.Dx()
	if w <= 0 {
		return
	}

	glyph := st.paneStatuses[p.PaneID]
	if glyph == " " {
		glyph = ""
	}
	title := st.paneTitles[p.PaneID]

	// Glyph and title on the first row, whichever of them exists. A
	// pane whose child never set a title still gets its glyph, so the
	// row never looks like a rendering fault.
	label := strings.TrimSpace(glyph + " " + title)
	if label != "" {
		compose.WriteStyled(dst, p.Dst.Min.X, p.Dst.Min.Y,
			compose.TruncateWidth(dst, label, w), uv.Style{})
	}

	// A spine below it, bright while the pane is producing output.
	//
	// The activity signal is the status glyph rather than a new wire
	// field: PaneStatuses already carries exactly this. Write's
	// heuristic sets StatusWorking on every write and lets it decay
	// after three seconds of quiet, so "»" means "this pane is doing
	// something right now" without anything further crossing the
	// socket.
	spine := uv.Style{}
	if glyph == "»" {
		spine = uv.Style{Attrs: uv.AttrBold}
	}
	for y := p.Dst.Min.Y + 1; y < p.Dst.Max.Y; y++ {
		compose.WriteStyled(dst, p.Dst.Min.X, y, "▌", spine)
	}
}

// hiddenCountsLocked reports how many columns have no placement,
// split by which side of the focused column they sit on.
//
// Both strategies leave some columns unplaced: CardStrategy the cards
// outside its window, ScrollStrategy any pane scrolled fully out of
// view. A pane that exists and is invisible with nothing to say so
// erodes trust in the layout.
//
// Derived here rather than carried on the wire: the client already
// holds the strip, and widening the Strategy interface to return a
// second value for one caller's benefit is a worse trade. Not stored
// on frameState either -- a retained wipe frame would carry a stale
// count for the same reason it would carry stale placements.
//
// c.mu must be held.
func (c *Client) hiddenCountsLocked(st frameState) (left, right int) {
	cols := c.strip.Columns()
	if len(cols) == 0 {
		return 0, 0
	}

	placed := make(map[int]bool, len(st.placements))
	for _, p := range st.placements {
		placed[p.PaneID] = true
	}

	focusIdx := 0
	for i, col := range cols {
		if col.PaneID == st.focusPaneID {
			focusIdx = i
			break
		}
	}

	for i, col := range cols {
		if placed[col.PaneID] {
			continue
		}
		if i < focusIdx {
			left++
		} else {
			right++
		}
	}
	return left, right
}

// drawHiddenMarkersLocked writes a "+N" on the header row for columns
// that have no placement, at whichever edge they fell off.
//
// Both strategies drop columns: CardStrategy the cards outside its
// window, ScrollStrategy any pane scrolled fully out of view (issue
// #48). The header row is already chrome, so neither has to reserve
// space for this. c.mu must be held.
func (c *Client) drawHiddenMarkersLocked(dst uv.Screen, st frameState) {
	left, right := c.hiddenCountsLocked(st)
	if left > 0 {
		compose.WriteStyled(dst, 0, 0, fmt.Sprintf("+%d", left), uv.Style{Attrs: uv.AttrBold})
	}
	if right > 0 {
		// One cell short of the edge: a write to the last column gets
		// autowrap-toggle escapes spliced into it on the wire.
		s := fmt.Sprintf("+%d", right)
		x := c.cols - 1 - runeLen(s)
		if x < 0 {
			x = 0
		}
		compose.WriteStyled(dst, x, 0, s, uv.Style{Attrs: uv.AttrBold})
	}
}

// drawStatusBarLocked paints the bottom row. c.mu must be held.
//
// Leave the final column untouched: ultraviolet's terminal renderer
// writes the last cell of a row with autowrap toggled off and back on
// around it, which splits whatever glyph lands there across a mode
// escape sequence on the wire. Budgeting one cell short of c.cols keeps
// the whole line contiguous in the raw output.
func (c *Client) drawStatusBarLocked(scr uv.Screen) {
	statusText, statusStyle := c.statusLineLocked(c.cols - 1)
	compose.WriteStyled(scr, 0, c.rows-1, statusText, statusStyle)
}

// currentPlacementsLocked is what is on screen right now: the
// interpolated rects if an animation is running, otherwise the
// settled ones.
//
// Arming a new animation from here rather than from c.placements is
// what makes a second focus change mid-flight continue from what the
// user is looking at instead of jumping back. c.mu must be held.
func (c *Client) currentPlacementsLocked() []protocol.PlacementData {
	if c.motion != nil {
		return c.motion.at()
	}
	return c.placements
}

// frameLocked is the screen rect p occupies: its Dst plus, in the card
// fan, the border cell layout.CardStrategy leaves just left of it. Hit
// testing and occlusion use this; content and selection stay in Dst.
// c.mu must be held.
func (c *Client) frameLocked(p protocol.PlacementData) image.Rectangle {
	r := p.Dst
	if c.layoutMode == protocol.LayoutCards && r.Min.X > 0 {
		r.Min.X--
	}
	return r
}

// controlHelp returns as much of the control-mode menu as fits in budget
// cells, always including the entries keys marks Essential.
//
// Both halves come from internal/keys, so the bar cannot advertise a
// binding the router does not have, or omit one it does. That used to be
// guaranteed by scripts/smoke.py comparing two hardcoded lists of the
// same strings, which only ever caught drift someone remembered to
// assert.
//
// Dropping starts from the end of the droppable list. The essential
// entries are never dropped: with no unprefixed escape hatch, a user who
// cannot read "q quit" and "esc exit" out of the bar has no way forward
// except a signal.
func controlHelp(budget int, detachable bool, custom ...[]keys.Binding) string {
	bindings := keys.Bindings
	if len(custom) > 0 && len(custom[0]) > 0 {
		bindings = custom[0]
	}
	droppable, essential := keys.BarItemsFor(bindings, detachable)

	tail := strings.Join(essential, "  ")
	used := runeLen(tail)

	taken := make([]string, 0, len(droppable))
	for _, v := range droppable {
		if used+2+runeLen(v) > budget {
			break
		}
		used += 2 + runeLen(v)
		taken = append(taken, v)
	}

	return strings.Join(append(taken, tail), "  ")
}

// statusLineLocked returns the bottom row's text and the style every one
// of its cells carries. c.mu must be held.
//
// It returns the style rather than drawing, because Draw needs a
// *uv.TerminalScreen that a unit test cannot cheaply build -- this is
// what makes the control-mode inversion assertable at all. That the
// style actually reaches the wire is proved by scripts/smoke.py.
func (c *Client) statusLineLocked(budget int) (string, uv.Style) {
	if budget < 0 {
		budget = 0
	}
	if c.controlMode {
		menu := truncateRunes(controlHelp(budget, c.detachable, c.bindings), budget)
		// Pad to the full budget: a partly-inverted row reads as a
		// rendering glitch, not as a mode.
		menu += strings.Repeat(" ", budget-runeLen(menu))
		return menu, uv.Style{Attrs: uv.AttrReverse}
	}
	return c.normalStatusLocked(budget), uv.Style{}
}

// normalStatusLocked builds the ordinary status line: what is focused,
// which panes want attention, and how to reach the verbs. c.mu must be
// held.
func (c *Client) normalStatusLocked(budget int) string {
	status := fmt.Sprintf("focus: [pane %d ★]", c.focusPaneID)
	for _, p := range c.placements {
		if glyph, ok := c.paneStatuses[p.PaneID]; ok && glyph != "" && glyph != " " {
			status += fmt.Sprintf("  [%d %s]", p.PaneID, glyph)
		}
	}
	// The right-hand side is the layout tag and then the hint, and it
	// degrades hint first: the pane statuses and the mode are live
	// state -- an accidental C-b c changes the mode (#91) -- while the
	// hint is a fixed string a user learns once. Right-aligned because
	// the left edge is pinned: smoke.py finds the focused pane by its
	// column in "focus: [pane N".
	mode := c.layoutMode.String()
	for _, right := range []string{mode + " · " + c.prefixLabel + " for commands", mode} {
		if pad := budget - runeLen(status) - runeLen(right); pad >= 2 {
			return truncateRunes(status+strings.Repeat(" ", pad)+right, budget)
		}
	}
	return truncateRunes(status, budget)
}

// SetDetachable declares whether this client can leave its session
// running behind it -- true only when it reached the server over a
// socket, so the panes belong to a process that outlives it.
//
// It gates the "d detach" entry in the control-mode menu. The zero
// value is false on purpose: a client that forgets to call this hides a
// verb it could have offered, which is a smaller lie than a client that
// advertises detach and then kills the user's session. An in-process
// wideboi owns its panes directly, so "detaching" there could only ever
// mean quitting.
func (c *Client) SetDetachable(on bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.detachable = on
}

// SetBindings configures custom control-mode bindings for status bar display
// and the help overlay.
func (c *Client) SetBindings(b []keys.Binding) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.bindings = b
}

// SetControlMode switches the client between forwarding keys to the
// focused pane and showing the verb menu. Called by cmd/wideboi after
// every key event, so the bar can never disagree with the router.
func (c *Client) SetControlMode(on bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.controlMode = on
}

// SetHelpVisible raises or clears the control-mode help overlay. Called
// by cmd/wideboi after every key from the router's own help flag, the
// same mirroring SetControlMode uses, so the overlay can never disagree
// with the router about whether it is up.
func (c *Client) SetHelpVisible(on bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.helpVisible = on
}

// SetLayoutMode installs this client's layout. Layout is presentation,
// and presentation is per-client (#92): the server never learns it, so
// cmd/wideboi calls this once, from the client's own config, before the
// first snapshot arrives.
func (c *Client) SetLayoutMode(mode protocol.LayoutMode) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.setLayoutModeLocked(mode)
}

// ToggleLayout flips between the card fan and the scrolling strip.
// Nothing is sent: other clients keep their own mode, and this one is
// forgotten on detach. It animates like any other change of geometry.
func (c *Client) ToggleLayout() {
	c.mu.Lock()
	defer c.mu.Unlock()
	next := protocol.LayoutCards
	if c.layoutMode == protocol.LayoutCards {
		next = protocol.LayoutScroll
	}
	// Same two "previous" values HandleServerMsg keeps, for the same
	// reasons: prevTarget decides whether anything moved, prevOnScreen
	// is where the animation starts.
	prevTarget := c.placements
	prevOnScreen := c.currentPlacementsLocked()
	c.setLayoutModeLocked(next)
	if c.sel != nil && !c.selectionStillPlacedLocked() {
		c.sel = nil
	}
	if c.focusPaneID != 0 && !placementsEqual(prevTarget, c.placements) {
		c.motion = &motion{from: prevOnScreen, to: c.placements, total: motionFrames}
	}
}

// setLayoutModeLocked installs mode and recomputes placements from the
// strip as it stands. With no columns yet ComputePlacements returns
// nil, which is right before the first snapshot. c.mu must be held.
func (c *Client) setLayoutModeLocked(mode protocol.LayoutMode) {
	c.layoutMode = mode
	layout.ApplyMode(c.strip, mode)
	c.placements = layout.ToProtocol(c.strip.ComputePlacements(c.cols, c.rows))
}

// runeLen counts cells the way compose.WriteString consumes them: one
// per rune.
//
// This is the right unit only because WriteString advances one cell per
// rune regardless of the glyph. It is not the true display width -- a
// double-width glyph is one rune and two columns, and WriteString gets
// that wrong too. The two agree by sharing a bug, so if WriteString is
// ever widened to grapheme clusters with measured widths, this has to
// move to Cell.Width in the same change.
func runeLen(s string) int { return utf8.RuneCountInString(s) }

// truncateRunes cuts s to at most n cells on a rune boundary. Slicing by
// byte index could split a multibyte glyph and put a partial UTF-8
// sequence on the wire.
func truncateRunes(s string, n int) string {
	if n < 0 {
		return ""
	}
	if runeLen(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

// SendVerb forwards a layout action request to the server.
func (c *Client) SendVerb(ctx context.Context, v protocol.VerbType) {
	c.transport.SendClient(ctx, protocol.MsgVerb{Verb: v})
}

// FocusColumn focuses the n'th column from the left, or the rightmost
// for keys.LastColumn. It resolves against this client's strip and sends
// the same MsgFocusPane a click sends, so the server's guard against a
// pane that closed in the meantime covers it too. Out of range is a
// no-op.
func (c *Client) FocusColumn(ctx context.Context, n int) {
	c.mu.Lock()
	ids := c.strip.PaneIDs()
	c.mu.Unlock()

	i := n - 1
	if n == keys.LastColumn {
		i = len(ids) - 1
	}
	if i < 0 || i >= len(ids) {
		return
	}
	c.transport.SendClient(ctx, protocol.MsgFocusPane{PaneID: ids[i]})
}

// SendKey forwards a decoded key event for the focused pane to the server.
func (c *Client) SendKey(ctx context.Context, k uv.KeyEvent) {
	c.mu.Lock()
	focusedID := c.focusPaneID
	c.mu.Unlock()

	if focusedID > 0 {
		c.transport.SendClient(ctx, protocol.MsgInput{PaneID: focusedID, Key: protocol.EncodeKey(k)})
	}
}

// SendInput forwards raw input bytes (e.g. paste) for the focused pane to the server.
func (c *Client) SendInput(ctx context.Context, data []byte) {
	c.mu.Lock()
	focusedID := c.focusPaneID
	c.mu.Unlock()

	if focusedID > 0 {
		c.transport.SendClient(ctx, protocol.MsgInput{PaneID: focusedID, Data: data})
	}
}

// SendScroll requests a scrollback offset delta for the focused pane.
func (c *Client) SendScroll(ctx context.Context, delta int) {
	c.mu.Lock()
	focusedID := c.focusPaneID
	c.mu.Unlock()

	if focusedID > 0 {
		c.transport.SendClient(ctx, protocol.MsgScroll{PaneID: focusedID, Delta: delta})
	}
}

// SendResize notifies the server of host window geometry changes.
func (c *Client) SendResize(ctx context.Context, cols, rows int) {
	c.mu.Lock()
	c.cols = cols
	c.rows = rows
	// A resize invalidates an animation in flight: its rects are in
	// the old viewport's coordinates, so continuing would interpolate
	// toward a layout that no longer exists. Snap instead.
	c.motion = nil
	if c.strip != nil && c.strip.ColCount() > 0 {
		c.placements = layout.ToProtocol(c.strip.ComputePlacements(c.cols, c.rows))
	}
	c.mu.Unlock()

	c.transport.SendClient(ctx, protocol.MsgResize{Cols: cols, Rows: rows})
}

// FocusedPaneID returns current focused pane ID.
func (c *Client) FocusedPaneID() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.focusPaneID
}
