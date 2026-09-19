// Command wideboi is a scrolling tiling terminal multiplexer for CLI agents.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/client"
	"github.com/lmorchard/wideboi/internal/hostterm"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/server"
	"github.com/lmorchard/wideboi/internal/transport"
)

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
		_ = t.Stop()
		return fmt.Errorf("start terminal: %w", err)
	}

	defer func() {
		scr.ExitAltScreen()
		_ = scr.Flush()
		_ = t.Stop()
	}()

	var (
		screenLock sync.Mutex
		stopped    atomic.Bool
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tp := transport.NewInProcChannel(256)
	srv := server.NewServer(tp, shell, cwd)

	guard := hostterm.NewGuard(func() error {
		stopped.Store(true)

		// Close server and tear down process tree
		serverErr := srv.Close()

		screenLock.Lock()
		defer screenLock.Unlock()
		scr.ExitAltScreen()
		_ = scr.Flush()
		err := t.Stop()

		if serverErr != nil {
			fmt.Fprintln(os.Stderr, "wideboi: teardown:", serverErr)
		}
		return err
	})

	defer guard.Stop()
	guard.Arm(syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)

	width, height, err := t.GetSize()
	if err != nil || width <= 0 || height <= 0 {
		width, height = 80, 24
	}

	cli := client.NewClient(tp, width, height)

	go func() {
		_ = srv.Run(ctx)
	}()

	cli.Attach(ctx)

	frame := time.NewTicker(16 * time.Millisecond)
	defer frame.Stop()

	for {
		select {
		case msg, ok := <-tp.ServerSend:
			if !ok {
				if stopped.Load() {
					time.Sleep(server.CloseResidual + signalExitMargin)
				}
				return nil
			}
			cli.HandleServerMsg(msg)

		case ev := <-t.Events():
			switch ev := ev.(type) {
			case uv.WindowSizeEvent:
				screenLock.Lock()
				if !stopped.Load() {
					scr.Resize(ev.Width, ev.Height)
				}
				screenLock.Unlock()
				cli.SendResize(ctx, ev.Width, ev.Height)

			case uv.KeyPressEvent:
				switch {
				// ctrl fallbacks are deliberately minimal. ctrl+w, ctrl+l,
				// ctrl+n and ctrl+h are delete-word, clear-screen,
				// next-history and backspace: claiming them makes every
				// shell in every pane worse. ctrl+q and ctrl+o survive as
				// the escape hatch for a terminal that is not sending
				// Option as Meta.
				case ev.MatchString("alt+q") || ev.MatchString("ctrl+q"):
					return nil
				case ev.MatchString("alt+h") || ev.MatchString("alt+left"):
					cli.SendVerb(ctx, protocol.VerbFocusLeft)
				case ev.MatchString("alt+l") || ev.MatchString("alt+right") || ev.MatchString("ctrl+o"):
					cli.SendVerb(ctx, protocol.VerbFocusRight)
				case ev.MatchString("alt+n"):
					cli.SendVerb(ctx, protocol.VerbNewColumn)
				case ev.MatchString("alt+w"):
					cli.SendVerb(ctx, protocol.VerbCycleWidth)
				case ev.MatchString("alt+x"):
					cli.SendVerb(ctx, protocol.VerbKillPane)
				case ev.MatchString("alt+j"):
					cli.SendVerb(ctx, protocol.VerbSmartJump)
				case ev.MatchString("alt+u") || ev.MatchString("pgup"):
					cli.SendScroll(ctx, 10)
				case ev.MatchString("alt+d") || ev.MatchString("pgdn"):
					cli.SendScroll(ctx, -10)
				default:
					cli.SendKey(ctx, uv.KeyEvent(ev))
				}
			}

		case <-frame.C:
			screenLock.Lock()
			if !stopped.Load() {
				cli.Draw(scr, srv.DrawPane, srv.CursorInfo)
				scr.Render()
				_ = scr.Flush()
			}
			screenLock.Unlock()
		}
	}
}
