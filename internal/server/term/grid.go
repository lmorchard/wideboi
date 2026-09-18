// Package term wraps the VT emulator behind an interface wideboi owns.
//
// charmbracelet/x/vt has no tagged release. This interface is the seam
// that keeps an upstream break confined to one file, and the reason a
// different emulator could be substituted without touching callers.
package term

import (
	"image"
	"io"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
)

// Grid is one pane's terminal state.
//
// Write takes PTY output in. Read gives out encoded keystrokes and
// terminal replies, destined for the PTY. Routing input through the
// emulator keeps it the single authority on pane state, and is the only
// way to turn a decoded uv.KeyEvent back into the bytes a child expects.
//
// Close releases the emulator's own resources. Grid.Read blocks on an
// internal pipe with no other way to unblock it, so a caller that owns a
// Grid must call Close to let a pending Read return.
type Grid interface {
	io.Writer
	io.Reader
	SendKey(k uv.KeyEvent)
	Resize(cols, rows int)
	Draw(dst uv.Screen, area image.Rectangle)
	Size() (cols, rows int)
	Close() error
}

// vtGrid adapts x/vt's SafeEmulator to Grid. SafeEmulator rather than
// Emulator because a pane's PTY reader goroutine writes to it while the
// compositor reads from it.
type vtGrid struct {
	em *vt.SafeEmulator
}

// NewVT returns a Grid backed by charmbracelet/x/vt.
func NewVT(cols, rows int) Grid {
	return &vtGrid{em: vt.NewSafeEmulator(cols, rows)}
}

func (g *vtGrid) Write(p []byte) (int, error) { return g.em.Write(p) }
func (g *vtGrid) Read(p []byte) (int, error)  { return g.em.Read(p) }
func (g *vtGrid) SendKey(k uv.KeyEvent)       { g.em.SendKey(k) }
func (g *vtGrid) Resize(cols, rows int)       { g.em.Resize(cols, rows) }
func (g *vtGrid) Size() (int, int)            { return g.em.Width(), g.em.Height() }

func (g *vtGrid) Draw(dst uv.Screen, area image.Rectangle) {
	g.em.Draw(dst, area)
}

// Close closes the underlying emulator, which unblocks any goroutine
// parked in Read: SafeEmulator embeds *vt.Emulator, whose Close calls
// CloseWithError(io.EOF) on the pipe writer Read's pipe reader drains
// from, so the pending Read returns (0, io.EOF) rather than blocking
// forever.
func (g *vtGrid) Close() error { return g.em.Close() }
