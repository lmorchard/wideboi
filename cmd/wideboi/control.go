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

// rpcQuery connects to the running session server, sends a client message, and waits
// for a matching server response type within the timeout.
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

	if !cc.SendClient(ctx, req) {
		return zero, fmt.Errorf("failed to send request to server")
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case msg, ok := <-cc.ServerSendChan():
			if !ok {
				return zero, fmt.Errorf("server closed connection before sending response")
			}
			if resp, ok := msg.(Resp); ok {
				return resp, nil
			}
		case <-timer.C:
			return zero, fmt.Errorf("timeout waiting for server response")
		}
	}
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
				arg == "-after" || arg == "--after") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				flags = append(flags, args[i])
			}
		} else {
			operands = append(operands, arg)
		}
	}
	return append(flags, operands...)
}

// runSplit creates a new pane and prints its pane ID to stdout.
func runSplit(cfg config.Config, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("split", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var cwd string
	var after int
	var session, socket string

	fs.StringVar(&cwd, "cwd", "", "working directory for new pane")
	fs.IntVar(&after, "after", 0, "insert pane after specified pane ID")
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

	var command string
	if len(fs.Args()) > 0 {
		command = strings.Join(fs.Args(), " ")
	}

	req := protocol.MsgSplitRequest{
		Command:     command,
		Cwd:         cwd,
		AfterPaneID: after,
	}

	resp, err := rpcQuery[protocol.MsgSplitResponse](cfg, req, 5*time.Second)
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
	if len(rest) < 2 {
		return fmt.Errorf("usage: wideboi send [flags] <pane-id> <text>")
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
