package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/lmorchard/wideboi/internal/commands"
	"github.com/lmorchard/wideboi/internal/config"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

// errRPCTimeout is rpcQuery giving up on a response.
var errRPCTimeout = commands.ErrRPCTimeout

// rpcQuery connects to the running session server, sends a client message, and waits
// for a matching server response type within the timeout. A timeout of zero or
// less waits indefinitely.
func rpcQuery[Resp any](cfg config.Config, req transport.ClientMessage, timeout time.Duration) (Resp, error) {
	return commands.RPCQuery[Resp](context.Background(), commands.Invocation{Cfg: cfg}, req, timeout)
}

// rpcOn sends req on an already-handshaken connection and waits for a
// response of type Resp, skipping broadcasts. A timeout of zero or less
// waits indefinitely.
func rpcOn[Resp any](ctx context.Context, cc *transport.ClientSocketConn, req transport.ClientMessage, timeout time.Duration) (Resp, error) {
	return commands.RPCOn[Resp](ctx, cc, req, timeout)
}

// connectOrSpawn returns a handshaken connection to the session's server,
// starting one in the background if none answers. spawned reports whether
// conn is the owner connection of a server this call started, which the
// caller must detach from (or shut down) when done.
func connectOrSpawn(cfg config.Config, serverFlags []string) (conn net.Conn, spawned bool, err error) {
	if conn, err := net.Dial("unix", cfg.Socket); err == nil {
		return conn, false, handshakeServer(conn, cfg.Socket)
	}
	allowNested := isAllowNested(os.Getenv) || slices.Contains(serverFlags, "--allow-nested")
	if !allowNested && isNestedSession(os.Getenv) {
		return nil, false, errors.New("already running inside a wideboi session (refusing to spawn a nested session; use --allow-nested or WIDEBOI_ALLOW_NESTED=1 to force)")
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

// addTargetFlags registers the standard -L/--session and -s/--socket flags on fs.
func addTargetFlags(fs *flag.FlagSet, session, socket *string) {
	fs.StringVar(session, "L", "", "session name")
	fs.StringVar(session, "session", "", "session name")
	fs.StringVar(socket, "s", "", "unix domain socket path")
	fs.StringVar(socket, "socket", "", "unix domain socket path")
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
				arg == "-limit" || arg == "--limit" ||
				arg == "-offset" || arg == "--offset" ||
				arg == "-o" || arg == "-output" || arg == "--output" ||
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
	addTargetFlags(fs, &session, &socket)

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
		command = commands.ShellJoin(fs.Args())
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
			// detaching. Kept on owner loss (the default), the session
			// survives that anyway; otherwise it ends, and the pane just
			// made with it.
			if !hangUp(ctx, cc, protocol.MsgDetach{}, detachCeiling) && !cfg.KeepSessionOnOwnerLossEnabled {
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

// runSend sends input text to a pane.
func runSend(cfg config.Config, args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var enter bool
	var session, socket string

	fs.BoolVar(&enter, "enter", false, "append Enter (carriage return)")
	fs.BoolVar(&enter, "e", false, "append Enter (carriage return)")
	addTargetFlags(fs, &session, &socket)

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

// preprocessDumpPaneArgs expands `--scrollback [lines]` into `--scrollback -limit [lines]`
// when lines is clearly intended as a line count rather than a target pane operand.
func preprocessDumpPaneArgs(args []string, inSession bool) []string {
	// First pass: identify non-flag integer operands
	var nonFlagInts []int
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			if (arg == "-L" || arg == "-session" || arg == "--session" ||
				arg == "-s" || arg == "-socket" || arg == "--socket" ||
				arg == "-n" || arg == "-lines" || arg == "--lines" ||
				arg == "-limit" || arg == "--limit" ||
				arg == "-offset" || arg == "--offset" ||
				arg == "-o" || arg == "-output" || arg == "--output") && i+1 < len(args) {
				i++
			}
			continue
		}
		if _, err := strconv.Atoi(arg); err == nil {
			nonFlagInts = append(nonFlagInts, i)
		}
	}

	var out []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		// -S is strictly boolean for backward compatibility with `wideboi capture -S <pane-id>`
		if arg == "-scrollback" || arg == "--scrollback" {
			out = append(out, arg)
			if i+1 < len(args) {
				if _, err := strconv.Atoi(args[i+1]); err == nil {
					// Disambiguate: args[i+1] is a line limit if a pane ID was already seen,
					// or follows after this number, or caller pane is known in session.
					if inSession || len(nonFlagInts) > 1 {
						i++
						out = append(out, "-limit", args[i])
					}
				}
			}
			continue
		}
		if strings.HasPrefix(arg, "--scrollback=") || strings.HasPrefix(arg, "-scrollback=") {
			parts := strings.SplitN(arg, "=", 2)
			if _, err := strconv.ParseBool(parts[1]); err == nil {
				// Valid boolean value (--scrollback=false / --scrollback=true)
				out = append(out, arg)
			} else if n, err := strconv.Atoi(parts[1]); err == nil && n > 0 {
				out = append(out, parts[0], "-limit", parts[1])
			} else {
				out = append(out, arg)
			}
			continue
		}
		out = append(out, arg)
	}
	return out
}

// runCapture reads terminal text from a pane (alias for runDumpPane).
func runCapture(cfg config.Config, args []string, stdout, stderr io.Writer) error {
	return runDumpPane(cfg, args, stdout, stderr)
}

// runDumpPane reads terminal text from a pane with optional pagination and ANSI styling.
func runDumpPane(cfg config.Config, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("dump-pane", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var scrollback bool
	var offset int
	var limit int
	var countOnly bool
	var ansi, plain bool
	var output string
	var session, socket string

	fs.BoolVar(&scrollback, "scrollback", false, "include scrollback history")
	fs.BoolVar(&scrollback, "S", false, "include scrollback history")
	fs.IntVar(&offset, "offset", -1, "0-indexed starting line from top of buffer")
	fs.IntVar(&limit, "limit", 0, "maximum lines to return")
	fs.IntVar(&limit, "lines", 0, "maximum lines to return")
	fs.IntVar(&limit, "n", 0, "maximum lines to return")
	fs.BoolVar(&countOnly, "count", false, "query and print total line count only")
	fs.BoolVar(&countOnly, "c", false, "query and print total line count only")
	fs.BoolVar(&ansi, "ansi", false, "preserve ANSI color and style escapes")
	fs.BoolVar(&plain, "plain", false, "strip ANSI formatting (default)")
	fs.StringVar(&output, "output", "", "write output to file instead of stdout")
	fs.StringVar(&output, "o", "", "write output to file instead of stdout")
	addTargetFlags(fs, &session, &socket)

	var callerID int
	if envID := os.Getenv("WIDEBOI_PANE_ID"); envID != "" {
		if id, err := strconv.Atoi(envID); err == nil {
			callerID = id
		}
	}

	preprocessed := preprocessDumpPaneArgs(args, callerID > 0)
	if err := fs.Parse(reorderFlags(preprocessed)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	applySessionFlags(&cfg, session, socket)

	targetID := callerID
	rest := fs.Args()
	if len(rest) > 0 {
		id, err := strconv.Atoi(rest[0])
		if err != nil {
			return fmt.Errorf("invalid pane id %q: %w", rest[0], err)
		}
		targetID = id
	} else if targetID <= 0 {
		return fmt.Errorf("usage: wideboi dump-pane [flags] [pane-id] (pane-id required outside wideboi pane)")
	}

	req := protocol.MsgDumpPaneRequest{
		PaneID:     targetID,
		Scrollback: scrollback,
		ANSI:       ansi && !plain,
		CountOnly:  countOnly,
	}

	if offset < 0 {
		// No offset specified: limit behaves as tailLines
		if limit > 0 {
			req.TailLines = limit
		}
	} else {
		req.Offset = offset
		req.Limit = limit
	}

	resp, err := rpcQuery[protocol.MsgDumpPaneResponse](cfg, req, 5*time.Second)
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return errors.New(resp.Error)
	}

	var outContent string
	if countOnly {
		outContent = fmt.Sprintf("%d\n", resp.TotalLines)
	} else {
		outContent = resp.Text
	}

	if output != "" {
		if err := os.WriteFile(output, []byte(outContent), 0666); err != nil {
			return fmt.Errorf("writing output file %q: %w", output, err)
		}
		return nil
	}

	fmt.Fprint(stdout, outContent)
	return nil
}

// runPipePane streams the raw, unparsed PTY byte stream of a pane to stdout or a file.
func runPipePane(cfg config.Config, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("pipe-pane", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var output string
	var appendMode bool
	var session, socket string

	fs.StringVar(&output, "output", "", "write raw stream to file instead of stdout")
	fs.StringVar(&output, "o", "", "write raw stream to file instead of stdout")
	fs.BoolVar(&appendMode, "append", false, "append to output file instead of truncating")
	fs.BoolVar(&appendMode, "a", false, "append to output file instead of truncating")
	addTargetFlags(fs, &session, &socket)

	if err := fs.Parse(reorderFlags(args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	applySessionFlags(&cfg, session, socket)

	var callerID int
	if envID := os.Getenv("WIDEBOI_PANE_ID"); envID != "" {
		if id, err := strconv.Atoi(envID); err == nil {
			callerID = id
		}
	}

	targetID := callerID
	rest := fs.Args()
	if len(rest) > 0 {
		id, err := strconv.Atoi(rest[0])
		if err != nil {
			return fmt.Errorf("invalid pane id %q: %w", rest[0], err)
		}
		targetID = id
	} else if targetID <= 0 {
		return fmt.Errorf("usage: wideboi pipe-pane [flags] [pane-id] (pane-id required outside wideboi pane)")
	}

	var outWriter io.Writer = stdout
	if output != "" {
		flagVal := os.O_CREATE | os.O_WRONLY
		if appendMode {
			flagVal |= os.O_APPEND
		} else {
			flagVal |= os.O_TRUNC
		}
		f, err := os.OpenFile(output, flagVal, 0666)
		if err != nil {
			return fmt.Errorf("opening output file %q: %w", output, err)
		}
		defer f.Close()
		outWriter = f
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	conn, err := commands.DialServer(ctx, cfg.Socket)
	if err != nil {
		return err
	}
	defer conn.Close()

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()

	req := protocol.MsgPipePaneRequest{
		PaneID: targetID,
	}
	if err := transport.WriteClientFrame(conn, req); err != nil {
		return fmt.Errorf("sending pipe-pane request: %w", err)
	}

	for {
		msg, err := transport.ReadServerFrame(conn)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("reading pipe stream: %w", err)
		}
		resp, ok := msg.(protocol.MsgPipePaneResponse)
		if !ok {
			continue
		}
		if resp.Error != "" {
			return errors.New(resp.Error)
		}
		if len(resp.Data) > 0 {
			if _, err := outWriter.Write(resp.Data); err != nil {
				return err
			}
		}
		if resp.Closed {
			return nil
		}
	}
}

// runClose closes a pane using hangup semantics.
func runClose(cfg config.Config, args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("close", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var session, socket string

	addTargetFlags(fs, &session, &socket)

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

// runRenamePane sets or clears the custom title of a pane.
func runRenamePane(cfg config.Config, args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("rename-pane", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var session, socket string
	addTargetFlags(fs, &session, &socket)

	if err := fs.Parse(reorderFlags(args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	applySessionFlags(&cfg, session, socket)

	rest := fs.Args()
	targetID := 0
	title := ""
	clear := false

	callerPaneStr := os.Getenv("WIDEBOI_PANE_ID")
	callerID := 0
	if callerPaneStr != "" {
		if id, err := strconv.Atoi(callerPaneStr); err == nil && id > 0 {
			callerID = id
		}
	}

	switch len(rest) {
	case 0:
		if callerID <= 0 {
			return fmt.Errorf("usage: wideboi rename-pane [flags] <pane-id> [title] (pane-id required outside wideboi pane)")
		}
		targetID = callerID
		clear = true
	case 1:
		if callerID > 0 {
			targetID = callerID
			title = rest[0]
			if title == "" {
				clear = true
			}
		} else {
			id, err := strconv.Atoi(rest[0])
			if err != nil {
				return fmt.Errorf("usage: wideboi rename-pane [flags] <pane-id> [title] (pane-id required outside wideboi pane)")
			}
			targetID = id
			clear = true
		}
	default:
		id, err := strconv.Atoi(rest[0])
		if err != nil {
			if callerID > 0 {
				targetID = callerID
				title = strings.Join(rest, " ")
			} else {
				return fmt.Errorf("invalid pane id %q: %w", rest[0], err)
			}
		} else {
			targetID = id
			title = strings.Join(rest[1:], " ")
			if title == "" {
				clear = true
			}
		}
	}

	req := protocol.MsgRenamePaneRequest{
		PaneID: targetID,
		Title:  title,
		Clear:  clear,
	}

	resp, err := rpcQuery[protocol.MsgRenamePaneResponse](cfg, req, 5*time.Second)
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return errors.New(resp.Error)
	}

	return nil
}

// runSetPaneStatus sets or clears an explicit status override on a pane.
func runSetPaneStatus(cfg config.Config, args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("set-pane-status", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var session, socket string
	addTargetFlags(fs, &session, &socket)

	if err := fs.Parse(reorderFlags(args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	applySessionFlags(&cfg, session, socket)

	rest := fs.Args()
	callerPaneStr := os.Getenv("WIDEBOI_PANE_ID")
	callerID := 0
	if callerPaneStr != "" {
		if id, err := strconv.Atoi(callerPaneStr); err == nil && id > 0 {
			callerID = id
		}
	}

	if len(rest) == 0 {
		return fmt.Errorf("usage: wideboi set-pane-status [flags] <working|input|done|failed|idle|clear> [pane-id]")
	}

	statusWord := ""
	targetID := callerID

	// Support both: set-pane-status <status> [id] AND set-pane-status <id> <status>
	if id, err := strconv.Atoi(rest[0]); err == nil && id > 0 {
		targetID = id
		if len(rest) > 1 {
			statusWord = rest[1]
		}
	} else {
		statusWord = rest[0]
		if len(rest) > 1 {
			if id, err := strconv.Atoi(rest[1]); err == nil && id > 0 {
				targetID = id
			} else {
				return fmt.Errorf("invalid pane id %q: %w", rest[1], err)
			}
		}
	}

	if targetID <= 0 {
		return fmt.Errorf("usage: wideboi set-pane-status [flags] <status> <pane-id> (pane-id required outside wideboi pane)")
	}

	clear := false
	var status protocol.PaneStatus

	switch strings.ToLower(statusWord) {
	case "clear", "reset", "none":
		clear = true
	case "working", "busy":
		status = protocol.StatusWorking
	case "input", "needs_input", "waiting":
		status = protocol.StatusNeedsInput
	case "done", "finished", "success":
		status = protocol.StatusDone
	case "failed", "error":
		status = protocol.StatusFailed
	case "idle":
		status = protocol.StatusIdle
	case "":
		return fmt.Errorf("usage: wideboi set-pane-status [flags] <status> [pane-id]")
	default:
		return fmt.Errorf("unknown status %q (must be working, input, done, failed, idle, or clear)", statusWord)
	}

	req := protocol.MsgSetPaneStatusRequest{
		PaneID: targetID,
		Status: status,
		Clear:  clear,
	}

	resp, err := rpcQuery[protocol.MsgSetPaneStatusResponse](cfg, req, 5*time.Second)
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
	addTargetFlags(fs, &session, &socket)

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
