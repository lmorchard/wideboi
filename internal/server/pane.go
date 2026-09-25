// Package server manages multiplexer state, PTY sessions, and layout calculation.
package server

import (
	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/server/ptyx"
	"github.com/lmorchard/wideboi/internal/server/term"
)

const CloseGrace = 2 * time.Second
const keyQueueDepth = 256
const ptyWriteTimeout = 50 * time.Millisecond

// Pane is the server-side owner of a PTY-backed process and VT emulator.
type Pane struct {
	id   int
	pty  *ptyx.Pane
	grid term.Grid
	cols int
	rows int

	// resizeMu serializes Resize against itself and against Close. Before
	// resizePanesLocked released s.mu around its resize calls, s.mu did
	// this job for free. Now two goroutines can be in that unlocked window
	// at once -- the Run loop handling a MsgResize/verb, and a pty-reader
	// goroutine calling onPaneExit for a *different* pane that also
	// resizes every survivor -- and both can reach the same pane's Resize
	// concurrently. p.cols/p.rows are plain ints and term.Grid.Resize does
	// an unlocked read-out/reflow/write-back of the cell buffer, so that
	// race is real, not theoretical.
	resizeMu sync.Mutex
	// renderMu keeps grid.Close from overlapping a pane snapshot. Renderers
	// acquire resizeMu first, so a renderer waiting behind a wedged Resize
	// cannot prevent Close from reaching grid.Close to break that wedge.
	renderMu sync.RWMutex

	// input is keys and mouse events for the child, in one queue so
	// they reach it in the order the user produced them.
	input   chan uv.Event
	dropped atomic.Uint64

	closed    chan struct{}
	closeOnce sync.Once
	closeErr  error

	// closeGrace overrides CloseGrace for this pane. Zero means the
	// default; see graceOrDefault.
	closeGrace time.Duration

	dead atomic.Bool

	isDashboard bool

	failMu   sync.Mutex
	failures []error
}

// graceOrDefault resolves the hangup grace for this pane.
//
// The resolution happens here, at the point of use, rather than in a
// constructor: the white-box fixtures in this package build &Pane{}
// literals directly (status_test.go, transport_close_test.go,
// pane_wedge_test.go), and a constructor cannot reach those. Zero
// therefore has to mean the default, or those fixtures would tear down
// with no grace at all and quietly stop exercising the real path.
func (p *Pane) graceOrDefault() time.Duration {
	if p.closeGrace <= 0 {
		return CloseGrace
	}
	return p.closeGrace
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
		input:  make(chan uv.Event, keyQueueDepth),
		closed: make(chan struct{}),
	}, nil
}

// NewCustomPane creates a non-PTY pane backed by an explicit term.Grid.
func NewCustomPane(id int, grid term.Grid, cols, rows int) *Pane {
	return &Pane{
		id:     id,
		grid:   grid,
		cols:   cols,
		rows:   rows,
		input:  make(chan uv.Event, keyQueueDepth),
		closed: make(chan struct{}),
	}
}

// ID returns the pane's unique identifier.
func (p *Pane) ID() int { return p.id }

// Start begins pumping bytes between the child and the emulator.
func (p *Pane) Start(onExit func()) {
	if p.pty == nil {
		go func() {
			<-p.closed
			onExit()
		}()
		return
	}

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
				if _, werr := p.pty.WriteBounded(buf[:n], ptyWriteTimeout); werr != nil {
					if errors.Is(werr, os.ErrDeadlineExceeded) {
						p.dropped.Add(uint64(n))
						continue
					}
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
			case ev := <-p.input:
				switch ev := ev.(type) {
				case uv.KeyEvent:
					p.grid.SendKey(ev)
				case uv.MouseEvent:
					p.grid.SendMouse(ev)
				}
			case <-p.closed:
				return
			}
		}
	}()
}

