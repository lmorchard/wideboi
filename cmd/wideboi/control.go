package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/lmorchard/wideboi/internal/config"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

// errRPCTimeout is rpcQuery giving up on a response.
var errRPCTimeout = errors.New("timeout waiting for server response")

// rpcQuery connects to the running session server, sends a client message, and waits
// for a matching server response type within the timeout. A timeout of zero or
// less waits indefinitely.
func rpcQuery[Resp any](cfg config.Config, req transport.ClientMessage, timeout time.Duration) (Resp, error) {
	var zero Resp
	conn, err := net.Dial("unix", cfg.Socket)
	if err != nil {
		return zero, fmt.Errorf("no wideboi server running at %s: %w", cfg.Socket, err)
	}
	if err := handshakeServer(conn, cfg.Socket); err != nil {
		return zero, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cc := transport.NewClientSocketConn(conn, 256)
	cc.RunPumps(ctx)
	return rpcOn[Resp](ctx, cc, req, timeout)
}

// rpcOn sends req on an already-handshaken connection and waits for a
// response of type Resp, skipping broadcasts. A timeout of zero or less
// waits indefinitely.
func rpcOn[Resp any](ctx context.Context, cc *transport.ClientSocketConn, req transport.ClientMessage, timeout time.Duration) (Resp, error) {
	var zero Resp
	if !cc.SendClient(ctx, req) {
		return zero, fmt.Errorf("failed to send request to server")
	}

	var timeoutC <-chan time.Time
	if timeout > 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		timeoutC = timer.C
	}

	for {
		select {
		case msg, ok := <-cc.ServerSendChan():
			if !ok {
				return zero, fmt.Errorf("server closed connection before sending response")
			}
			if resp, ok := msg.(Resp); ok {
				return resp, nil
			}
		case <-timeoutC:
			return zero, errRPCTimeout
		}
	}
}

// connectOrSpawn returns a handshaken connection to the session's server,
// starting one in the background if none answers. spawned reports whether
// conn is the owner connection of a server this call started, which the
// caller must detach from (or shut down) when done.
func connectOrSpawn(cfg config.Config, serverFlags []string) (conn net.Conn, spawned bool, err error) {
	if conn, err := net.Dial("unix", cfg.Socket); err == nil {
		return conn, false, handshakeServer(conn, cfg.Socket)
	}
	conn, exited, err := spawnServer(cfg.Socket, serverFlags)
	if err != nil {
		return nil, false, err
	}
	// The handshake is the readiness signal: the server binds its
	// listener before it says hello (see runServer).
	if _, err := transport.Handshake(conn); err != nil {
		conn.Close()
		if errors.Is(err, io.EOF) {
			switch serverExitCode(exited, reapCeiling) {
			case exitSessionTaken:
				// Another process started this session between our dial
				// and our server's bind (#86); use theirs.
				conn, err := dialWithin(cfg.Socket, takenCeiling)
				if err != nil {
					return nil, false, fmt.Errorf("another wideboi took the session at %s first, but it did not answer within %s: %w",
						cfg.Socket, takenCeiling, err)
				}
				return conn, false, handshakeServer(conn, cfg.Socket)
			case -1:
			default:
				return nil, false, startupExitError(cfg.Socket)
			}
		}
		return nil, false, describeHandshakeErr(cfg.Socket, err, "")
	}
	return conn, true, nil
}

func applySessionFlags(cfg *config.Config, session, socket string) {
	if session != "" {
		cfg.Session = session
		cfg.Socket = config.SessionSocketPath(session)
	} else if socket != "" {
		cfg.Socket = socket
		cfg.Session = ""
	}
}

// reorderFlags separates flags and positional operands so flags placed after operands
// (e.g. `wideboi send 1 text -e` or `wideboi capture 1 -S`) are honored by flag.FlagSet,
// while preserving literal text placed after `--`.
func reorderFlags(args []string) []string {
	var flags []string
	var operands []string
	afterDashDash := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if afterDashDash {
			operands = append(operands, arg)
			continue
		}
		if arg == "--" {
			afterDashDash = true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
			if (arg == "-L" || arg == "-session" || arg == "--session" ||
				arg == "-s" || arg == "-socket" || arg == "--socket" ||
				arg == "-n" || arg == "-lines" || arg == "--lines" ||
				arg == "-cwd" || arg == "--cwd" ||
				arg == "-after" || arg == "--after" ||
				arg == "-timeout" || arg == "--timeout") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				flags = append(flags, args[i])
			}
		} else {
			operands = append(operands, arg)
		}
	}
	return append(flags, operands...)
}

