// Command wideboi is a scrolling tiling terminal multiplexer for CLI agents.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
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

// exitSessionTaken is `wideboi server`'s status when another server
// already holds the session. A spawning plain wideboi reads it to
// attach instead of reporting a failure (#86). 1 is any other error;
// 128+n is a signal.
const exitSessionTaken = 3

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
	// ownerFD is the inherited connection a spawning plain wideboi owns
	// this server through, or -1. Internal: see spawnServer.
	ownerFD int
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
		if opts.subcommand == "" && (arg == "server" || arg == "attach" || arg == "kill-session" || arg == "cleanup" || arg == "version" || arg == "help") {
			opts.subcommand = arg
			continue
		}
		if opts.subcommand == "" && (arg == "ls" || arg == "list-sessions") {
			opts.subcommand = "ls"
			continue
		}
		flagArgs = append(flagArgs, arg)
		if arg == "-c" || arg == "-config" || arg == "--config" ||
			arg == "-l" || arg == "-layout" || arg == "--layout" ||
			arg == "-p" || arg == "-prefix" || arg == "--prefix" ||
			arg == "-s" || arg == "-socket" || arg == "--socket" ||
			arg == "-L" || arg == "-session" || arg == "--session" ||
			arg == "-shell" || arg == "--shell" ||
			arg == "-owner-fd" || arg == "--owner-fd" {
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
	fs.StringVar(&opts.flags.Session, "L", "", "session name")
	fs.StringVar(&opts.flags.Session, "session", "", "session name")
	fs.StringVar(&opts.flags.Websocket, "websocket", "", "address for websocket server (e.g. \":8080\")")
	fs.StringVar(&opts.flags.Shell, "shell", "", "shell executable path")
	fs.IntVar(&opts.ownerFD, "owner-fd", -1, "internal: inherited owner connection")
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
  wideboi [flags]            Start a session in the background and attach to it,
                             or attach to the one already running
  wideboi [flags] server     Start a background server listening on socket
  wideboi [flags] attach     Attach a client to a running server
  wideboi [flags] kill-session
                             End the session: close every pane and stop the server
  wideboi cleanup            Remove logs and sockets from dead sessions
  wideboi ls                 List running sessions (alias: list-sessions)
  wideboi version            Display version information
  wideboi help               Show this help text

Flags:
  -c, --config <path>    Path to TOML configuration file
                         (default: $XDG_CONFIG_HOME/wideboi/config.toml
                          or ~/.config/wideboi/config.toml)
  -l, --layout <mode>    Starting layout for this client: "cards" (default) or "scroll"
  -p, --prefix <key>     Control mode prefix key: "ctrl+<letter>" or "ctrl+space"
                         (default: "ctrl+b")
  -L, --session <name>   Session to start or attach to (default: "default");
                         its socket is $TMPDIR/wideboi-<uid>/<name>.sock
  -s, --socket <path>    Unix domain socket path, instead of a session name
      --websocket <addr> Address for WebSocket server (e.g. ":8080")
      --shell <path>     Shell executable to launch in panes
                         (default: $SHELL or /bin/sh)
  -v, --version          Print version and exit
  -h, --help             Show this help text and exit

Environment Variables:
  WIDEBOI_LAYOUT         Starting layout for this client ("cards" or "scroll")
  WIDEBOI_PREFIX         Prefix key override (e.g. "ctrl+b")
  WIDEBOI_SESSION        Session name override
  WIDEBOI_WEBSOCKET      Address for WebSocket server (e.g. ":8080")
  WIDEBOI_SOCK           Socket path override
  WIDEBOI_SHELL          Shell path override
  WIDEBOI_LOG_LEVEL      Log verbosity: trace, debug, info (default), warn, error
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
		if err := runServer(cfg, opts.ownerFD); errors.Is(err, transport.ErrSessionTaken) {
			fmt.Fprintln(os.Stderr, "wideboi:", err)
			os.Exit(exitSessionTaken)
		} else {
			fatal(err)
		}
	case "attach":
		fatal(runAttach(cfg, bindings))
	case "kill-session":
		fatal(runKillSession(cfg))
	case "cleanup":
		fatal(runCleanup(os.Stdout, config.SessionDir()))
	case "ls":
		fatal(runList(os.Stdout))
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

// runServer serves the session on cfg.Socket. ownerFD, when not -1, is
// the inherited connection of the plain wideboi that spawned this
// server and owns the session; see spawnServer.
func runServer(cfg config.Config, ownerFD int) error {
	f, _ := logger.Init(logger.Path(cfg.Socket, "server"), cfg.LogLevel, true)
	if f != nil {
		defer f.Close()
	}
	slog.Info("starting wideboi server", "socketPath", cfg.Socket, "ownerFD", ownerFD)

	cwd, _ := os.Getwd()

	// Bound before anything else, so a lost race exits before a single
	// pane spawns. Logged as well as returned: a spawned server's
	// stderr is /dev/null.
	sl, err := transport.NewSocketListener(cfg.Socket)
	if err != nil {
		slog.Error("cannot listen", "err", err)
		return err
	}
	defer sl.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// A literally-nil interface when there is no owner: a nil
	// *ServerSocketConn stored in it would not compare equal to nil,
	// and NewServer would register it as a client.
	var ownerConn transport.Transport
	if ownerFD >= 0 {
		of := os.NewFile(uintptr(ownerFD), "wideboi-owner")
		conn, err := net.FileConn(of)
		// FileConn dups the fd with close-on-exec set. The original
		// has it cleared -- that is how it got here -- so left open
		// it would leak into every pane this server spawns.
		of.Close()
		if err != nil {
			slog.Error("owner connection", "fd", ownerFD, "err", err)
			return fmt.Errorf("owner connection on fd %d: %w", ownerFD, err)
		}
		sc := transport.NewServerSocketConn(conn, 256)
		sc.RunPumps(ctx)
		ownerConn = sc
	}

	srv := server.NewServer(ownerConn, cfg.Shell, cwd)
	if ownerConn != nil {
		srv.SetOwner(ownerConn)
	}
	if len(cfg.WidthPresets) > 0 {
		srv.SetWidthPresets(cfg.WidthPresets)
	}

	// The server is responsible for its panes, and there is no
	// terminal to restore: teardown is the whole job. Without this,
	// SIGTERM took Go's default disposition and srv.Close never ran,
	// which left the socket file behind.
	var signalled atomic.Bool
	guard := hostterm.NewGuard(func() error {
		signalled.Store(true)
		err := srv.Close()
		// The re-raise skips defers, and a socket nobody answers is
		// litter the next server has to step over.
		_ = sl.Close()
		return err
	})
	defer guard.Stop()
	guard.Arm(syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)

	srv.ListenSocket(ctx, sl)

	var httpSrv *http.Server
	if cfg.Websocket != "" {
		mux := http.NewServeMux()
		srv.ListenWebSocket(ctx, mux)

		httpSrv = &http.Server{
			Handler: mux,
		}

		wsListener, err := net.Listen("tcp", cfg.Websocket)
		if err != nil {
			slog.Error("cannot listen on websocket address", "err", err)
			return err
		}

		go func() {
			fmt.Fprintf(os.Stderr, "wideboi: websocket server listening at ws://%s/ws\n", cfg.Websocket)
			slog.Info("websocket server listening", "addr", cfg.Websocket)
			if err := httpSrv.Serve(wsListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
				slog.Error("websocket server failed", "err", err)
			}
		}()
	}

	err = srv.Run(ctx)
	if httpSrv != nil {
		_ = httpSrv.Shutdown(context.Background())
	}

	// Run returns as soon as Close begins, and the guard's Close is
	// one way that happens. Let it finish and re-raise rather than
	// exit 0 underneath it. (Before the deferred Stop runs, signalled
	// can only have been set by a signal.)
	if signalled.Load() {
		_ = guard.Stop()
		time.Sleep(signalExitMargin)
	}
	return err
}

// runKillSession ends the session at cfg.Socket and waits until it has:
// the server hangs up only once every pane is reaped.
func runKillSession(cfg config.Config) error {
	conn, err := net.Dial("unix", cfg.Socket)
	if err != nil {
		return fmt.Errorf("no wideboi server running at %s: %w", cfg.Socket, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cc := transport.NewClientSocketConn(conn, 256)
	cc.RunPumps(ctx)
	if !hangUp(ctx, cc, protocol.MsgShutdown{}, shutdownCeiling) {
		return fmt.Errorf("wideboi server at %s did not shut down within %s", cfg.Socket, shutdownCeiling)
	}
	return nil
}

func runAttach(cfg config.Config, bindings []keys.Binding) error {
	conn, err := net.Dial("unix", cfg.Socket)
	if err != nil {
		return fmt.Errorf("no wideboi server running at %s (start one with 'wideboi server'): %w", cfg.Socket, err)
	}
	return runClient(cfg, bindings, conn, nil)
}

// run is plain `wideboi`: attach to the session on cfg.Socket if there
// is one, otherwise start one in the background and own it.
func run(cfg config.Config, bindings []keys.Binding) error {
	if conn, err := net.Dial("unix", cfg.Socket); err == nil {
		return runClient(cfg, bindings, conn, nil)
	}
	conn, exited, err := spawnServer(os.Args[1:])
	if err != nil {
		return err
	}
	err = runClient(cfg, bindings, conn, exited)
	if !errors.Is(err, errSessionTaken) {
		return err
	}
	// Another wideboi started this session between our dial and our
	// server's bind (#86). Join it: the user asked for a wideboi.
	slog.Info("another server took the session first; attaching to it", "socketPath", cfg.Socket)
	conn, err = dialWithin(cfg.Socket, takenCeiling)
	if err != nil {
		return fmt.Errorf("another wideboi took the session at %s first, but it did not answer within %s: %w",
			cfg.Socket, takenCeiling, err)
	}
	return runClient(cfg, bindings, conn, nil)
}

// errSessionTaken is runClient's report that the server it spawned
// found the session already held; run attaches to that one instead.
var errSessionTaken = errors.New("session taken")

// takenCeiling bounds the wait for the winning server to listen. It
// holds the lock before it binds, so it is normally milliseconds away.
const takenCeiling = 5 * time.Second

// reapCeiling bounds the wait for a spawned server's exit code once
// its owner connection has closed; the reap follows the close promptly.
const reapCeiling = 2 * time.Second

// dialWithin dials socket until it answers or ceiling passes: a wait
// for observed state, with the ceiling as the timeout.
func dialWithin(socket string, ceiling time.Duration) (net.Conn, error) {
	deadline := time.Now().Add(ceiling)
	for {
		conn, err := net.Dial("unix", socket)
		if err == nil || time.Now().After(deadline) {
			return conn, err
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// serverExitCode waits up to ceiling for a spawned server's exit code;
// -1 if it does not arrive.
func serverExitCode(exited <-chan int, ceiling time.Duration) int {
	select {
	case code := <-exited:
		return code
	case <-time.After(ceiling):
		return -1
	}
}

// runClient drives the host terminal for a session reached over conn.
//
// serverExit is non-nil for the plain wideboi that spawned the session,
// and delivers that server's exit code (see spawnServer). This process
// then owns the session, which ends with it -- on a quit, a signal, or
// a death that runs no code at all -- unless it detached first.
func runClient(cfg config.Config, bindings []keys.Binding, conn net.Conn, serverExit <-chan int) error {
	owner := serverExit != nil
	f, _ := logger.Init(logger.Path(cfg.Socket, "client"), cfg.LogLevel, false)
	if f != nil {
		defer f.Close()
	}
	slog.Info("wideboi client starting", "socketPath", cfg.Socket, "owner", owner)

	t := uv.DefaultTerminal()
	scr := t.Screen()

	var (
		screenLock sync.Mutex
		// cConnLock serializes teardown/reconnect swaps of cConn
		cConnLock sync.Mutex
		// stopped: a signal's teardown has begun, so stop drawing.
		stopped atomic.Bool
		// hungUp: the connection is over, so there is nothing left
		// to tell the server.
		hungUp atomic.Bool
		// started: the terminal is initialized and in alt-screen.
		started atomic.Bool
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cConn := transport.NewClientSocketConn(conn, 256)
	cConn.RunPumps(ctx)

	// The guard restores the terminal on every exit path, a signal
	// included; before this, SIGTERM to an attached client left the
	// host terminal in the alt screen. Deferred after cancel so it
	// runs first, while ctx is still live.
	guard := hostterm.NewGuard(func() error {
		stopped.Store(true)
		// An owner's session dies with it, unless the connection has
		// already ended -- a detach, a quit, or the server hanging up.
		// Shut it down *before* restoring the terminal, and wait: the
		// panes are then hung up before this process re-raises, which
		// is the order verify-exit asserts. If the ceiling passes,
		// restore and exit anyway; the server still sees our EOF with
		// no detach before it, and ends the session itself.
		var late bool
		if owner && !hungUp.Load() {
			cConnLock.Lock()
			currentConn := cConn
			cConnLock.Unlock()
			late = !hangUp(ctx, currentConn, protocol.MsgShutdown{}, shutdownCeiling)
		}

		var err error
		screenLock.Lock()
		defer screenLock.Unlock()
		if started.Load() {
			scr.ExitAltScreen()
			_ = scr.Flush()
			err = t.Stop()
		}

		if late {
			fmt.Fprintf(os.Stderr, "wideboi: server did not confirm shutdown within %s\n", shutdownCeiling)
		}
		return err
	})
	defer guard.Stop()
	guard.Arm(syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)

	// awaitReRaise is for every return below. If a signal's teardown is
	// running on the guard's goroutine, it re-raises when it finishes;
	// returning first would exit 0 underneath it, so the parent would
	// see the wrong status.
	awaitReRaise := func() {
		if stopped.Load() {
			_ = guard.Stop()
			time.Sleep(signalExitMargin)
		}
	}

	width, height, termErr := t.GetSize()
	if termErr != nil || width <= 0 || height <= 0 {
		width, height = 80, 24
	}

	cli := client.NewClient(cConn, width, height, cfg.PrefixLabel)
	cli.SetLayoutMode(cfg.LayoutMode)
	cli.SetBindings(bindings)
	cli.SetDetachable(true)
	rt := &router{prefix: cfg.Prefix, detachable: true, bindings: bindings}

	cli.Attach(ctx)

	var frame *time.Ticker
	defer func() {
		if frame != nil {
			frame.Stop()
		}
	}()

	var (
		// gotMsg: the server has said anything at all. An owner's
		// connection that ends before it did means the server we
		// spawned never got going.
		gotMsg bool
	)
	var events <-chan uv.Event
	var frameC <-chan time.Time

	for {
		cConnLock.Lock()
		currentConn := cConn
		cConnLock.Unlock()

		select {
		case msg, ok := <-currentConn.ServerSendChan():
			if !ok {
				hungUp.Store(true)
				awaitReRaise()
				// The stream ended. Distinguish a server that shut
				// down or was detached from cleanly -- both ordinary,
				// both exit 0 -- from a protocol failure, which used
				// to look exactly the same from here and so hid a
				// defect that killed every session on the first
				// coloured cell a child printed.
				if err := currentConn.Err(); err != nil {
					return fmt.Errorf("connection to wideboi server failed: %w", err)
				}
				if owner && !gotMsg {
					if serverExitCode(serverExit, reapCeiling) == exitSessionTaken {
						return errSessionTaken
					}
					return fmt.Errorf("wideboi server exited during startup; see %s", logger.Path(cfg.Socket, "server"))
				}
				if owner {
					slog.Info("server closed the connection")
					return nil
				}

				// The server is gone but we didn't ask to detach, and we don't own it.
				// It might be a network drop or a server restart. Try to reconnect.
				slog.Info("connection dropped, attempting to reconnect...")
				hungUp.Store(false) // Not permanently hung up yet

				reconnectConn, err := dialWithin(cfg.Socket, 2*time.Second)
				if err != nil {
					slog.Info("reconnect failed", "err", err)
					return nil // Just exit cleanly if the server is truly gone
				}

				slog.Info("reconnected successfully")

				// Close the old transport pumps and swap in the new one.
				// The transport handles stopping its own writeLoop when Close is called.
				currentConn.Close()

				cConnLock.Lock()
				cConn = transport.NewClientSocketConn(reconnectConn, 256)
				cConn.RunPumps(ctx)
				cli.SetTransport(cConn)
				cConnLock.Unlock()

				cli.Attach(ctx)
				continue
			}
			if !gotMsg {
				gotMsg = true
				screenLock.Lock()
				if !stopped.Load() {
					scr.EnterAltScreen()
					enableMouse(scr, cfg)
					if err := t.Start(); err != nil {
						scr.ExitAltScreen()
						_ = scr.Flush()
						_ = t.Stop()
						screenLock.Unlock()
						return fmt.Errorf("start terminal: %w", err)
					}
					started.Store(true)
					events = t.Events()
					frame = time.NewTicker(16 * time.Millisecond)
					frameC = frame.C
				}
				screenLock.Unlock()
			}
			cli.HandleServerMsg(msg)

		case ev := <-events:
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
				case routeDetach:
					// Leaves the server and its children running.
					// Waiting for the hang-up makes sure the detach
					// was read before our socket closes.
					slog.Info("client detaching")
					cConnLock.Lock()
					hConn := cConn
					cConnLock.Unlock()
					if !hangUp(ctx, hConn, protocol.MsgDetach{}, detachCeiling) {
						slog.Warn("server did not acknowledge the detach", "ceiling", detachCeiling)
					}
					hungUp.Store(true)
					awaitReRaise()
					// Restore the terminal first, so the notice lands in
					// the scrollback rather than in the alt screen the
					// restore wipes. The deferred Stop is then a no-op.
					// Sampled before Stop, whose teardown sets stopped
					// itself: only a signal's teardown means the process
					// is about to die rather than detach.
					signalled := stopped.Load()
					_ = guard.Stop()
					if !signalled {
						printDetachNotice(os.Stdout, cfg.Socket)
					}
					return nil
				case routeQuit:
					slog.Info("client ending the session")
					acked := hangUp(ctx, cConn, protocol.MsgShutdown{}, shutdownCeiling)
					// Set either way. Unacknowledged, the teardown
					// would otherwise send a second shutdown and wait
					// out the whole ceiling again; our EOF, with no
					// detach before it, ends an owned session anyway.
					hungUp.Store(true)
					awaitReRaise()
					if !acked {
						return fmt.Errorf("wideboi server did not shut down within %s", shutdownCeiling)
					}
					return nil
				case routeVerb:
					cli.SendVerb(ctx, act.Verb)
				case routeToggleLayout:
					cli.ToggleLayout()
				case routeScroll:
					cli.SendScroll(ctx, act.Scroll)
				case routeFocusColumn:
					cli.FocusColumn(ctx, act.Column)
				case routeForward:
					cli.SendKey(ctx, uv.KeyEvent(ev))
				case routeIgnore:
				}
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

		case <-frameC:
			screenLock.Lock()
			if !stopped.Load() {
				if cli.Draw(scr) {
					present(scr)
				}
			}
			screenLock.Unlock()
		}
	}
}

// printDetachNotice tells a user who just detached that the session did
// not end with the screen: it is still running, and this is where. The
// commands name the session the way the user would: nothing for the
// default, so the common case stays short; -L for another named
// session; -s for a socket anywhere else.
func printDetachNotice(w io.Writer, socket string) {
	target := " -s " + socket
	if name, ok := config.SessionName(socket); ok {
		target = " -L " + name
		if name == "default" {
			target = ""
		}
	}
	fmt.Fprintf(w, "[wideboi detached; the session is still running at %s]\n", socket)
	fmt.Fprintf(w, "[reattach: wideboi%s   end it: wideboi%s kill-session]\n", target, target)
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
