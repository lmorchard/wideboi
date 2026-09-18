// Command wideboi is a scrolling tiling terminal multiplexer for CLI agents.
package main

import (
	"fmt"
	"image"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/client"
	"github.com/lmorchard/wideboi/internal/client/compose"
	"github.com/lmorchard/wideboi/internal/hostterm"
)

// signalExitMargin is the slack added to client.CloseResidual when the
// <-quit case waits for the signal goroutine's re-raise. See that case
// for the derivation.
const signalExitMargin = 500 * time.Millisecond

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cwd, _ := os.Getwd()

	t := uv.DefaultTerminal()
	scr := t.Screen()
	scr.EnterAltScreen()
	if err := t.Start(); err != nil {
		// t.Stop is documented safe to call without a prior successful
		// Start, and it un-does the alt-screen-enter sequence buffered
		// above (still unflushed at this point) before returning.
		_ = t.Stop()
		return fmt.Errorf("start terminal: %w", err)
	}

	// From here on, every exit out of run() — a normal return, an early
	// return below, or a panic unwinding through this function (e.g. a
	// degenerate pty size reaching the emulator; see the width/height
	// clamp below) — must restore the terminal. t.Stop and
	// scr.ExitAltScreen/Flush are all safe to call more than once
	// (verified against the vendored source: Stop's own idempotence
	// guards, and console.Restore no-ops once its saved state is nil),
	// so this defer is harmless to run again even when the guard's
	// shutdown closure already did the same restore on a normal exit.
	defer func() {
		scr.ExitAltScreen()
		_ = scr.Flush()
		_ = t.Stop()
	}()

	// screenLock keeps the shutdown closure's scr/t restore and the main
	// loop's own scr/t access mutually exclusive (hostterm.Guard runs the
	// shutdown closure on its own signal goroutine, independent of this
	// loop): without it, a signal arriving mid-Render/Flush races the
	// closure's ExitAltScreen/Flush/Stop on the same *TerminalScreen and
	// *Terminal.
	//
	// stopped is deliberately a plain atomic.Bool, not a bool guarded by
	// screenLock, and is set before closePanes rather than after. Two
	// requirements pull in the same direction here:
	//
	//   - closePanes must never be able to block on screenLock (see the
	//     closure below), so nothing that gates closePanes may need the
	//     lock either, including marking that shutdown has started.
	//   - The <-quit case needs to see that a signal caused this shutdown
	//     well before closePanes returns: an interactive shell ignores
	//     SIGTERM, so ptyx.Kill (via Pane.Close) burns the whole grace
	//     period before it even closes the PTY master, which is what
	//     unblocks the pane's PTY-reader pump and fires onExit -> close
	//     (quit). That happens partway through closePanes, not after it.
	//     A flag set only once the closure reaches its locked restore
	//     section would almost always still read false when <-quit fires.
	//
	// An atomic store can't block and is visible the instant it happens,
	// which satisfies both: it's safe to set first, and it's set early
	// enough for <-quit to observe it.
	var (
		screenLock sync.Mutex
		stopped    atomic.Bool
	)

	// panesMu guards the slice header only, and exists solely because
	// the guard below is armed before the spawn loop runs: the signal
	// goroutine may read this slice while this goroutine is still
	// appending to it. Once the spawn loop is done there are no further
	// writes, so the main loop reads it unlocked.
	var (
		panesMu sync.Mutex
		panes   []*client.Pane
	)
	snapshotPanes := func() []*client.Pane {
		panesMu.Lock()
		defer panesMu.Unlock()
		return append([]*client.Pane(nil), panes...)
	}

	guard := hostterm.NewGuard(func() error {
		// See the stopped/screenLock comment above for why this is set
		// before closePanes, unconditionally, with no lock involved.
		stopped.Store(true)

		// closePanes must run before taking screenLock, not after. It
		// touches neither scr nor t, so it never needed the lock, and
		// gating it behind one would make child teardown wait on
		// whatever the render loop is doing — including scr.Flush()
		// parked on a stalled consumer, which would make the armed
		// signals unable to reap children at all.
		paneErrs := closePanes(snapshotPanes())

		screenLock.Lock()
		defer screenLock.Unlock()
		scr.ExitAltScreen()
		_ = scr.Flush()
		err := t.Stop()

		// Only now, with the alt screen exited and the console restored,
		// is it safe to write diagnostics: anything printed earlier
		// lands in the alt screen and is erased by the restore that
		// follows it. Printing here rather than from a defer in run() is
		// deliberate — on the signal path, Guard.Arm re-raises the
		// signal as soon as this closure returns, so run()'s defers
		// usually never execute at all.
		for _, e := range paneErrs {
			fmt.Fprintln(os.Stderr, "wideboi: teardown:", e)
		}
		return err
	})

	// Arm before anything is spawned, and register Stop in the same
	// breath. Both halves matter. The window between t.Start() (raw mode
	// and the alt screen are live) and this point must contain no
	// blocking work and no child processes: a signal arriving in it
	// would kill the process by default disposition, leaving the host
	// terminal in raw mode on the alt screen, and leaving any pane
	// already spawned orphaned. Registering the defer here likewise
	// covers a panic out of the spawn loop below, which would otherwise
	// restore the terminal (via the defer above) while reaping nothing.
	//
	// The signal set is the spec's adopted teardown matrix. SIGINT is in
	// it even though raw mode clears ISIG — so Ctrl+C reaches the
	// focused pane as \x03 rather than as a signal — because a `kill
	// -INT` from outside is an ordinary way to stop a process and must
	// not bypass teardown.
	defer guard.Stop()
	guard.Arm(syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)

	width, height, err := t.GetSize()
	if err != nil || width <= 0 || height <= 0 {
		// Some ptys report a size of 0x0 with no error at all (observed
		// with no winsize set on the pty), so checking err alone misses
		// it. Left unclamped, (0-1)/2 and 0-1 both go negative below,
		// which panics inside the emulator's buffer allocation rather
		// than degrading gracefully the way a genuine zero does.
		width, height = 80, 24
	}

	// Hardcoded layout: two equal columns with a one-cell divider.
	// Floored at 1: even after the fallback above, nothing stops some
	// other pty from reporting a real-but-tiny size, and this arithmetic
	// must never hand the emulator a non-positive dimension.
	paneCols := max((width-1)/2, 1)
	paneRows := max(height-1, 1) // one row of chrome at the bottom

	for i := 0; i < 2; i++ {
		p, err := client.NewPane([]string{shell}, paneCols, paneRows, cwd)
		if err != nil {
			// No explicit teardown here: the deferred guard.Stop() above
			// runs the shutdown closure, which reaps whatever already
			// spawned and restores the terminal.
			return fmt.Errorf("pane %d: %w", i, err)
		}
		panesMu.Lock()
		panes = append(panes, p)
		panesMu.Unlock()
	}

	// quit is closed by whichever pane's shell exits first, and that
	// ends the whole multiplexer. That is correct for this hardcoded
	// two-pane layout — there is no UI for a pane-shaped hole and no way
	// to open a replacement — but onExit reads as a per-pane callback
	// and is not one. Plan 2 owns per-pane close: a pane exiting should
	// remove its column and only the last one should end the session.
	quit := make(chan struct{})
	var once sync.Once
	for _, p := range panes {
		p.Start(func() { once.Do(func() { close(quit) }) })
	}

	focus := 0
	frame := time.NewTicker(16 * time.Millisecond)
	defer frame.Stop()

	for {
		select {
		case <-quit:
			if stopped.Load() {
				// The guard's closure is already running (or has
				// finished) on the signal goroutine, and
				// hostterm.Guard.Arm re-raises the signal with its
				// default disposition right after that closure returns
				// — that re-raise is what is supposed to end this
				// process with 128+signo. Returning immediately here
				// would race it and risk exiting 0 first, so give the
				// re-raise a bounded window to land before falling
				// through to return.
				//
				// The bound is derived, not picked. quit fires when a
				// pane's Close reaches the point of shutting the pty
				// master, which is *after* the SIGTERM grace window has
				// already elapsed; what is left at that instant is
				// client.CloseResidual (ptyx's SIGKILL wait plus its
				// liveness poll), and then the closure's restore and
				// the re-raise, which are effectively instant.
				// signalExitMargin covers those. Deriving it this way is
				// what keeps a change to either the grace period or
				// ptyx's escalation budget from silently breaking the
				// exit-status contract.
				//
				// The sleep bounds this wait; it is not what keeps
				// run() from hanging afterward. The deferred guard.Stop()
				// below blocks on sync.Once until the signal goroutine's
				// closure actually finishes, however long that takes.
				// What guarantees it finishes is that this select loop
				// is about to exit for good: nothing will ever contend
				// for screenLock again, so the closure's own
				// screenLock.Lock() can always succeed.
				time.Sleep(client.CloseResidual + signalExitMargin)
			}
			return nil

		case ev := <-t.Events():
			switch ev := ev.(type) {
			case uv.WindowSizeEvent:
				// Resize is NOT implemented. This resizes the host
				// screen buffer so rendering stays in bounds and
				// nothing panics — and that is all it does.
				//
				// Specifically, after a resize: paneCols/paneRows and
				// the pane emulators keep their launch-time geometry,
				// so a narrowed window crops pane 0 and can push pane 1
				// and the status line entirely off-screen (SetCell
				// silently drops out-of-bounds writes); and no
				// ptyx.Pane.Resize / TIOCSWINSZ is issued, so each
				// child keeps wrapping at the width it was started
				// with. Nothing recovers when the window is widened
				// again.
				//
				// Doing it properly means recomputing the layout and
				// resizing the emulators, and Grid.Resize on the pinned
				// x/vt neither reflows nor repaints (see
				// internal/server/term/reflow_test.go and the spec).
				// Real resize belongs to Plan 2, together with the
				// layout engine. Do not add Grid.Resize here.
				screenLock.Lock()
				if !stopped.Load() {
					scr.Resize(ev.Width, ev.Height)
				}
				screenLock.Unlock()
			case uv.KeyPressEvent:
				switch {
				case ev.MatchString("ctrl+q"):
					return nil
				case ev.MatchString("ctrl+o"):
					focus = (focus + 1) % len(panes)
				default:
					// SendKey, never Write(ev.String()) — String() is a
					// display name like "ctrl+c", not the byte \x03.
					// SendKey is also the only non-blocking route to a
					// child; see client.Pane.SendKey.
					panes[focus].SendKey(uv.KeyEvent(ev))
				}
			}

		case <-frame.C:
			screenLock.Lock()
			if !stopped.Load() {
				// Composite: each pane's surface into the screen buffer.
				for i, p := range panes {
					x := i * (paneCols + 1)
					compose.Blit(scr, p.Surface(), image.Rect(x, 0, x+paneCols, paneRows))
				}
				for y := 0; y < paneRows; y++ {
					compose.WriteString(scr, paneCols, y, "│")
				}
				status := fmt.Sprintf(" focus: pane %d   ctrl+o switch   ctrl+q quit ", focus)
				for i, p := range panes {
					if p.Dead() {
						status += fmt.Sprintf("  [pane %d dead] ", i)
					}
				}
				compose.WriteString(scr, 0, height-1, status)

				scr.Render()
				_ = scr.Flush()
			}
			screenLock.Unlock()
		}
	}
}

// closePanes tears down every pane concurrently and returns whatever
// each one had to report. An interactive shell on a pty ignores SIGTERM,
// so each Close (via ptyx.Kill) burns its full grace period; running
// them concurrently means teardown waits ~1x grace instead of
// ~len(panes)x.
//
// The errors are returned rather than logged here because this runs
// while the alt screen is still up. The caller prints them after the
// terminal has been restored. Discarding them is not an option: a
// process tree that survived SIGKILL is reported nowhere else.
func closePanes(panes []*client.Pane) []error {
	errs := make([]error, len(panes))
	var wg sync.WaitGroup
	for i, p := range panes {
		wg.Add(1)
		go func(i int, p *client.Pane) {
			defer wg.Done()
			if err := p.Close(); err != nil {
				errs[i] = fmt.Errorf("pane %d: %w", i, err)
			}
		}(i, p)
	}
	wg.Wait()

	out := errs[:0]
	for _, err := range errs {
		if err != nil {
			out = append(out, err)
		}
	}
	return out
}
