// Command wideboi is a scrolling tiling terminal multiplexer for CLI agents.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/lmorchard/wideboi/internal/client"
	"github.com/lmorchard/wideboi/internal/config"
	"github.com/lmorchard/wideboi/internal/hostterm"
	"github.com/lmorchard/wideboi/internal/keys"
	"github.com/lmorchard/wideboi/internal/logger"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/server"
	"github.com/lmorchard/wideboi/internal/transport"
)

const signalExitMargin = 500 * time.Millisecond

// defaultSocketPath is where `wideboi server` listens, where `wideboi
// attach` dials, and what a plain `wideboi` probes before deciding
// whether to start its own session.
func defaultSocketPath() string {
	if p := os.Getenv("WIDEBOI_SOCK"); p != "" {
		_ = os.MkdirAll(filepath.Dir(p), 0700)
		return p
	}
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

type cliOptions struct {
	subcommand string
	flags      config.ConfigFlags
	showVer    bool
	showHelp   bool
}

func parseCLI(args []string) (cliOptions, error) {
	var opts cliOptions
	var flagArgs []string
	skipNext := false

	for i := 0; i < len(args); i++ {
		if skipNext {
			flagArgs = append(flagArgs, args[i])
			skipNext = false
			continue
		}
		arg := args[i]
		if opts.subcommand == "" && (arg == "server" || arg == "attach" || arg == "version" || arg == "help") {
			opts.subcommand = arg
			continue
		}
		flagArgs = append(flagArgs, arg)
		if arg == "-c" || arg == "-config" || arg == "--config" ||
			arg == "-l" || arg == "-layout" || arg == "--layout" ||
			arg == "-p" || arg == "-prefix" || arg == "--prefix" ||
			arg == "-s" || arg == "-socket" || arg == "--socket" ||
			arg == "-shell" || arg == "--shell" {
			skipNext = true
		}
	}

	fs := flag.NewFlagSet("wideboi", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	fs.StringVar(&opts.flags.ConfigFile, "c", "", "path to TOML configuration file")
	fs.StringVar(&opts.flags.ConfigFile, "config", "", "path to TOML configuration file")
	fs.StringVar(&opts.flags.Layout, "l", "", "layout strategy: cards or scroll")
	fs.StringVar(&opts.flags.Layout, "layout", "", "layout strategy: cards or scroll")
	fs.StringVar(&opts.flags.Prefix, "p", "", "prefix key, e.g. ctrl+b, ctrl+space")
	fs.StringVar(&opts.flags.Prefix, "prefix", "", "prefix key, e.g. ctrl+b, ctrl+space")
	fs.StringVar(&opts.flags.Socket, "s", "", "unix domain socket path")
	fs.StringVar(&opts.flags.Socket, "socket", "", "unix domain socket path")
	fs.StringVar(&opts.flags.Shell, "shell", "", "shell executable path")
	fs.BoolVar(&opts.showVer, "v", false, "display version and build information")
	fs.BoolVar(&opts.showVer, "version", false, "display version and build information")
	fs.BoolVar(&opts.showHelp, "h", false, "show help and usage information")
	fs.BoolVar(&opts.showHelp, "help", false, "show help and usage information")

	if err := fs.Parse(flagArgs); err != nil {
		return opts, err
	}
	if opts.subcommand == "version" {
		opts.showVer = true
	}
	if opts.subcommand == "help" {
		opts.showHelp = true
	}
	return opts, nil
}

func printHelp(w io.Writer) {
	fmt.Fprintf(w, `Usage:
  wideboi [flags]            Start an in-process session, or attach if running
  wideboi [flags] server     Start a background server listening on socket
  wideboi [flags] attach     Attach a client to a running server
  wideboi version            Display version information
  wideboi help               Show this help text

Flags:
  -c, --config <path>    Path to TOML configuration file
                         (default: $XDG_CONFIG_HOME/wideboi/config.toml
                          or ~/.config/wideboi/config.toml)
  -l, --layout <mode>    Layout strategy: "cards" (default) or "scroll"
  -p, --prefix <key>     Control mode prefix key: "ctrl+<letter>" or "ctrl+space"
                         (default: "ctrl+b")
  -s, --socket <path>    Unix domain socket path
                         (default: $TMPDIR/wideboi-<uid>/default.sock)
      --shell <path>     Shell executable to launch in panes
                         (default: $SHELL or /bin/sh)
  -v, --version          Print version and exit
  -h, --help             Show this help text and exit

Environment Variables:
  WIDEBOI_LAYOUT         Layout mode override ("cards" or "scroll")
  WIDEBOI_PREFIX         Prefix key override (e.g. "ctrl+b")
  WIDEBOI_SOCK           Socket path override
  WIDEBOI_SHELL          Shell path override
  SHELL                  Default shell path (when shell is not set in config)
`)
}

func main() {
	opts, err := parseCLI(os.Args[1:])
	if err != nil {
		fatal(err)
	}
	if opts.showVer {
		fmt.Printf("wideboi %s (%s, built %s)\n", version, commit, date)
		return
	}
	if opts.showHelp {
		printHelp(os.Stdout)
		return
	}

	cfg, bindings, err := config.Load(opts.flags, os.Getenv)
	if err != nil {
		fatal(err)
	}

	switch opts.subcommand {
	case "server":
		fatal(runServer(cfg))
	case "attach":
		fatal(runAttach(cfg, bindings))
	default:
		fatal(run(cfg, bindings))
	}
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
	case "", "cards":
		return protocol.LayoutCards, nil
	case "scroll":
		return protocol.LayoutScroll, nil
	default:
		return 0, fmt.Errorf("WIDEBOI_LAYOUT=%q: want \"scroll\" or \"cards\"", name)
	}
}

func runServer(cfg config.Config) error {
	f, _ := logger.Init("server")
	if f != nil {
		defer f.Close()
	}
	slog.Info("starting wideboi server", "socketPath", cfg.Socket)

	cwd, _ := os.Getwd()

	sl, err := transport.NewSocketListener(cfg.Socket)
	if err != nil {
		return err
	}
	defer sl.Close()

	srv := server.NewServer(nil, cfg.Shell, cwd)
	srv.SetLayout(cfg.LayoutMode)
	if len(cfg.WidthPresets) > 0 {
		srv.SetWidthPresets(cfg.WidthPresets)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv.ListenSocket(ctx, sl)

	return srv.Run(ctx)
}

func runAttach(cfg config.Config, bindings []keys.Binding) error {
	f, _ := logger.Init("client")
	if f != nil {
		defer f.Close()
	}
	slog.Info("attaching wideboi client to socket", "socketPath", cfg.Socket)

	conn, err := net.Dial("unix", cfg.Socket)
	if err != nil {
		return fmt.Errorf("no wideboi server running at %s (start one with 'wideboi server'): %w", cfg.Socket, err)
	}

	t := uv.DefaultTerminal()
	scr := t.Screen()
	scr.EnterAltScreen()
	enableMouse(scr, cfg)
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

	cli := client.NewClient(cConn, width, height, cfg.PrefixLabel)
	cli.SetBindings(bindings)
	cli.SetDetachable(true)
	rt := &router{prefix: cfg.Prefix, detachable: true, bindings: bindings}

	cli.Attach(ctx)

	frame := time.NewTicker(16 * time.Millisecond)
	defer frame.Stop()

	var dirtyFrames int
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
				cli.ClearSelection()
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

			case uv.MouseEvent:
				if text := cli.HandleMouse(ctx, ev); text != "" {
					screenLock.Lock()
					writeClipboard(scr, text)
					screenLock.Unlock()
				}
			}

		case <-frame.C:
			screenLock.Lock()
			if cli.Draw(scr, nil, nil) {
				dirtyFrames = 2
			}
			if dirtyFrames > 0 {
				dirtyFrames--
				present(scr)
			}
			screenLock.Unlock()
		}
	}
}

