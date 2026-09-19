// Package client owns the host terminal rendering, composition, and input routing.
package client

import (
	"context"
	"fmt"
	"sync"

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
	mu          sync.Mutex
	transport   *transport.InProcChannel
	cols        int
	rows        int
	placements  []protocol.PlacementData
	focusPaneID int
	mirrors     map[int]*PaneMirror
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

// Draw composites active pane surfaces, dividers, and status bar onto host screen scr.
func (c *Client) Draw(scr uv.Screen) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, p := range c.placements {
		m, ok := c.mirrors[p.PaneID]
		if !ok || m == nil {
			continue
		}
		// Blit pane surface onto host screen destination rect
		compose.Blit(scr, m.Surface, p.Dst)

		// Draw column divider on right edge if applicable
		if p.Dst.Max.X < c.cols {
			for y := p.Dst.Min.Y; y < p.Dst.Max.Y; y++ {
				compose.WriteString(scr, p.Dst.Max.X, y, "│")
			}
		}
	}

	// Status bar on bottom row
	status := fmt.Sprintf(" focus: pane %d   ctrl+o switch   ctrl+n new col   ctrl+w cycle width   ctrl+q quit ", c.focusPaneID)
	compose.WriteString(scr, 0, c.rows-1, status)
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
