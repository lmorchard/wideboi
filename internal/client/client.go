// Package client owns the host terminal rendering, composition, and input routing.
package client

import (
	"context"
	"image"
	"log/slog"
	"sync"
	"time"
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

// Client manages screen rendering, off-screen mirrors, and input forwarding.
type Client struct {
	mu                 sync.Mutex
	transport          transport.Transport
	cols               int
	rows               int
	strip              *layout.Strip
	ptyWidths          map[int]int
	displayWidths      map[int]int
	panX               map[int]int
	pendingReveal      map[int]bool
	followPTY          bool
	panStep            int
	placements         []protocol.PlacementData
	focusPaneID        int
	pendingFocusPaneID int
	paneStatuses       map[int]protocol.PaneStatus
	paneTitles         map[int]string
	mirrors            map[int]*PaneMirror
	paneUpdates        map[int]protocol.MsgPaneUpdate
	cursorInfos        map[int]cursorPos
	paneMetadata       map[int]protocol.MsgPaneMetadata
	prefixLabel        string
	controlMode        bool
	helpVisible        bool
	search             *searchState
	detachable         bool
	bindings           []keys.Binding
	motion             *motion
	sel                *selection
	theme              Theme
	// mouseTracking is which panes' children have asked for mouse
	// events, from MsgPaneUpdate. grab is a drag being forwarded to one.
	mouseTracking map[int]bool
	grab          *mouseGrab

	stagingScreen      *offscreenHostScreen
	lastRenderedScreen *offscreenHostScreen
	lastHostScreen     HostScreen
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
		transport:     tp,
		cols:          cols,
		rows:          rows,
		strip:         layout.NewStrip(),
		ptyWidths:     make(map[int]int),
		displayWidths: make(map[int]int),
		panX:          make(map[int]int),
		pendingReveal: make(map[int]bool),
		panStep:       10,
		prefixLabel:   prefixLabel,
		mirrors:       make(map[int]*PaneMirror),
		paneUpdates:   make(map[int]protocol.MsgPaneUpdate),
		cursorInfos:   make(map[int]cursorPos),
		paneMetadata:  make(map[int]protocol.MsgPaneMetadata),
		theme:         DefaultTheme(),
	}
}

// SetTheme configures the client's visual theme.
func (c *Client) SetTheme(t Theme) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.theme = t
}

func (c *Client) SetPanStep(step int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if step > 0 {
		c.panStep = step
	}
}

func (c *Client) SetWidthPresets(presets []int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.strip.SetWidthPresets(presets)
}

func (c *Client) PanFocused(delta int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.focusPaneID
	if id == 0 {
		return
	}
	old := c.panX[id]
	c.panX[id] += delta * c.panStep
	c.clampPanLocked(id)
	if old != c.panX[id] {
		c.updatePlacementsLocked()
	}
}

func (c *Client) ToggleFollowPTY() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.followPTY = !c.followPTY
	if !c.followPTY {
		return
	}
	for id, width := range c.ptyWidths {
		c.displayWidths[id] = width
		c.strip.SetColumnWidth(id, width)
		c.clampPanLocked(id)
	}
	c.updatePlacementsLocked()
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
	var toSend []transport.ClientMessage
	c.mu.Lock()
	defer func() {
		c.mu.Unlock()
		for _, m := range toSend {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			c.transport.SendClient(ctx, m)
			cancel()
		}
	}()

	switch m := msg.(type) {
	case protocol.MsgPaneCreated:
		c.pendingFocusPaneID = m.PaneID
	case protocol.MsgLayoutSnapshot:
		slog.Debug("received MsgLayoutSnapshot", "cols", len(m.Columns), "focusPaneID", c.focusPaneID)
		c.applySnapshotLocked(m)

	case protocol.MsgPaneUpdate:
		c.applyPaneUpdateLocked(m)

	case protocol.MsgPanePatch:
		base, ok := c.paneUpdates[m.PaneID]
		if ok {
			if next, valid := protocol.ApplyPanePatch(base, m); valid {
				c.applyPaneUpdateLocked(next)
				break
			}
		}
		delete(c.paneUpdates, m.PaneID)
		// A baseline mismatch means at least one patch was lost or a
		// layout replaced the mirror. Ask the server for a full snapshot.
		toSend = append(toSend, protocol.MsgPaneResync{PaneID: m.PaneID})

	case protocol.MsgPaneMetadata:
		if c.paneMetadata == nil {
			c.paneMetadata = make(map[int]protocol.MsgPaneMetadata)
		}
		c.paneMetadata[m.PaneID] = m
	case protocol.MsgFocusPane:
		c.strip.FocusPaneID(m.PaneID)
		c.focusPaneID = c.strip.FocusedPaneID()
		c.updatePlacementsLocked()
	case protocol.MsgHistorySnapshot:
		if scroll := c.applyHistoryLocked(m); scroll != nil {
			toSend = append(toSend, *scroll)
		}
	}
}

