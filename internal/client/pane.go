// Package client owns the host terminal: composition, rendering, input.
package client

import (
	"errors"
	"fmt"
	"io"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/client/compose"
	"github.com/lmorchard/wideboi/internal/server/ptyx"
	"github.com/lmorchard/wideboi/internal/server/term"
)

// CloseGrace is how long Close gives a pane's process tree to exit on
// SIGTERM before ptyx.Kill escalates to SIGKILL. An interactive shell on
// a pty ignores SIGTERM, so in practice every Close burns this whole
// window before the pty master is closed.
const CloseGrace = 2 * time.Second

// CloseResidual bounds the teardown work Close still has ahead of it at
// the moment the pty master closes — i.e. at the moment a pane's reader
// pump unblocks and fires onExit. It is re-exported from ptyx rather
// than restated as a second literal so that raising either half of the
// budget cannot silently invalidate a caller that waits on it; see the
// <-quit case in cmd/wideboi/main.go, which is the one such caller.
const CloseResidual = ptyx.KillResidual

// keyQueueDepth is how many decoded keys a pane will hold for a child
// that has stopped reading its stdin. Anything past this is dropped, not
// queued and not blocked on: see SendKey.
const keyQueueDepth = 256

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

	// keys decouples the caller of SendKey from the emulator. See
	// SendKey and Start for why this buffer has to exist.
	keys    chan uv.KeyEvent
	dropped atomic.Uint64

	// closed is shut by Close and is what lets the key-writer goroutine
	// exit. It is a channel rather than a flag so the goroutine can
	// select on it; closeOnce keeps a second Close from panicking.
	closed    chan struct{}
	closeOnce sync.Once

	// dead is set when one of this pane's pump goroutines panics. A dead
	// pane stops updating but does not take the multiplexer with it.
	dead atomic.Bool

	failMu   sync.Mutex
	failures []error
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
		keys:    make(chan uv.KeyEvent, keyQueueDepth),
		closed:  make(chan struct{}),
	}, nil
}

// Start begins pumping bytes between the child and the emulator. Three
// goroutines are required, not one:
//
//	PTY  -> emulator : the child's output becomes cell state
//	emulator -> PTY  : encoded keystrokes and terminal replies
//	keys -> emulator : decoded key events, off the caller's goroutine
//
// The second is mandatory because Grid.SendKey writes to an io.Pipe and
// blocks until something reads. Without this drain, the first keypress
// deadlocks the process.
//
// The third exists because that same chain can block in the other
// direction: the emulator->PTY pump parks in Master.Write whenever a
// child stops draining its stdin, which stops it reading the pipe, which
// makes SendKey block. Whoever calls SendKey would then block too — and
// that caller is the client's single event loop, which owns rendering
// and the quit keys. See SendKey.
//
// A panic in any of the three is recovered, recorded, and marks this
// pane dead; the other panes and the event loop carry on. onExit is
// called only on a clean end-of-stream exit from the PTY reader, never
// on a panic, because onExit is what shuts the whole multiplexer down.
//
// The accepted consequence of that isolation: a dead pane's child is
// still running with nobody draining its pty, so it will eventually
// block writing. It is reaped like any other pane at teardown, and its
// recorded panic is reported by Close.
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
				p.dirty.Store(true)
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

	// Queued key events -> emulator. This is the goroutine that is
	// allowed to block on a wedged child, so that SendKey's caller is
	// not. Close unblocks it two ways: closing p.closed ends the select,
	// and closing the grid makes an in-flight SendKey return.
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

// SendKey queues a decoded key event for the pane's child, to be encoded
// as the bytes a terminal application expects.
//
// This never blocks and never fails: if the queue is full — which means
// the child has stopped reading its stdin for the length of a 256-key
// backlog — the key is dropped and counted, and the count is reported by
// Close. Dropping is deliberate. The alternative is blocking the
// client's event loop on an unresponsive child, which would freeze
// rendering and the quit keys for every pane at once.
func (p *Pane) SendKey(k uv.KeyEvent) {
	select {
	case p.keys <- k:
	default:
		p.dropped.Add(1)
	}
}

// Dead reports whether one of this pane's pump goroutines panicked. A
// dead pane's cells are frozen at whatever they last showed.
func (p *Pane) Dead() bool { return p.dead.Load() }

// Surface redraws the pane's cells if anything changed, and returns them.
func (p *Pane) Surface() compose.Surface {
	if p.dirty.Swap(false) {
		p.grid.Draw(p.surface, p.surface.Bounds())
	}
	return p.surface
}

// Write forwards raw bytes to the pane's child process, bypassing the
// emulator. Use this for pasted text, not for keystrokes.
//
// Unlike SendKey this is a direct write to the pty master, so it blocks
// if the child is not reading. Nothing calls it from the event loop.
func (p *Pane) Write(b []byte) (int, error) { return p.pty.Master.Write(b) }

// Size reports the pane's logical size.
func (p *Pane) Size() (cols, rows int) { return p.cols, p.rows }

// Close tears down the pane's entire process tree and its emulator.
//
// Both matter: killing the PTY closes Master, which unblocks the PTY->
// emulator pump in Start; closing the grid unblocks the emulator->PTY
// pump, which is otherwise parked forever on Grid.Read once the pane
// stops receiving keys. Without the second, that goroutine leaks for
// the life of the process.
//
// The returned error is everything this pane has to report, joined: a
// process tree that survived SIGKILL, a failed emulator close, any panic
// recovered in a pump goroutine, and any keystrokes dropped because the
// child stopped reading. Callers must not discard it — a surviving
// process tree is exactly the leak this package exists to prevent, and
// this is the only place it is ever observable.
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

// panicked records a recovered panic from one of the pump goroutines and
// marks the pane dead. The value and stack are kept rather than
// discarded: x/vt is an untagged pinned dependency, and a parser bug
// there would otherwise present only as a pane that quietly stopped
// updating.
func (p *Pane) panicked(where string, r any) {
	err := fmt.Errorf("%s goroutine panicked: %v\n%s", where, r, debug.Stack())
	p.dead.Store(true)
	p.failMu.Lock()
	p.failures = append(p.failures, err)
	p.failMu.Unlock()
}

var _ io.Writer = (*Pane)(nil)
