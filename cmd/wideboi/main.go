// Command wideboi is a scrolling tiling terminal multiplexer for CLI agents.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/client"
	"github.com/lmorchard/wideboi/internal/hostterm"
	"github.com/lmorchard/wideboi/internal/server"
	"github.com/lmorchard/wideboi/internal/transport"
)

const signalExitMargin = 500 * time.Millisecond

func defaultSocketPath() string {
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("wideboi-%d", os.Getuid()))
	_ = os.MkdirAll(dir, 0700)
	return filepath.Join(dir, "default.sock")
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "server":
			if err := runServer(defaultSocketPath()); err != nil {
				log.Fatal(err)
			}
			return
		case "attach":
			if err := runAttach(defaultSocketPath()); err != nil {
				log.Fatal(err)
			}
			return
		}
	}
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func runServer(socketPath string) error {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cwd, _ := os.Getwd()

	sl, err := transport.NewSocketListener(socketPath)
	if err != nil {
		return err
	}
	defer sl.Close()

	tp := transport.NewInProcChannel(256)
	srv := server.NewServer(tp, shell, cwd)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	return srv.Run(ctx)
}

func runAttach(socketPath string) error {
	return run()
}

func run() error {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cwd, _ := os.Getwd()

	prefixName := os.Getenv("WIDEBOI_PREFIX")
	if prefixName == "" {
		prefixName = defaultPrefix
	}
	prefix, prefixLabel, err := parsePrefix(prefixName)
	if err != nil {
		return err
	}

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

	width, height, termErr := t.GetSize()
	if termErr != nil || width <= 0 || height <= 0 {
		width, height = 80, 24
	}

	cli := client.NewClient(tp, width, height, prefixLabel)
	rt := &router{prefix: prefix}

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
				act := rt.route(ev)
				switch act.Kind {
				case routeQuit:
					return nil
				case routeDetach:
					return nil
				case routeVerb:
					cli.SendVerb(ctx, act.Verb)
				case routeScroll:
					cli.SendScroll(ctx, act.Scroll)
				case routeForward:
					cli.SendKey(ctx, uv.KeyEvent(ev))
				case routeIgnore:
				}
				// After every key, not only the ones that changed the
				// mode: the bar must never be able to disagree with the
				// router about which mode is active.
				cli.SetControlMode(rt.control)
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
