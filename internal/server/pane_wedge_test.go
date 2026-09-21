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
	"context"
	"errors"
	"image"
	"os"
	"sync"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/server/ptyx"
	"github.com/lmorchard/wideboi/internal/server/term"
	"github.com/lmorchard/wideboi/internal/transport"
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
func (g *blockingGrid) Title() string                   { return "" }
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

// Regression guard for issue #38: a child process that stops reading its stdin
// must not wedge the pty-writer pump, which would otherwise fill the emulator's
// unbuffered reply pipe, cause SafeEmulator.SendKey to block holding se.mu, and
// cause the server's render loop (broadcastPaneUpdates under s.mu) to freeze
// all panes across the multiplexer.
func TestChildNotReadingStdinDoesNotFreezeServer(t *testing.T) {
	tp := transport.NewInProcChannel(32)
	srv := NewServer(tp, "/bin/sh", "")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = srv.Run(ctx)
	}()

	// Attach so server spawns 2 panes running shell
	tp.SendClient(ctx, protocol.MsgAttach{Cols: 80, Rows: 24})

	// Wait for layout snapshot
	select {
	case <-tp.ServerSend:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for initial attach response")
	}

	srv.mu.Lock()
	p1 := srv.panes[1]
	p2 := srv.panes[2]
	srv.mu.Unlock()

	// Tell pane 1's shell to run sleep 100 so its foreground process does
	// not read stdin.
	p1.SendText("sleep 100\n")
	time.Sleep(200 * time.Millisecond)

	// Fill pane 1's tty input queue completely across OSes (1024 on macOS,
	// 4096+ on Linux) so the next child-bound write blocks in the kernel if
	// unbounded.
	buf := make([]byte, 1024)
	for i := range buf {
		buf[i] = 'A'
	}
	buf[len(buf)-1] = '\n'

	timeouts := 0
	for i := 0; i < 64 && timeouts < 3; i++ {
		n, err := p1.pty.WriteBounded(buf, 10*time.Millisecond)
		if errors.Is(err, os.ErrDeadlineExceeded) && n == 0 {
			timeouts++
		} else {
			timeouts = 0
		}
		if err != nil && !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("fill write %d: %v", i, err)
		}
	}
	if timeouts < 3 {
		t.Fatal("failed to saturate tty input buffer within 64KB")
	}

	// Send keystrokes to pane 1:
	// Key 1 enters the pty-writer pump, which hits the write deadline
	// (trading dropped child-bound bytes) and continues draining the pipe.
	p1.SendKey(uv.KeyPressEvent{Code: '1', Text: "1"})
	time.Sleep(100 * time.Millisecond)

	// Key 2 is processed without blocking SafeEmulator.SendKey.
	p1.SendKey(uv.KeyPressEvent{Code: '2', Text: "2"})
	time.Sleep(100 * time.Millisecond)

	// Verify pane 1 Draw() completes and does not hang on se.mu.
	draw1Done := make(chan struct{})
	go func() {
		scr := uv.NewScreenBuffer(40, 24)
		p1.Draw(scr, image.Rect(0, 0, 40, 24))
		close(draw1Done)
	}()

	select {
	case <-draw1Done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("p1.Draw is blocked on se.mu")
	}

	// Verify the server's render loop (frameTicker -> broadcastPaneUpdates)
	// has not deadlocked srv.mu, and other panes remain fully responsive.
	responsive := make(chan struct{})
	go func() {
		srv.mu.Lock()
		defer srv.mu.Unlock()
		_ = p2.CursorPosition()
		close(responsive)
	}()

	select {
	case <-responsive:
	case <-time.After(1 * time.Second):
		t.Fatal("srv.mu is deadlocked by a child that stopped reading stdin")
	}

	_ = srv.Close()
}