// enableMouse asks the host terminal to report presses, releases and
// drags in SGR encoding, unless config turned the mouse off.
//
// Drag tracking (DEC 1002), not all-motion (1003): wideboi only needs
// motion while a button is held, and all-motion would send an event for
// every cell the pointer crosses. Terminal.Stop's Reset turns tracking
// back off, so teardown needs nothing extra.
func enableMouse(scr *uv.TerminalScreen, cfg config.Config) {
	if !cfg.MouseEnabled {
		return
	}
	scr.SetMouseMode(uv.MouseModeDrag)
	scr.SetMouseEncoding(uv.MouseEncodingSGR)
}

// writeClipboard sets the host clipboard over OSC 52, which reaches the
// terminal the user is sitting at even through SSH -- unlike shelling
// out to pbcopy, which would copy on whichever machine wideboi runs on.
//
// Written straight to the screen's output rather than into a cell:
// ultraviolet drops OSC sequences from cell content deliberately,
// because a cell is repainted on every change and a clipboard write
// must fire exactly once. screenLock must be held.
func writeClipboard(scr *uv.TerminalScreen, text string) {
	_, _ = scr.WriteString(ansi.SetSystemClipboard(text))
	_ = scr.Flush()
}

func run(cfg config.Config, bindings []keys.Binding) error {
	if conn, err := net.Dial("unix", cfg.Socket); err == nil {
		conn.Close()
		return runAttach(cfg, bindings)
	}

	cwd, _ := os.Getwd()

	t := uv.DefaultTerminal()
	scr := t.Screen()
	scr.EnterAltScreen()
	enableMouse(scr, cfg)
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
	srv := server.NewServer(tp, cfg.Shell, cwd)
	srv.SetLayout(cfg.LayoutMode)
	if len(cfg.WidthPresets) > 0 {
		srv.SetWidthPresets(cfg.WidthPresets)
	}

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

	cli := client.NewClient(tp, width, height, cfg.PrefixLabel)
	cli.SetBindings(bindings)
	rt := &router{prefix: cfg.Prefix, bindings: bindings}

	go func() {
		_ = srv.Run(ctx)
	}()

	cli.Attach(ctx)

	frame := time.NewTicker(16 * time.Millisecond)
	defer frame.Stop()

	var dirtyFrames int
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
				cli.ClearSelection()
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

			case uv.MouseEvent:
				if text := cli.HandleMouse(ctx, ev); text != "" {
					screenLock.Lock()
					if !stopped.Load() {
						writeClipboard(scr, text)
					}
					screenLock.Unlock()
				}
			}

		case <-frame.C:
			screenLock.Lock()
			if !stopped.Load() {
				if cli.Draw(scr, srv.DrawPane, srv.CursorInfo) {
					dirtyFrames = 2
				}
				if dirtyFrames > 0 {
					dirtyFrames--
					present(scr)
				}
			}
			screenLock.Unlock()
		}
	}
}
