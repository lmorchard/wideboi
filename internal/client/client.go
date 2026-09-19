// Package client owns the host terminal rendering, composition, and input routing.
package client

import (
	"context"
	"fmt"
	"image"
	"sync"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/client/compose"
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
	transport    *transport.InProcChannel
	cols         int
	rows         int
	placements   []protocol.PlacementData
	focusPaneID  int
	paneStatuses map[int]string
	mirrors      map[int]*PaneMirror
}

// NewClient initializes a Client instance.
func NewClient(tp *transport.InProcChannel, cols, rows int) *Client {
	return &Client{
		transport: tp,
		cols:      cols,
		rows:      rows,
		mirrors:   make(map[int]*PaneMirror),
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
		c.placements = m.Placements
		c.focusPaneID = m.FocusPaneID
		c.paneStatuses = m.PaneStatuses

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

	var focusedPlacement *protocol.PlacementData

	for i := range c.placements {
		p := &c.placements[i]
		if p.PaneID == c.focusPaneID {
			focusedPlacement = p
		}
		if drawPane != nil {
			drawPane(p.PaneID, scr, p.Dst)
		}

		// Draw column divider on right edge if applicable
		if p.Dst.Max.X < c.cols {
			for y := p.Dst.Min.Y; y < p.Dst.Max.Y; y++ {
				compose.WriteString(scr, p.Dst.Max.X, y, "│")
			}
		}
	}

	// Status bar on bottom row
	status := fmt.Sprintf("focus: pane %d", c.focusPaneID)
	for _, p := range c.placements {
		if glyph, ok := c.paneStatuses[p.PaneID]; ok && glyph != "" && glyph != " " {
			status += fmt.Sprintf("  [%d %s]", p.PaneID, glyph)
		}
	}
	// Leave the final column untouched: ultraviolet's terminal renderer
	// writes the last cell of a row with autowrap toggled off and back on
	// around it, which splits whatever glyph lands there across a mode
	// escape sequence on the wire. Budgeting one cell short of c.cols
	// keeps the whole help string contiguous in the raw output.
	status += helpFor(c.cols-1, runeLen(status))
	status = truncateRunes(status, c.cols-1)
	compose.WriteString(scr, 0, c.rows-1, status)

	// Host cursor position and visibility
	if focusedPlacement != nil && cursorInfo != nil {
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
	} else {
		scr.HideCursor()
	}
}

// Key help. These strings must match cmd/wideboi's binding matrix;
// scripts/smoke.py asserts they do, so the two cannot drift apart
// silently. "alt+" rather than a glyph because it has to be legible in a
// terminal that may not render one, and because it is what a user would
// type into their terminal's own key configuration.
//
// Split into segments because the whole line does not fit an 80-column
// terminal -- the commonest width there is, and cmd/wideboi's own
// fallback when the host reports no size. Spelled out in full it is 91
// cells against a budget of 79 minus the "focus: pane N" prefix, so a
// single truncation lopped off the one verb a stuck user most needs.
// Segments are listed in display order, most essential first, and the
// tail is dropped as the budget shrinks; helpQuit is never a candidate
// and is accounted for before any segment is admitted. At 80 columns
// that leaves focus/new/width/kill/quit; jump returns at 89 columns and
// scroll at 105. A pane status glyph pushes those thresholds up by the
// width of its "  [N x]" segment.
var (
	helpSegments = []string{
		"  alt+h/l focus",
		"  alt+n new",
		"  alt+w width",
		"  alt+x kill",
		"  alt+j jump",
		"  alt+u/d scroll",
	}
	helpQuit = "  alt+q quit"
)

// helpFor returns as much of the key help as fits in budget cells given
// that used cells are already spoken for, always including the quit
// verb. A caller with no room even for that gets it anyway and truncates
// -- there is nothing better to show, and every wider terminal keeps it.
func helpFor(budget, used int) string {
	used += runeLen(helpQuit)
	var help string
	for _, seg := range helpSegments {
		n := runeLen(seg)
		if used+n > budget {
			break
		}
		used += n
		help += seg
	}
	return help + helpQuit
}

// runeLen counts cells the way compose.WriteString consumes them: one
// per rune. Byte length is the wrong unit -- the status glyphs (»,  ✓,
// ✗) are three bytes each and one cell each, so a byte-based budget
// silently ate three cells of help per glyph.
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
	c.mu.Unlock()

	c.transport.SendClient(ctx, protocol.MsgResize{Cols: cols, Rows: rows})
}

// FocusPaneID returns current focused pane ID.
func (c *Client) FocusPaneID() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.focusPaneID
}
