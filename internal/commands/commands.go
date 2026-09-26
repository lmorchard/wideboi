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
)

// Invocation carries execution context for a command.
type Invocation struct {
	Cfg          config.Config
	Socket       string
	CallerPaneID int
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

// resolveSocket finds the socket to connect to from inv, config, or environment.
func resolveSocket(inv Invocation) string {
	if inv.Socket != "" {
		return inv.Socket
	}
	if inv.Cfg.Socket != "" {
		return inv.Cfg.Socket
	}
	if env := os.Getenv("WIDEBOI_SOCK"); env != "" {
		return env
	}
	return config.DefaultSocketPath()
}

// dialServer connects to the session socket and completes the handshake.
func dialServer(ctx context.Context, socket string) (net.Conn, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socket)
	if err != nil {
		return nil, fmt.Errorf("connect to wideboi server at %s: %w", socket, err)
	}
	if _, err := transport.Handshake(conn); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("server handshake: %w", err)
	}
	return conn, nil
}

// SendClientMsg connects, handshakes, and sends a ClientMessage to the server.
func SendClientMsg(ctx context.Context, inv Invocation, msg transport.ClientMessage) error {
	socket := resolveSocket(inv)
	conn, err := dialServer(ctx, socket)
	if err != nil {
		return err
	}
	defer conn.Close()
	return transport.WriteClientFrame(conn, msg)
}

// RPCQuery connects, handshakes, sends a ClientMessage, and waits for a typed response.
func RPCQuery[Resp any](ctx context.Context, inv Invocation, req transport.ClientMessage, timeout time.Duration) (Resp, error) {
	var zero Resp
	socket := resolveSocket(inv)
	conn, err := dialServer(ctx, socket)
	if err != nil {
		return zero, err
	}
	defer conn.Close()

	if err := transport.WriteClientFrame(conn, req); err != nil {
		return zero, fmt.Errorf("write request: %w", err)
	}

	if timeout > 0 {
		_ = conn.SetReadDeadline(time.Now().Add(timeout))
	}

	for {
		msg, err := transport.ReadServerFrame(conn)
		if err != nil {
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
