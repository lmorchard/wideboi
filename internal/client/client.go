// Package client owns the host terminal rendering, composition, and input routing.
package client

import (
	"context"
	"fmt"
	"image"
	"strings"
	"sync"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/client/compose"
	"github.com/lmorchard/wideboi/internal/layout"
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
	mirrors      map[int]*PaneMirror
	prefixLabel  string
	controlMode  bool
	activeWipe   *WipeTransition
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
	}
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
		oldFocus := c.focusPaneID
		if len(m.Columns) > 0 {
			c.strip.SyncColumns(m.Columns, m.FocusPaneID)
			c.placements = layout.ToProtocol(c.strip.ComputePlacements(c.cols, c.rows))
		} else {
			c.placements = m.Placements
		}
		c.focusPaneID = m.FocusPaneID
		c.paneStatuses = m.PaneStatuses

		if oldFocus != 0 && c.focusPaneID != oldFocus {
			dir := WipeLeftToRight
			if c.focusPaneID < oldFocus {
				dir = WipeRightToLeft
			}
			fA := compose.NewSurface(c.cols, c.rows)
			fB := compose.NewSurface(c.cols, c.rows)
			c.activeWipe = NewWipeTransition(fA, fB, c.cols, c.rows, dir, 8)
		}

		activeIDs := make(map[int]bool)
		for _, p := range c.placements {
			activeIDs[p.PaneID] = true
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

		// Prune inactive mirrors
		for id := range c.mirrors {
			if !activeIDs[id] {
				delete(c.mirrors, id)
			}
		}
	}
}

// Draw composites active pane surfaces, dividers, host cursor, and status bar onto host screen scr.
func (c *Client) Draw(scr *uv.TerminalScreen, drawPane func(id int, dst uv.Screen, area image.Rectangle), cursorInfo func(id int) (image.Point, bool)) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.activeWipe != nil && c.activeWipe.Active() {
		c.activeWipe.Draw(scr)
		scr.HideCursor()
		if c.activeWipe.Step() {
			c.activeWipe = nil
		}
		return
	}

	var focusedPlacement *protocol.PlacementData

	for i := range c.placements {
		p := &c.placements[i]
		if p.PaneID == c.focusPaneID {
			focusedPlacement = p
		}

		// Draw 1-row pane header bar at Y = 0
		headerW := p.Dst.Dx()
		if headerW > 0 {
			glyph := c.paneStatuses[p.PaneID]
			var header string
			if glyph != "" && glyph != " " {
				header = fmt.Sprintf(" [%d] %s", p.PaneID, glyph)
			} else {
				header = fmt.Sprintf(" [%d]", p.PaneID)
			}
			if p.PaneID == c.focusPaneID {
				header += " ★"
			}
			if runeLen(header) < headerW {
				header += strings.Repeat(" ", headerW-runeLen(header))
			}
			header = truncateRunes(header, headerW)

			if p.PaneID == c.focusPaneID {
				compose.WriteStyled(scr, p.Dst.Min.X, 0, header, uv.Style{Attrs: uv.AttrReverse})
			} else {
				compose.WriteString(scr, p.Dst.Min.X, 0, header)
			}
		}

		if drawPane != nil {
			drawPane(p.PaneID, scr, p.Dst)
		}

		// Draw column divider on right edge if applicable.
		// Bold ┃ if adjacent to focused pane, otherwise │.
		if p.Dst.Max.X < c.cols {
			divider := "│"
			if p.PaneID == c.focusPaneID || (i+1 < len(c.placements) && c.placements[i+1].PaneID == c.focusPaneID) {
				divider = "┃"
			}
			for y := p.Dst.Min.Y; y < p.Dst.Max.Y; y++ {
				compose.WriteString(scr, p.Dst.Max.X, y, divider)
			}
		}
	}

	// Status bar on bottom row.
	//
	// Leave the final column untouched: ultraviolet's terminal renderer
	// writes the last cell of a row with autowrap toggled off and back
	// on around it, which splits whatever glyph lands there across a
	// mode escape sequence on the wire. Budgeting one cell short of
	// c.cols keeps the whole line contiguous in the raw output.
	statusText, statusStyle := c.statusLineLocked(c.cols - 1)
	compose.WriteStyled(scr, 0, c.rows-1, statusText, statusStyle)

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
	case focusedPlacement != nil && cursorInfo != nil:
		cp, visible := cursorInfo(c.focusPaneID)
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

// Control-mode verbs, in display order, most essential first. These
// strings must match cmd/wideboi's router table; scripts/smoke.py
// asserts they do, so the two cannot drift apart silently.
//
// Unprefixed letters rather than a modifier because that is the whole
// point of the mode: the terminal has already told us a prefix arrived,
// so no modifier needs to survive the trip.
var controlVerbs = []string{
	"h/l focus",
	"n new",
	"w width",
	"x kill",
	"j jump",
	"u scroll",
	"d detach",
}

// Never dropped. With no unprefixed bindings left, a user who cannot
// read these two out of the bar has no way forward except a signal.
var controlTail = []string{"q quit", "esc exit"}

// controlHelp returns as much of the control-mode verb menu as fits in
// budget cells, always including controlTail.
//
// The full menu is 71 cells, against a budget of 79 at an 80-column
// terminal, so in practice nothing is dropped at any width wideboi is
// usable at. The dropping exists for narrower terminals and for
// whatever verbs get added later.
func controlHelp(budget int) string {
	tail := strings.Join(controlTail, "  ")
	used := runeLen(tail)
	taken := make([]string, 0, len(controlVerbs))
	for _, v := range controlVerbs {
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
		menu := truncateRunes(controlHelp(budget), budget)
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
	// The hint is right-aligned and is the first thing to go when the
	// terminal is too narrow for it: the pane statuses are live
	// information, the hint is a fixed string a user learns once.
	hint := c.prefixLabel + " for commands"
	if pad := budget - runeLen(status) - runeLen(hint); pad >= 2 {
		status += strings.Repeat(" ", pad) + hint
	}
	return truncateRunes(status, budget)
}

// SetControlMode switches the client between forwarding keys to the
// focused pane and showing the verb menu. Called by cmd/wideboi after
// every key event, so the bar can never disagree with the router.
func (c *Client) SetControlMode(on bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.controlMode = on
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

// SendKey forwards a decoded key event for the focused pane to the server.
func (c *Client) SendKey(ctx context.Context, k uv.KeyEvent) {
	c.mu.Lock()
	focusedID := c.focusPaneID
	c.mu.Unlock()

	if focusedID > 0 {
		c.transport.SendClient(ctx, protocol.MsgInput{PaneID: focusedID, Key: k})
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
	if c.strip != nil && c.strip.ColCount() > 0 {
		c.placements = layout.ToProtocol(c.strip.ComputePlacements(c.cols, c.rows))
	}
	c.mu.Unlock()

	c.transport.SendClient(ctx, protocol.MsgResize{Cols: cols, Rows: rows})
}

// FocusPaneID returns current focused pane ID.
func (c *Client) FocusPaneID() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.focusPaneID
}
