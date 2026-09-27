package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/lmorchard/wideboi/internal/config"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

var (
	ErrUnknownCommand = errors.New("unknown command")
	ErrRPCTimeout     = errors.New("timeout waiting for server response")
)

// Invocation carries execution context for a command.
type Invocation struct {
	Cfg          config.Config
	Socket       string
	CallerPaneID int
	DetachFile   string
	Args         []string
	Stdout       io.Writer
	Stderr       io.Writer
}

// Command is an executable internal wideboi command.
type Command struct {
	Name        string
	Aliases     []string
	Description string
	Category    string
	ArgsUsage   string
	Run         func(ctx context.Context, inv Invocation) error
}

// HandshakeError keeps the MismatchError reachable for errors.As while
// replacing its peer-neutral wording.
type HandshakeError struct {
	msg   string
	cause error
}

func (e *HandshakeError) Error() string { return e.msg }
func (e *HandshakeError) Unwrap() error { return e.cause }

// DescribeHandshakeErr phrases a failed handshake with the server at
// socket. hint, if set, replaces the usual advice.
func DescribeHandshakeErr(socket string, err error, hint string) error {
	var mm *transport.MismatchError
	if !errors.As(err, &mm) {
		return fmt.Errorf("handshake with the wideboi server at %s failed: %w", socket, err)
	}
	server := "the wideboi server at " + socket
	if mm.PID != 0 {
		server += fmt.Sprintf(" (pid %d)", mm.PID)
	}
	speaks := fmt.Sprintf("speaks protocol v%d", mm.Theirs)
	if mm.Theirs == 0 {
		speaks = "is from a build older than protocol versioning"
	}
	if hint == "" {
		hint = "Detach from it with a matching build, or start a separate session with -L <name>."
	}
	return &HandshakeError{
		msg:   fmt.Sprintf("%s %s; this client speaks v%d. %s", server, speaks, mm.Ours, hint),
		cause: err,
	}
}

// HandshakeServer checks that the server on conn speaks this build's
// protocol, and turns a refusal into something a user can act on. Long
// sessions outlive rebuilds, so a stale server on the socket is
// ordinary; before this it surfaced as "protobuf frame too large"
// (#174). conn is closed on failure.
func HandshakeServer(conn net.Conn, socket string) error {
	return HandshakeServerWithin(conn, socket, transport.HandshakeCeiling)
}

// HandshakeServerWithin is HandshakeServer with an explicit ceiling on
// the wait for the server's hello; see transport.HandshakeWithin.
func HandshakeServerWithin(conn net.Conn, socket string, ceiling time.Duration) error {
	if _, err := transport.HandshakeWithin(conn, ceiling); err != nil {
		conn.Close()
		return DescribeHandshakeErr(socket, err, "")
	}
	return nil
}

// ResolveSocket finds the socket to connect to from inv, config, or environment.
func ResolveSocket(inv Invocation) (string, error) {
	if inv.Socket != "" {
		return inv.Socket, nil
	}
	if inv.Cfg.Socket != "" {
		return inv.Cfg.Socket, nil
	}
	if inv.Cfg.Session != "" {
		return config.SessionSocketPath(inv.Cfg.Session), nil
	}
	cfg, _, err := config.Load(config.ConfigFlags{}, nil)
	if err != nil {
		return "", err
	}
	return cfg.Socket, nil
}

func resolveSocket(inv Invocation) string {
	s, _ := ResolveSocket(inv)
	return s
}

// DialServer connects to the session socket and completes the handshake.
func DialServer(ctx context.Context, socket string) (net.Conn, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socket)
	if err != nil {
		return nil, fmt.Errorf("no wideboi server running at %s: %w", socket, err)
	}
	if err := HandshakeServer(conn, socket); err != nil {
		return nil, err
	}
	return conn, nil
}

func dialServer(ctx context.Context, socket string) (net.Conn, error) {
	return DialServer(ctx, socket)
}

// SendClientMsg connects, handshakes, and sends a ClientMessage to the server.
func SendClientMsg(ctx context.Context, inv Invocation, msg transport.ClientMessage) error {
	socket, err := ResolveSocket(inv)
	if err != nil {
		return err
	}
	conn, err := DialServer(ctx, socket)
	if err != nil {
		return err
	}
	defer conn.Close()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()

	if err := transport.WriteClientFrame(conn, msg); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	return nil
}

// RPCOn sends req on an already-handshaken connection and waits for a
// response of type Resp, skipping broadcasts. A timeout of zero or less
// waits indefinitely.
func RPCOn[Resp any](ctx context.Context, cc *transport.ClientSocketConn, req transport.ClientMessage, timeout time.Duration) (Resp, error) {
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
			return zero, ErrRPCTimeout
		case <-ctx.Done():
			return zero, ctx.Err()
		}
	}
}

// RPCQuery connects, handshakes, sends a ClientMessage, and waits for a typed response.
func RPCQuery[Resp any](ctx context.Context, inv Invocation, req transport.ClientMessage, timeout time.Duration) (Resp, error) {
	var zero Resp
	socket, err := ResolveSocket(inv)
	if err != nil {
		return zero, err
	}
	conn, err := DialServer(ctx, socket)
	if err != nil {
		return zero, err
	}
	defer conn.Close()

	deadline := time.Time{}
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	if ctxDeadline, ok := ctx.Deadline(); ok {
		if deadline.IsZero() || ctxDeadline.Before(deadline) {
			deadline = ctxDeadline
		}
	}
	if !deadline.IsZero() {
		_ = conn.SetDeadline(deadline)
	}

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()

	if err := transport.WriteClientFrame(conn, req); err != nil {
		if ctx.Err() != nil {
			return zero, ctx.Err()
		}
		var netErr net.Error
		if errors.Is(err, os.ErrDeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
			return zero, ErrRPCTimeout
		}
		return zero, fmt.Errorf("write request: %w", err)
	}

	for {
		msg, err := transport.ReadServerFrame(conn)
		if err != nil {
			if ctx.Err() != nil {
				return zero, ctx.Err()
			}
			var netErr net.Error
			if errors.Is(err, os.ErrDeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
				return zero, ErrRPCTimeout
			}
			return zero, fmt.Errorf("read response: %w", err)
		}
		if resp, ok := msg.(Resp); ok {
			return resp, nil
		}
	}
}

// SendVerb sends a Verb message to the server for the given paneID (or CallerPaneID).
func SendVerb(ctx context.Context, inv Invocation, verb protocol.VerbType) error {
	paneID := inv.CallerPaneID
	return SendClientMsg(ctx, inv, protocol.MsgVerb{
		Verb:   verb,
		PaneID: paneID,
	})
}
