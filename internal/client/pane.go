// Package client owns the host terminal: composition, rendering, input.
package client

import (
	"io"
	"sync/atomic"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/client/compose"
	"github.com/lmorchard/wideboi/internal/server/ptyx"
	"github.com/lmorchard/wideboi/internal/server/term"
)

// Pane couples a PTY-backed process to an emulator and a draw surface.
//
// This type is temporary: Plan 2 splits it across the client/server seam,
// with the PTY and emulator server-side and the surface client-side.
type Pane struct {
	pty     *ptyx.Pane
	grid    term.Grid
	surface compose.Surface
	dirty   atomic.Bool
	cols    int
	rows    int
}

// NewPane spawns argv on a PTY sized cols x rows.
func NewPane(argv []string, cols, rows int, dir string) (*Pane, error) {
	p, err := ptyx.Spawn(argv, cols, rows, dir)
	if err != nil {
		return nil, err
	}
	return &Pane{
		pty:     p,
		grid:    term.NewVT(cols, rows),
		surface: compose.NewSurface(cols, rows),
		cols:    cols,
		rows:    rows,
	}, nil
}

// Start begins pumping bytes in both directions. Two goroutines are
// required, not one:
//
//	PTY  -> emulator : the child's output becomes cell state
//	emulator -> PTY  : encoded keystrokes and terminal replies
//
// The second is mandatory because Grid.SendKey writes to an io.Pipe and
// blocks until something reads. Without this drain, the first keypress
// deadlocks the process.
//
// Each goroutine recovers, so a parser panic in one pane cannot take
// the multiplexer down.
func (p *Pane) Start(onExit func()) {
	// PTY output -> emulator.
	go func() {
		defer func() {
			_ = recover() // a broken pane must not kill the multiplexer
			onExit()
		}()
		buf := make([]byte, 4096)
		for {
			n, err := p.pty.Master.Read(buf)
			if n > 0 {
				_, _ = p.grid.Write(buf[:n])
				p.dirty.Store(true)
			}
			if err != nil {
				return
			}
		}
	}()

	// Emulator output (keys, replies) -> PTY.
	go func() {
		defer func() { _ = recover() }()
		buf := make([]byte, 4096)
		for {
			n, err := p.grid.Read(buf)
			if n > 0 {
				if _, werr := p.pty.Master.Write(buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
}

// SendKey forwards a decoded key event to the pane's child, encoded as
// the bytes a terminal application expects.
func (p *Pane) SendKey(k uv.KeyEvent) { p.grid.SendKey(k) }

// Surface redraws the pane's cells if anything changed, and returns them.
func (p *Pane) Surface() compose.Surface {
	if p.dirty.Swap(false) {
		p.grid.Draw(p.surface, p.surface.Bounds())
	}
	return p.surface
}

// Write forwards raw bytes to the pane's child process, bypassing the
// emulator. Use this for pasted text, not for keystrokes.
func (p *Pane) Write(b []byte) (int, error) { return p.pty.Master.Write(b) }

// Size reports the pane's logical size.
func (p *Pane) Size() (cols, rows int) { return p.cols, p.rows }

// Close tears down the pane's entire process tree.
func (p *Pane) Close() error { return p.pty.Kill(2 * time.Second) }

var _ io.Writer = (*Pane)(nil)