// runSplit creates a new pane and prints its pane ID to stdout. If no
// server is running it starts one, passing it globalArgs (the flags given
// before the subcommand) so it resolves the same session.
func runSplit(cfg config.Config, globalArgs []string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("split", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var cwd string
	var after int
	var keep bool
	var session, socket string

	fs.StringVar(&cwd, "cwd", "", "working directory for new pane")
	fs.BoolVar(&keep, "keep", false, "keep the pane, screen and exit code, after its process exits")
	fs.IntVar(&after, "after", 0, "insert pane after specified pane ID")
	fs.StringVar(&session, "L", "", "session name")
	fs.StringVar(&session, "session", "", "session name")
	fs.StringVar(&socket, "s", "", "unix domain socket path")
	fs.StringVar(&socket, "socket", "", "unix domain socket path")

	// Not reorderFlags: split's flags end where the command begins, so
	// `split --keep make -j4 test` keeps its -j4 for make. (Go's flag
	// parsing stops at the first non-flag, and at --.)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	applySessionFlags(&cfg, session, socket)

	var command string
	if len(fs.Args()) > 0 {
		command = shellJoin(fs.Args())
	}

	req := protocol.MsgSplitRequest{
		Command:     command,
		Cwd:         cwd,
		AfterPaneID: after,
		Keep:        keep,
	}

	serverFlags := append([]string(nil), globalArgs...)
	if session != "" {
		serverFlags = append(serverFlags, "-L", session)
	} else if socket != "" {
		serverFlags = append(serverFlags, "-s", socket)
	}
	conn, spawned, err := connectOrSpawn(cfg, serverFlags)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cc := transport.NewClientSocketConn(conn, 256)
	cc.RunPumps(ctx)

	resp, err := rpcOn[protocol.MsgSplitResponse](ctx, cc, req, 5*time.Second)
	if spawned {
		// Hand the session to itself: detached, it lives until its last
		// pane closes. A server started for a split that failed has
		// nothing to live for, so it goes too.
		if err == nil && resp.Error == "" {
			// Unacknowledged, our exit reads as the owner leaving without
			// detaching, which ends the session and the pane just made.
			if !hangUp(ctx, cc, protocol.MsgDetach{}, detachCeiling) {
				return fmt.Errorf("started a wideboi server at %s but could not detach from it; its session ends with this command", cfg.Socket)
			}
		} else {
			hangUp(ctx, cc, protocol.MsgShutdown{}, shutdownCeiling)
		}
	}
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return errors.New(resp.Error)
	}

	fmt.Fprintf(stdout, "%d\n", resp.PaneID)
	return nil
}

// shellJoin turns split's operands into the string handed to $SHELL -c.
// One operand is already a shell command and passes through verbatim;
// several are argv, so each is quoted and the words survive intact.
func shellJoin(args []string) string {
	if len(args) == 1 {
		return args[0]
	}
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellQuote(a)
	}
	return strings.Join(quoted, " ")
}

