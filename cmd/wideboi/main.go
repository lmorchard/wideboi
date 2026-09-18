// Command wideboi is a scrolling tiling terminal multiplexer for CLI agents.
package main

import (
	"fmt"
	"image"
	"log"
	"os"
	"sync"
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

	width, height, err := t.GetSize()
	if err != nil {
		width, height = 80, 24
	}

	// Hardcoded layout: two equal columns with a one-cell divider.
	paneCols := (width - 1) / 2
	paneRows := height - 1 // one row of chrome at the bottom

	var panes []*client.Pane
	for i := 0; i < 2; i++ {
		p, err := client.NewPane([]string{shell}, paneCols, paneRows, cwd)
		if err != nil {
			// No pane has been Start()ed yet, so nothing races this: tear
			// down whatever already spawned and restore the terminal
			// before returning, rather than stranding both.
			closePanes(panes)
			scr.ExitAltScreen()
			_ = scr.Flush()
			_ = t.Stop()
			return fmt.Errorf("pane %d: %w", i, err)
		}
		panes = append(panes, p)
	}

	quit := make(chan struct{})
	var once sync.Once
	for _, p := range panes {
		p.Start(func() { once.Do(func() { close(quit) }) })
	}

	// screenLock makes the shutdown closure below and the main loop's own
	// scr/t access mutually exclusive. Without it, a signal arriving
	// mid-Render/Flush races the shutdown closure's ExitAltScreen/Flush/
	// Stop on the same *TerminalScreen and *Terminal from a different
	// goroutine (hostterm.Guard runs its shutdown func on its own signal
	// goroutine, independent of this loop).
	var screenLock sync.Mutex

	guard := hostterm.NewGuard(func() error {
		screenLock.Lock()
		defer screenLock.Unlock()

		closePanes(panes)

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
			return nil

		case ev := <-t.Events():
			switch ev := ev.(type) {
			case uv.WindowSizeEvent:
				screenLock.Lock()
				scr.Resize(ev.Width, ev.Height)
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
