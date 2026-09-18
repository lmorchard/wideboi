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

	var panes []*client.Pane
	for i := 0; i < 2; i++ {
		p, err := client.NewPane([]string{shell}, paneCols, paneRows, cwd)
		if err != nil {
			// No pane has been Start()ed yet, so nothing races this: tear
			// down whatever already spawned before returning. The
			// deferred restore above handles the terminal.
			closePanes(panes)
			return fmt.Errorf("pane %d: %w", i, err)
		}
		panes = append(panes, p)
	}

	quit := make(chan struct{})
	var once sync.Once
	for _, p := range panes {
		p.Start(func() { once.Do(func() { close(quit) }) })
	}

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

	guard := hostterm.NewGuard(func() error {
		// See the stopped/screenLock comment above for why this is set
		// before closePanes, unconditionally, with no lock involved.
		stopped.Store(true)

		// closePanes must run before taking screenLock, not after. It
		// touches neither scr nor t, so it never needed the lock, and
		// gating it behind one would make child teardown wait on
		// whatever the render loop is doing — including scr.Flush()
		// parked on a stalled consumer, which would make SIGTERM/SIGHUP/
		// SIGQUIT unable to reap children at all.
		closePanes(panes)

		screenLock.Lock()
		defer screenLock.Unlock()
		scr.ExitAltScreen()
		_ = scr.Flush()
		return t.Stop()
	})
	guard.Arm(syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	defer guard.Stop()

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
				// The sleep bounds this wait; it is not what keeps
				// run() from hanging afterward. The deferred guard.Stop()
				// below blocks on sync.Once until the signal goroutine's
				// closure actually finishes, however long that takes.
				// What guarantees it finishes is that this select loop
				// is about to exit for good: nothing will ever contend
				// for screenLock again, so the closure's own
				// screenLock.Lock() can always succeed.
				time.Sleep(2 * time.Second)
			}
			return nil

		case ev := <-t.Events():
			switch ev := ev.(type) {
			case uv.WindowSizeEvent:
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
				compose.WriteString(scr, 0, height-1,
					fmt.Sprintf(" focus: pane %d   ctrl+o switch   ctrl+q quit ", focus))

				scr.Render()
				_ = scr.Flush()
			}
			screenLock.Unlock()
		}
	}
}

// closePanes tears down every pane concurrently. An interactive shell on a
// pty ignores SIGTERM, so each Close (via ptyx.Kill) burns its full grace
// period; running them concurrently means teardown waits ~1x grace instead
// of ~len(panes)x.
func closePanes(panes []*client.Pane) {
	var wg sync.WaitGroup
	for _, p := range panes {
		wg.Add(1)
		go func(p *client.Pane) {
			defer wg.Done()
			_ = p.Close()
		}(p)
	}
	wg.Wait()
}