func (c *Client) applyPaneUpdateLocked(m protocol.MsgPaneUpdate) {
	// Trace, not Debug: busy panes still send one of these per frame.
	slog.Log(context.Background(), logger.LevelTrace, "received MsgPaneUpdate", "paneID", m.PaneID, "cols", m.Cols, "rows", m.Rows)
	mirror := c.ensureMirrorLocked(m.PaneID, m.Cols, m.Rows)
	c.paneUpdates[m.PaneID] = m
	for y, line := range m.Lines {
		for x, cell := range line {
			// LineData has one entry per terminal column. SetCell
			// writes a wide glyph's continuation itself; the wire
			// placeholder must not overwrite it.
			if x > 0 && line[x-1].Width > 1 {
				continue
			}
			uvCell := uv.NewCell(mirror.Surface.WidthMethod(), cell.Content)
			uvCell.Style = cell.Style.Decode()
			mirror.Surface.SetCell(x, y, uvCell)
		}
	}
	if c.cursorInfos == nil {
		c.cursorInfos = make(map[int]cursorPos)
	}
	c.cursorInfos[m.PaneID] = cursorPos{
		pt:      image.Pt(m.CursorX, m.CursorY),
		visible: m.CursorVisible,
	}
	if c.pendingReveal[m.PaneID] {
		delete(c.pendingReveal, m.PaneID)
		c.revealCursorLocked(m.PaneID)
	}
	if c.mouseTracking == nil {
		c.mouseTracking = make(map[int]bool)
	}
	c.mouseTracking[m.PaneID] = m.MouseTracking
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

// SetDetachable declares whether this client can leave its session

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

// updatePlacementsLocked recalculates placements after a local geometry/focus/mode change,
// arming motion if they moved.
func (c *Client) updatePlacementsLocked() {
	prevTarget := c.placements
	prevOnScreen := c.currentPlacementsLocked()

	c.placements = c.computePlacementsLocked()

	if c.focusPaneID != 0 && !placementsEqual(prevTarget, c.placements) {
		c.motion = &motion{from: prevOnScreen, to: c.placements, total: motionFrames}
	}

	if c.sel != nil && !c.selectionStillPlacedLocked() {
		c.sel = nil
	}
}

// LayoutMode returns the client's current layout mode.
func (c *Client) LayoutMode() protocol.LayoutMode {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.layoutModeLocked()
}

func (c *Client) layoutModeLocked() protocol.LayoutMode {
	if c.strip == nil {
		return protocol.LayoutScroll
	}
	if _, ok := c.strip.Strategy().(layout.CardStrategy); ok {
		return protocol.LayoutCards
	}
	return protocol.LayoutScroll
}

// ToggleLayout flips between the card fan and the scrolling strip.
// Nothing is sent: other clients keep their own mode, and this one is
// forgotten on detach. It animates like any other change of geometry.
func (c *Client) ToggleLayout() {
	c.mu.Lock()
	defer c.mu.Unlock()
	next := protocol.LayoutCards
	if c.layoutModeLocked() == protocol.LayoutCards {
		next = protocol.LayoutScroll
	}
	layout.ApplyMode(c.strip, next)
	c.updatePlacementsLocked()
}

// setLayoutModeLocked installs mode and recomputes placements from the
// strip as it stands. With no columns yet ComputePlacements returns
// nil, which is right before the first snapshot. c.mu must be held.
func (c *Client) setLayoutModeLocked(mode protocol.LayoutMode) {
	layout.ApplyMode(c.strip, mode)
	c.placements = c.computePlacementsLocked()
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

// SendVerb forwards a layout action request to the server, appending the currently focused pane.
func (c *Client) SendVerb(ctx context.Context, v protocol.VerbType) {
	var msg transport.ClientMessage

	c.mu.Lock()
	switch v {
	case protocol.VerbFocusLeft:
		c.strip.FocusLeft()
		c.focusPaneID = c.strip.FocusedPaneID()
		c.updatePlacementsLocked()
	case protocol.VerbFocusRight:
		c.strip.FocusRight()
		c.focusPaneID = c.strip.FocusedPaneID()
		c.updatePlacementsLocked()
	case protocol.VerbFocusLast:
		c.strip.FocusLast()
		c.focusPaneID = c.strip.FocusedPaneID()
		c.updatePlacementsLocked()
	case protocol.VerbSmartJump:
		if id := c.smartJumpTargetLocked(); id > 0 {
			c.strip.FocusPaneID(id)
			c.focusPaneID = c.strip.FocusedPaneID()
			c.updatePlacementsLocked()
		}
	case protocol.VerbCycleWidth, protocol.VerbGrowWidth, protocol.VerbShrinkWidth:
		id := c.focusPaneID
		if id == 0 {
			break
		}
		c.followPTY = false
		switch v {
		case protocol.VerbCycleWidth:
			c.strip.CycleWidth(id)
		case protocol.VerbGrowWidth:
			c.strip.GrowWidth(id, 10)
		case protocol.VerbShrinkWidth:
			c.strip.ShrinkWidth(id, 10)
		}
		for _, col := range c.strip.Columns() {
			if col.PaneID == id {
				c.displayWidths[id] = col.Width
				break
			}
		}
		c.clampPanLocked(id)
		c.updatePlacementsLocked()
		msg = protocol.MsgSetPaneWidth{PaneID: id, Width: c.displayWidths[id]}
	case protocol.VerbClaimSize:
		widths := make(map[int]int, len(c.displayWidths))
		for id, width := range c.displayWidths {
			widths[id] = width
		}
		msg = protocol.MsgVerb{Verb: v, PaneID: c.focusPaneID, Widths: widths}
	default:
		focused := c.focusPaneID
		msg = protocol.MsgVerb{Verb: v, PaneID: focused}
	}
	c.mu.Unlock()

	if msg != nil {
		c.transport.SendClient(ctx, msg)
	}
}

func (c *Client) smartJumpTargetLocked() int {
	rank := func(st protocol.PaneStatus) int {
		switch st {
		case protocol.StatusFailed:
			return 3
		case protocol.StatusDone:
			return 2
		case protocol.StatusNeedsInput:
			return 1
		default:
			return 0
		}
	}

	bestID, bestRank := 0, 0
	for _, p := range c.strip.Columns() {
		id := p.PaneID
		r := rank(c.paneStatuses[id])
		switch {
		case r == 0:
		case r > bestRank:
			bestID, bestRank = id, r
		case r == bestRank && (bestID == 0 || id < bestID):
			bestID, bestRank = id, r
		}
	}
	return bestID
}

// FocusColumn focuses the n'th column from the left, or the rightmost
// for keys.LastColumn.
func (c *Client) FocusColumn(ctx context.Context, n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ids := c.strip.PaneIDs()

	i := n - 1
	if n == keys.LastColumn {
		i = len(ids) - 1
	}
	if i >= 0 && i < len(ids) {
		c.strip.FocusPaneID(ids[i])
		c.focusPaneID = ids[i]
		c.updatePlacementsLocked()
	}
}

// SendKey forwards a decoded key event for the focused pane to the server.
func (c *Client) SendKey(ctx context.Context, k uv.KeyEvent) {
	c.mu.Lock()
	focusedID := c.focusPaneID
	c.pendingReveal[focusedID] = true
	c.revealCursorLocked(focusedID)
	c.mu.Unlock()

	if focusedID > 0 {
		c.transport.SendClient(ctx, protocol.MsgInput{PaneID: focusedID, Key: protocol.EncodeKey(k)})
	}
}

// SendInput forwards raw input bytes (e.g. paste) for the focused pane to the server.
func (c *Client) SendInput(ctx context.Context, data []byte) {
	c.mu.Lock()
	focusedID := c.focusPaneID
	c.pendingReveal[focusedID] = true
	c.revealCursorLocked(focusedID)
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
		c.placements = c.computePlacementsLocked()
	}
	c.mu.Unlock()

	c.transport.SendClient(ctx, protocol.MsgResize{Cols: cols, Rows: rows})
}

// SendSplit requests the server to spawn a new pane.
func (c *Client) SendSplit(ctx context.Context, cmd, cwd string, afterPaneID int, keep bool) {
	c.transport.SendClient(ctx, protocol.MsgSplitRequest{
		Command:     cmd,
		Cwd:         cwd,
		AfterPaneID: afterPaneID,
		Keep:        keep,
	})
}

// FocusedPaneID returns current focused pane ID.
func (c *Client) FocusedPaneID() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.focusPaneID
}

// PaneMetadata returns the metadata for a pane if known.
func (c *Client) PaneMetadata(paneID int) (protocol.MsgPaneMetadata, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	meta, ok := c.paneMetadata[paneID]
	return meta, ok
}
