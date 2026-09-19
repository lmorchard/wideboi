package server

// White-box (package server, not server_test): it constructs a *Pane
// directly with a fake Grid, bypassing NewPane, which only this package
// can do. That is deliberate. Reproducing the real wedge -- the pinned
// x/vt writing an in-band resize notification into its own unbuffered
// reply pipe while the only drainer is blocked writing to a child that
// stopped reading its stdin -- needs either a real child paused with
// SIGSTOP plus enough bytes to fill the kernel's tty input queue (racy:
// queue sizes are OS-dependent and untested here), or a fake Grid that
// models the same coupling directly: Resize blocks on a channel, and
// Close is what closes that channel. That mirrors the real mechanism --
// grid.Close reaches Emulator.Close, which does pw.CloseWithError(io.EOF)
// on the very pipe a wedged Resize is blocked writing into, which is
// exactly what unblocks it -- rather than merely asserting Close doesn't
// wait for an unrelated timer.
//
// This fidelity mattered in practice: an earlier version of this fake had
// Close return immediately without touching the block, independent of
// Resize. That version made the real fix look broken (Close still hung,
// because resizeMu was never released) when the actual bug was in the
// fake, not the fix -- Close's own bookkeeping still takes resizeMu after
// Kill/grid.Close, and it only stops waiting once grid.Close has actually
// done what the real one does: break the wedge.

import (
	"image"
	"sync"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/server/ptyx"
	"github.com/lmorchard/wideboi/internal/server/term"
)

// blockingGrid is a term.Grid whose Resize blocks until Close is called,
// modeling a Resize wedged inside the real emulator's unbuffered reply
// pipe and Close being the thing that breaks it. Every other method is a
// cheap stub; this test does not exercise them.
type blockingGrid struct {
	resizeEntered chan struct{}
	unblock       chan struct{}
	closeOnce     sync.Once
}

func newBlockingGrid() *blockingGrid {
	return &blockingGrid{
		resizeEntered: make(chan struct{}),
		unblock:       make(chan struct{}),
	}
}

func (g *blockingGrid) Write(p []byte) (int, error)     { return len(p), nil }
func (g *blockingGrid) Read(p []byte) (int, error)      { return 0, nil }
func (g *blockingGrid) SendKey(uv.KeyEvent)             {}
func (g *blockingGrid) SendText(string)                 {}
func (g *blockingGrid) CursorPosition() image.Point     { return image.Point{} }
func (g *blockingGrid) CursorVisible() bool             { return false }
func (g *blockingGrid) Status() term.PaneStatus         { return term.StatusIdle }
func (g *blockingGrid) ScrollbackLen() int              { return 0 }
func (g *blockingGrid) ScrollOffset() int               { return 0 }
func (g *blockingGrid) SetScrollOffset(int)             {}
func (g *blockingGrid) Draw(uv.Screen, image.Rectangle) {}
func (g *blockingGrid) CellAt(x, y int) *uv.Cell        { return nil }
func (g *blockingGrid) Size() (int, int)                { return 10, 10 }

// Resize blocks until Close is called, standing in for term.Reflow's real
// read-out/write-back blocking on an unbuffered pipe nobody is draining.
func (g *blockingGrid) Resize(cols, rows int) {
	close(g.resizeEntered)
	<-g.unblock
}

// Close is the only thing that unblocks a pending Resize, matching
// Emulator.Close's real CloseWithError call on the same pipe a wedged
// Resize is blocked writing into.
func (g *blockingGrid) Close() error {
	g.closeOnce.Do(func() { close(g.unblock) })
	return nil
}

// Regression guard for "Close now blocks the only thing that can unblock
// it": a Pane.Resize wedged inside Grid.Resize while holding resizeMu used
// to mean Close -- which used to take resizeMu before pty.Kill/grid.Close
// -- could never reach the two calls that are the real system's only way
// to un-wedge a Resize (Kill closes Master, unparking the pty-writer pump
// that drains the reply pipe; grid.Close unblocks the pipe directly).
// Close now runs those two calls before taking resizeMu, specifically so
// it does not depend on a wedged Resize ever finishing on its own -- it
// depends on Close's own Kill/grid.Close making it finish.
func TestCloseDoesNotHangOnWedgedResize(t *testing.T) {
	realPty, err := ptyx.Spawn([]string{"/bin/cat"}, 10, 10, "")
	if err != nil {
		t.Fatalf("spawn real pty: %v", err)
	}

	grid := newBlockingGrid()
	p := &Pane{
		id:     1,
		pty:    realPty,
		grid:   grid,
		cols:   10,
		rows:   10,
		keys:   make(chan uv.KeyEvent, keyQueueDepth),
		closed: make(chan struct{}),
	}

	resizeDone := make(chan error, 1)
	go func() {
		resizeDone <- p.Resize(20, 20)
	}()

	select {
	case <-grid.resizeEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("test setup bug: Resize never entered the blocking Grid.Resize")
	}
	// Resize is now inside grid.Resize, holding p.resizeMu, and stays there
	// until something calls grid.Close -- nothing does yet.

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- p.Close()
	}()

	select {
	case <-closeDone:
		// Close returned. Correct: it ran Kill/grid.Close, which broke the
		// wedge, which let Resize finish and release resizeMu before
		// Close's own bookkeeping needed it.
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return within 5s while Resize was wedged -- " +
			"it never reached grid.Close, or grid.Close no longer breaks the " +
			"wedge before Close needs resizeMu for its own bookkeeping: an " +
			"unrecoverable hang on quit")
	}

	select {
	case <-resizeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Resize never returned even after Close ran -- grid.Close should have unblocked it")
	}
}