// shellQuote single-quotes s for a POSIX shell. Inside single quotes
// nothing is special except the quote itself, which is closed, escaped,
// and reopened.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// runSend sends input text to a pane.
func runSend(cfg config.Config, args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var enter bool
	var session, socket string

	fs.BoolVar(&enter, "enter", false, "append Enter (carriage return)")
	fs.BoolVar(&enter, "e", false, "append Enter (carriage return)")
	fs.StringVar(&session, "L", "", "session name")
	fs.StringVar(&session, "session", "", "session name")
	fs.StringVar(&socket, "s", "", "unix domain socket path")
	fs.StringVar(&socket, "socket", "", "unix domain socket path")

	if err := fs.Parse(reorderFlags(args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	applySessionFlags(&cfg, session, socket)

	rest := fs.Args()
	if len(rest) != 2 {
		return fmt.Errorf("usage: wideboi send [flags] <pane-id> <text> (quote text containing spaces)")
	}

	paneID, err := strconv.Atoi(rest[0])
	if err != nil {
		return fmt.Errorf("invalid pane id %q: %w", rest[0], err)
	}

	data := []byte(rest[1])
	if enter {
		data = append(data, '\r')
	}

	req := protocol.MsgSendInputRequest{
		PaneID: paneID,
		Data:   data,
	}

	resp, err := rpcQuery[protocol.MsgSendInputResponse](cfg, req, 5*time.Second)
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return errors.New(resp.Error)
	}

	return nil
}

// runCapture reads terminal text from a pane.
func runCapture(cfg config.Config, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("capture", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var scrollback bool
	var lines int
	var session, socket string

	fs.BoolVar(&scrollback, "scrollback", false, "include scrollback history")
	fs.BoolVar(&scrollback, "S", false, "include scrollback history")
	fs.IntVar(&lines, "lines", 0, "limit output to last N lines")
	fs.IntVar(&lines, "n", 0, "limit output to last N lines")
	fs.StringVar(&session, "L", "", "session name")
	fs.StringVar(&session, "session", "", "session name")
	fs.StringVar(&socket, "s", "", "unix domain socket path")
	fs.StringVar(&socket, "socket", "", "unix domain socket path")

	if err := fs.Parse(reorderFlags(args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	applySessionFlags(&cfg, session, socket)

	rest := fs.Args()
	if len(rest) < 1 {
		return fmt.Errorf("usage: wideboi capture [flags] <pane-id>")
	}

	paneID, err := strconv.Atoi(rest[0])
	if err != nil {
		return fmt.Errorf("invalid pane id %q: %w", rest[0], err)
	}

	req := protocol.MsgCaptureRequest{
		PaneID:     paneID,
		Scrollback: scrollback,
		Lines:      lines,
	}

	resp, err := rpcQuery[protocol.MsgCaptureResponse](cfg, req, 5*time.Second)
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return errors.New(resp.Error)
	}

	fmt.Fprint(stdout, resp.Text)
	return nil
}

// runClose closes a pane using hangup semantics.
func runClose(cfg config.Config, args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("close", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var session, socket string

	fs.StringVar(&session, "L", "", "session name")
	fs.StringVar(&session, "session", "", "session name")
	fs.StringVar(&socket, "s", "", "unix domain socket path")
	fs.StringVar(&socket, "socket", "", "unix domain socket path")

	if err := fs.Parse(reorderFlags(args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	applySessionFlags(&cfg, session, socket)

	rest := fs.Args()
	if len(rest) < 1 {
		return fmt.Errorf("usage: wideboi close [flags] <pane-id>")
	}

	paneID, err := strconv.Atoi(rest[0])
	if err != nil {
		return fmt.Errorf("invalid pane id %q: %w", rest[0], err)
	}

	req := protocol.MsgClosePaneRequest{
		PaneID: paneID,
	}

	resp, err := rpcQuery[protocol.MsgClosePaneResponse](cfg, req, 5*time.Second)
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return errors.New(resp.Error)
	}

	return nil
}

// exitWaitTimeout is timeout(1)'s code for "gave up waiting".
const exitWaitTimeout = 124

// runWait blocks until a pane's process exits and returns its exit code,
// which main exits with.
func runWait(cfg config.Config, args []string, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("wait", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var timeout time.Duration
	var session, socket string

	fs.DurationVar(&timeout, "timeout", 0, "give up after this long (exit 124); default waits forever")
	fs.StringVar(&session, "L", "", "session name")
	fs.StringVar(&session, "session", "", "session name")
	fs.StringVar(&socket, "s", "", "unix domain socket path")
	fs.StringVar(&socket, "socket", "", "unix domain socket path")

	if err := fs.Parse(reorderFlags(args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, nil
		}
		return 0, err
	}
	applySessionFlags(&cfg, session, socket)

	rest := fs.Args()
	if len(rest) != 1 {
		return 0, fmt.Errorf("usage: wideboi wait [--timeout <duration>] <pane-id>")
	}
	paneID, err := strconv.Atoi(rest[0])
	if err != nil {
		return 0, fmt.Errorf("invalid pane id %q: %w", rest[0], err)
	}

	resp, err := rpcQuery[protocol.MsgWaitResponse](cfg, protocol.MsgWaitRequest{PaneID: paneID}, timeout)
	if errors.Is(err, errRPCTimeout) {
		fmt.Fprintf(stderr, "wideboi: timed out after %s waiting for pane %d\n", timeout, paneID)
		return exitWaitTimeout, nil
	}
	if err != nil {
		return 0, err
	}
	if resp.Error != "" {
		return 0, errors.New(resp.Error)
	}
	return resp.ExitCode, nil
}
