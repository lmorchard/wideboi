// Command wideboi is a scrolling tiling terminal multiplexer for CLI agents.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/client"
	"github.com/lmorchard/wideboi/internal/hostterm"
	"github.com/lmorchard/wideboi/internal/logger"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/server"
	"github.com/lmorchard/wideboi/internal/transport"
)

const signalExitMargin = 500 * time.Millisecond

func defaultSocketPath() string {
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("wideboi-%d", os.Getuid()))
	_ = os.MkdirAll(dir, 0700)
	return filepath.Join(dir, "default.sock")
}

// Stamped by the linker at build time; see LDFLAGS in the Makefile.
// The defaults are what an unstamped `go build` produces, and saying
// so is more useful than an empty string.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version", "-v":
			fmt.Printf("wideboi %s (%s, built %s)\n", version, commit, date)
			return
		case "server":
			fatal(runServer(defaultSocketPath()))
			return
		case "attach":
			fatal(runAttach(defaultSocketPath()))
			return
		}
	}
	fatal(run())
}

// fatal reports err on stderr and exits non-zero.
//
// Deliberately not log.Fatal. logger.Init calls slog.SetDefault, which
// since Go 1.21 also repoints the standard log package at the slog
// handler -- and that handler writes to a file under the runtime dir.
// That redirection is wanted for everything else (log.Printf from
// anywhere writes to stderr, which is live alt-screen real estate while
// wideboi is running, and LESSONS.md records that hazard), but it turned
// the one message a user most needs to see -- "no wideboi server running
// at ...; start one with 'wideboi server'" -- into a silent exit 1.
//
// os.Exit skips deferred closes, which is fine: os.File writes are
// unbuffered, so nothing already logged is lost.
func fatal(err error) {
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, "wideboi:", err)
	os.Exit(1)
}

// parseLayout maps WIDEBOI_LAYOUT onto a mode.
//
// An unknown value is an error rather than a silent fallback. A typo
// that quietly starts the wrong layout is the same defect shape as the
// pgdn binding that shipped dead: an unmatchable config value looks
// exactly like an absent one, so nothing ever tells you. See
// docs/LESSONS.md, "A binding nobody typed is a binding nobody
// verified."
//
// Only the halves that own a server read this. An attaching client
// takes the mode from the server's snapshot, because layout is shared
// session state.
func parseLayout(name string) (protocol.LayoutMode, error) {
	switch name {
	case "", "scroll":
		return protocol.LayoutScroll, nil
	case "cards":
		return protocol.LayoutCards, nil
	default:
		return 0, fmt.Errorf("WIDEBOI_LAYOUT=%q: want \"scroll\" or \"cards\"", name)
	}
}

func runServer(socketPath string) error {
	f, _ := logger.Init("server")
	if f != nil {
		defer f.Close()
	}
	slog.Info("starting wideboi server", "socketPath", socketPath)

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cwd, _ := os.Getwd()

	layoutMode, err := parseLayout(os.Getenv("WIDEBOI_LAYOUT"))
	if err != nil {
		return err
	}

	sl, err := transport.NewSocketListener(socketPath)
	if err != nil {
		return err
	}
	defer sl.Close()

	srv := server.NewServer(nil, shell, cwd)
	srv.SetLayout(layoutMode)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv.ListenSocket(ctx, sl)

	return srv.Run(ctx)
}

func runAttach(socketPath string) error {
	f, _ := logger.Init("client")
	if f != nil {
		defer f.Close()
	}
	slog.Info("attaching wideboi client to socket", "socketPath", socketPath)

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return fmt.Errorf("no wideboi server running at %s (start one with 'wideboi server'): %w", socketPath, err)
	}

	prefixName := os.Getenv("WIDEBOI_PREFIX")
	if prefixName == "" {
		prefixName = defaultPrefix
	}
	prefix, prefixLabel, err := parsePrefix(prefixName)
	if err != nil {
		conn.Close()
		return err
	}

	t := uv.DefaultTerminal()
	scr := t.Screen()
	scr.EnterAltScreen()
	if err := t.Start(); err != nil {
		_ = t.Stop()
		conn.Close()
		return fmt.Errorf("start terminal: %w", err)
	}

	defer func() {
		scr.ExitAltScreen()
		_ = scr.Flush()
		_ = t.Stop()
	}()

	var screenLock sync.Mutex

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cConn := transport.NewClientSocketConn(conn, 256)
	cConn.RunPumps(ctx)

	width, height, termErr := t.GetSize()
	if termErr != nil || width <= 0 || height <= 0 {
		width, height = 80, 24
	}

	cli := client.NewClient(cConn, width, height, prefixLabel)
	cli.SetDetachable(true)
	rt := &router{prefix: prefix, detachable: true}

	cli.Attach(ctx)

	frame := time.NewTicker(16 * time.Millisecond)
	defer frame.Stop()

	for {
		select {
		case msg, ok := <-cConn.ServerSendChan():
			if !ok {
				// The stream ended. Distinguish a server that shut
				// down or was detached from cleanly -- both ordinary,
				// both exit 0 -- from a protocol failure, which used
				// to look exactly the same from here and so hid a
				// defect that killed every session on the first
				// coloured cell a child printed.
				if err := cConn.Err(); err != nil {
					return fmt.Errorf("connection to wideboi server failed: %w", err)
				}
				slog.Info("server closed the connection")
				return nil
			}
			cli.HandleServerMsg(msg)

		case ev := <-t.Events():
			switch ev := ev.(type) {
			case uv.WindowSizeEvent:
				screenLock.Lock()
				scr.Resize(ev.Width, ev.Height)
				screenLock.Unlock()
				cli.SendResize(ctx, ev.Width, ev.Height)

			case uv.KeyPressEvent:
				act := rt.route(ev)
				switch act.Kind {
				case routeQuit, routeDetach:
					// Detaching leaves the server and its children
					// running; the socket close is what tells the
					// server this client is gone.
					slog.Info("client detaching", "verb", act.Kind)
					return nil
				case routeVerb:
					cli.SendVerb(ctx, act.Verb)
				case routeScroll:
					cli.SendScroll(ctx, act.Scroll)
				case routeForward:
					cli.SendKey(ctx, uv.KeyEvent(ev))
				case routeIgnore:
				}
				cli.SetControlMode(rt.control)
				cli.SetHelpVisible(rt.help)
			}

		case <-frame.C:
			screenLock.Lock()
			cli.Draw(scr, nil, nil)
			scr.Render()
			_ = scr.Flush()
			screenLock.Unlock()
		}
	}
}

func run() error {
	sockPath := defaultSocketPath()
	if conn, err := net.Dial("unix", sockPath); err == nil {
		conn.Close()
		return runAttach(sockPath)
	}

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
	// Parse before the alt screen is entered, so a bad value prints
	// where the user can read it.
	layoutMode, err := parseLayout(os.Getenv("WIDEBOI_LAYOUT"))
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
	srv.SetLayout(layoutMode)

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
				cli.SetHelpVisible(rt.help)
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