// SendKey queues a decoded key event for the pane's child.
func (p *Pane) SendKey(k uv.KeyEvent) {
	select {
	case p.input <- k:
	default:
		p.dropped.Add(1)
	}
}

// SendMouse queues a mouse event for the pane's child. It rides the key
// writer's goroutine because vt's SendMouse, like SendKey, writes to an
// io.Pipe and blocks until the pty-writer reads -- calling it inline
// under s.mu would stall the server behind a slow child.
//
// Motion is dropped once the queue is half full. It is the one event
// that is safe to lose -- the next motion supersedes it -- and a fast
// drag produces a flood of it, which must not crowd out the release
// that ends the drag. A child that never sees button-up believes the
// button is still held.
func (p *Pane) SendMouse(m uv.MouseEvent) {
	if _, ok := m.(uv.MouseMotionEvent); ok && len(p.input) >= cap(p.input)/2 {
		p.dropped.Add(1)
		return
	}
	select {
	case p.input <- m:
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

// DrawAt renders the pane's cell state at the given scroll offset onto dst inside area.
func (p *Pane) DrawAt(dst uv.Screen, area image.Rectangle, offset int) {
	p.grid.DrawAt(dst, area, offset)
}

// Resize changes the pane's logical size: the emulator's grid and the
// child's PTY window, in that order.
//
// The emulator first because term.Reflow reads its cells out, reflows
// them and writes them back, so the grid must be consistent before the
// child is told to redraw against it. The child second because
// TIOCSWINSZ raises SIGWINCH, and a well-behaved full-screen app repaints
// immediately.
//
// Held under resizeMu for its whole body, including the no-op check: two
// callers racing on the same pane could otherwise both read stale
// p.cols/p.rows, both decide a resize is needed, and both run
// term.Reflow's read-out/write-back concurrently against the same buffer.
func (p *Pane) Resize(cols, rows int) error {
	p.resizeMu.Lock()
	defer p.resizeMu.Unlock()

	if cols <= 0 || rows <= 0 {
		return fmt.Errorf("pane %d: refusing resize to %dx%d", p.id, cols, rows)
	}
	if cols == p.cols && rows == p.rows {
		return nil
	}
	p.cols, p.rows = cols, rows
	p.grid.Resize(cols, rows)
	if p.pty != nil {
		return p.pty.Resize(cols, rows)
	}
	return nil
}

// Write forwards raw bytes to the child process.
//
// Uses ptyWriteTimeout so a child that has stopped reading its stdin
// cannot hold callers (including Server.handleClientMsg under s.mu)
// indefinitely.
func (p *Pane) Write(b []byte) (int, error) {
	if p.pty == nil {
		return len(b), nil
	}
	n, err := p.pty.WriteBounded(b, ptyWriteTimeout)
	if errors.Is(err, os.ErrDeadlineExceeded) && n < len(b) {
		p.dropped.Add(uint64(len(b) - n))
	}
	return n, err
}

// Size reports logical dimensions.
//
// Locked under resizeMu, the same lock Resize holds while writing
// p.cols/p.rows: Resize no longer runs under s.mu (resizePanesLocked
// releases it around the resize calls), so a caller reading Size only
// under s.mu -- as Server.PaneSize does -- would otherwise race a
// concurrent Resize on the same plain, unguarded ints.
func (p *Pane) Size() (cols, rows int) {
	p.resizeMu.Lock()
	defer p.resizeMu.Unlock()
	return p.cols, p.rows
}

// CursorPosition reports the cell coordinates of the cursor relative to origin.
func (p *Pane) CursorPosition() image.Point { return p.grid.CursorPosition() }

// CursorVisible reports whether DECTCEM cursor visibility is enabled.
func (p *Pane) CursorVisible() bool { return p.grid.CursorVisible() }

// Title reports the pane's terminal title, or "" if its child has
// never set one.
func (p *Pane) Title() string { return p.grid.Title() }

// CWD reports the pane's current working directory, or "" if not set.
func (p *Pane) CWD() string { return p.grid.CWD() }

// UserVars reports the pane's agent metadata set via OSC 1337.
func (p *Pane) UserVars() map[string]string { return p.grid.UserVars() }

// Status reports the current agent status of the pane.
func (p *Pane) Status() protocol.PaneStatus { return p.grid.Status() }

// UpdateMessage constructs a protocol.MsgPaneUpdate for wire transport at the
// pane's current scroll offset.
// It returns false if the pane began closing before rendering could start.
func (p *Pane) UpdateMessage() (protocol.MsgPaneUpdate, bool) {
	return p.UpdateMessageForOffset(p.ScrollOffset(), false)
}

// UpdateMessageForOffset constructs a protocol.MsgPaneUpdate for wire transport
// at a specific scroll offset. When offset > 0, cursor visibility is suppressed.
// It returns false if the pane began closing before rendering could start.
func (p *Pane) UpdateMessageForOffset(offset int, unreadOutput bool) (protocol.MsgPaneUpdate, bool) {
	p.resizeMu.Lock()
	defer p.resizeMu.Unlock()
	p.renderMu.RLock()
	defer p.renderMu.RUnlock()
	select {
	case <-p.closed:
		return protocol.MsgPaneUpdate{}, false
	default:
	}
	cols, rows := p.cols, p.rows

	buf := uv.NewScreenBuffer(cols, rows)
	p.DrawAt(buf, image.Rect(0, 0, cols, rows), offset)

	lines := make([]protocol.LineData, rows)
	for y := 0; y < rows; y++ {
		line := make(protocol.LineData, cols)
		for x := 0; x < cols; x++ {
			c := buf.CellAt(x, y)
			if c == nil {
				line[x] = protocol.CellData{Content: " ", Width: 1}
				continue
			}
			content := c.Content
			if content == "" {
				content = " "
			}
			w := c.Width
			if w <= 0 {
				w = 1
			}
			line[x] = protocol.CellData{
				Content: content,
				Width:   w,
				Style:   protocol.EncodeStyle(c.Style),
			}
		}
		lines[y] = line
	}

	cp := p.CursorPosition()
	cursorVisible := p.CursorVisible()
	if offset > 0 {
		cursorVisible = false
	}
	return protocol.MsgPaneUpdate{
		PaneID:        p.id,
		Cols:          cols,
		Rows:          rows,
		Lines:         lines,
		CursorX:       cp.X,
		CursorY:       cp.Y,
		CursorVisible: cursorVisible,
		MouseTracking: p.grid.MouseTracking(),
		ScrollOffset:  offset,
		ScrollbackLen: p.ScrollbackLen(),
		UnreadOutput:  unreadOutput,
	}, true
}

// Generation reports the grid's change counter; see term.Grid.Generation.
func (p *Pane) Generation() uint64 { return p.grid.Generation() }

// OutputGen reports the child terminal output counter; see term.Grid.OutputGen.
func (p *Pane) OutputGen() uint64 { return p.grid.OutputGen() }

func (p *Pane) ScrollbackLen() int { return p.grid.ScrollbackLen() }
func (p *Pane) HistoryRows() protocol.MsgHistorySnapshot {
	p.renderMu.RLock()
	defer p.renderMu.RUnlock()
	length, rows := p.grid.HistoryRows()
	return protocol.MsgHistorySnapshot{PaneID: p.id, ScrollbackLen: length, Rows: rows}
}
func (p *Pane) ScrollOffset() int          { return p.grid.ScrollOffset() }
func (p *Pane) SetScrollOffset(offset int) { p.grid.SetScrollOffset(offset) }

// Close hangs up the pane's pty and closes its emulator.
//
// pty.Hangup and grid.Close run BEFORE resizeMu is taken, not after. They
// are the only two things that can unblock a Resize wedged mid-flight: the
// pinned x/vt writes an in-band resize notification into the emulator's
// own reply pipe during Resize when that mode is enabled, that pipe is
// unbuffered, and its only drainer is the pane's pty-writer pump -- which
// can itself be blocked in Master.Write against a child that has stopped
// reading its stdin. Hangup closes Master, unparking the pump so it drains
// the pipe; grid.Close unblocks the pipe directly. An earlier version of
// this fix took resizeMu first, which meant Close waited on the very lock
// a wedged Resize was holding, and only Close's own Hangup/grid.Close could
// ever release that Resize -- an unrecoverable hang on quit, not a race.
//
// This does reopen a window: Hangup/grid.Close can now run concurrently with
// a Resize that is not wedged, merely still in flight. That used to be a
// real race on the underlying pty fd -- ptyx.Pane.Hangup's Master.Close vs.
// ptyx.Pane.Resize's Setsize ioctl, confirmed under -race -- because
// pty.Setsize calls f.Fd() and hands the raw descriptor to the ioctl
// syscall with no reference held on the underlying poll.FD, so a
// concurrent Close could leave it pointed at a stale, reused descriptor.
// ptyx.Pane.Resize now issues that ioctl through the master's
// SyscallConn instead (see ptyx/ioctl.go's setsize), which increfs the
// poll.FD for the call and errors once the file is closed rather than
// racing it, so this window is merely concurrent, not racy: accepted,
// because no design with a single pane-level mutex can give both "Close
// never blocks on a wedged Resize" and "Close never overlaps an
// in-flight one" at once -- the pipe an unwedge depends on is exactly
// what a concurrently-running Resize is also touching. resizeMu is still
// taken for the bookkeeping that follows, once any wedge still in
// progress has had its chance to break.
//
// The whole body runs inside closeOnce, caching its result in closeErr.
// Only wrapping the `close(p.closed)` line, as this used to, left the
// teardown and grid.Close unprotected against a second concurrent Close
// call reaching them at the same time. Unreachable today -- every caller
// removes the pane from s.panes under s.mu before closing it -- but
// Close is public API this
// package cannot fully control, and enforcing the precondition here
// costs nothing: a second concurrent caller now simply waits for the
// first's teardown and gets the same cached result.
func (p *Pane) Close() error {
	p.closeOnce.Do(func() {
		close(p.closed)

		if p.pty != nil {
			p.pty.Hangup(p.graceOrDefault())
		}
		p.renderMu.Lock()
		gridErr := p.grid.Close()
		p.renderMu.Unlock()

		p.resizeMu.Lock()
		defer p.resizeMu.Unlock()

		var errs []error
		if gridErr != nil {
			errs = append(errs, fmt.Errorf("close emulator: %w", gridErr))
		}
		if n := p.dropped.Load(); n > 0 {
			errs = append(errs, fmt.Errorf("dropped %d keystroke(s): the child stopped reading its stdin", n))
		}

		p.failMu.Lock()
		errs = append(errs, p.failures...)
		p.failMu.Unlock()

		p.closeErr = errors.Join(errs...)
	})
	return p.closeErr
}

func (p *Pane) panicked(where string, r any) {
	err := fmt.Errorf("%s goroutine panicked: %v\n%s", where, r, debug.Stack())
	p.dead.Store(true)
	p.failMu.Lock()
	p.failures = append(p.failures, err)
	p.failMu.Unlock()
}

// recordFailure appends an operational error (e.g. a failed TIOCSWINSZ
// during resize) to be surfaced the next time the pane closes, alongside
// pump-goroutine panics. This exists so a failure can be collected without
// logging it: log.Printf writes to stderr, which is live alt-screen real
// estate while wideboi is running.
func (p *Pane) recordFailure(err error) {
	p.failMu.Lock()
	p.failures = append(p.failures, err)
	p.failMu.Unlock()
}

var _ io.Writer = (*Pane)(nil)
