// Package server manages multiplexer state, PTY sessions, and layout calculation.
package server

import (
	"errors"
	"fmt"
	"image"
	"io"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/server/ptyx"
	"github.com/lmorchard/wideboi/internal/server/term"
)

const CloseGrace = 2 * time.Second
const CloseResidual = ptyx.KillResidual
const keyQueueDepth = 256

// Pane is the server-side owner of a PTY-backed process and VT emulator.
type Pane struct {
	id   int
	pty  *ptyx.Pane
	grid term.Grid
	cols int
	rows int

	keys    chan uv.KeyEvent
	dropped atomic.Uint64

	closed    chan struct{}
	closeOnce sync.Once

	dead atomic.Bool

	failMu   sync.Mutex
	failures []error
}

// NewPane spawns argv on a PTY sized cols x rows for the given pane id.
func NewPane(id int, argv []string, cols, rows int, dir string) (*Pane, error) {
	p, err := ptyx.Spawn(argv, cols, rows, dir)
	if err != nil {
		return nil, err
	}
	return &Pane{
		id:     id,
		pty:    p,
		grid:   term.NewVT(cols, rows),
		cols:   cols,
		rows:   rows,
		keys:   make(chan uv.KeyEvent, keyQueueDepth),
		closed: make(chan struct{}),
	}, nil
}

// ID returns the pane's unique identifier.
func (p *Pane) ID() int { return p.id }

// Start begins pumping bytes between the child and the emulator.
func (p *Pane) Start(onExit func()) {
	// PTY output -> emulator.
	go func() {
		defer func() {
			if r := recover(); r != nil {
				p.panicked("pty-reader", r)
				return
			}
			onExit()
		}()
		buf := make([]byte, 4096)
		for {
			n, err := p.pty.Master.Read(buf)
			if n > 0 {
				_, _ = p.grid.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	// Emulator output (keys, replies) -> PTY.
	go func() {
		defer func() {
			if r := recover(); r != nil {
				p.panicked("pty-writer", r)
			}
		}()
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

	// Queued key events -> emulator.
	go func() {
		defer func() {
			if r := recover(); r != nil {
				p.panicked("key-writer", r)
			}
		}()
		for {
			select {
			case k := <-p.keys:
				p.grid.SendKey(k)
			case <-p.closed:
				return
			}
		}
	}()
}

// SendKey queues a decoded key event for the pane's child.
func (p *Pane) SendKey(k uv.KeyEvent) {
	select {
	case p.keys <- k:
	default:
		p.dropped.Add(1)
	}
}

// SendText forwards text directly to the emulator.
func (p *Pane) SendText(text string) {
	p.grid.SendText(text)
}

// Dead reports whether a pump goroutine panicked.
func (p *Pane) Dead() bool { return p.dead.Load() }

// Draw renders the pane's current cell state onto dst inside area.
func (p *Pane) Draw(dst uv.Screen, area image.Rectangle) {
	p.grid.Draw(dst, area)
}

// Resize resizes the emulator's dimensions.
func (p *Pane) Resize(cols, rows int) {
	p.cols, p.rows = cols, rows
	p.grid.Resize(cols, rows)
}

// Write forwards raw bytes to the child process.
func (p *Pane) Write(b []byte) (int, error) { return p.pty.Master.Write(b) }

// Size reports logical dimensions.
func (p *Pane) Size() (cols, rows int) { return p.cols, p.rows }

// CursorPosition reports the cell coordinates of the cursor relative to origin.
func (p *Pane) CursorPosition() image.Point { return p.grid.CursorPosition() }

// CursorVisible reports whether DECTCEM cursor visibility is enabled.
func (p *Pane) CursorVisible() bool { return p.grid.CursorVisible() }

// Status reports the current agent status of the pane.
func (p *Pane) Status() term.PaneStatus { return p.grid.Status() }

func (p *Pane) ScrollbackLen() int         { return p.grid.ScrollbackLen() }
func (p *Pane) ScrollOffset() int          { return p.grid.ScrollOffset() }
func (p *Pane) SetScrollOffset(offset int) { p.grid.SetScrollOffset(offset) }

// Close tears down the process tree and emulator.
func (p *Pane) Close() error {
	p.closeOnce.Do(func() { close(p.closed) })

	errs := []error{p.pty.Kill(CloseGrace)}
	if err := p.grid.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close emulator: %w", err))
	}
	if n := p.dropped.Load(); n > 0 {
		errs = append(errs, fmt.Errorf("dropped %d keystroke(s): the child stopped reading its stdin", n))
	}

	p.failMu.Lock()
	errs = append(errs, p.failures...)
	p.failMu.Unlock()

	return errors.Join(errs...)
}

func (p *Pane) panicked(where string, r any) {
	err := fmt.Errorf("%s goroutine panicked: %v\n%s", where, r, debug.Stack())
	p.dead.Store(true)
	p.failMu.Lock()
	p.failures = append(p.failures, err)
	p.failMu.Unlock()
}

var _ io.Writer = (*Pane)(nil)
