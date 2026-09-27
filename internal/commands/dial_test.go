package commands_test

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/commands"
	"github.com/lmorchard/wideboi/internal/config"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

func writeMismatchHello(c net.Conn, version, pid uint32) error {
	payload := make([]byte, 16)
	copy(payload, "WIDEBOI\x00")
	binary.BigEndian.PutUint32(payload[8:], version)
	binary.BigEndian.PutUint32(payload[12:], pid)
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if _, err := c.Write(header[:]); err != nil {
		return err
	}
	_, err := c.Write(payload)
	return err
}

func writeServerMsg(w io.Writer, msg any) error {
	payload, err := protocol.MarshalServer(msg)
	if err != nil {
		return err
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	_, err = w.Write(payload)
	return err
}

func readClientMsg(r io.Reader) (transport.ClientMessage, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	size := binary.BigEndian.Uint32(header[:])
	payload := make([]byte, size)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return protocol.UnmarshalClient(payload)
}

func TestResolveSocket(t *testing.T) {
	t.Run("explicit inv socket", func(t *testing.T) {
		inv := commands.Invocation{Socket: "/custom/path.sock"}
		sock, err := commands.ResolveSocket(inv)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sock != "/custom/path.sock" {
			t.Errorf("got %q, want %q", sock, "/custom/path.sock")
		}
	})

	t.Run("cfg socket", func(t *testing.T) {
		inv := commands.Invocation{
			Cfg: config.Config{Socket: "/cfg/path.sock"},
		}
		sock, err := commands.ResolveSocket(inv)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sock != "/cfg/path.sock" {
			t.Errorf("got %q, want %q", sock, "/cfg/path.sock")
		}
	})

	t.Run("cfg session", func(t *testing.T) {
		inv := commands.Invocation{
			Cfg: config.Config{Session: "mysession"},
		}
		sock, err := commands.ResolveSocket(inv)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := config.SessionSocketPath("mysession")
		if sock != want {
			t.Errorf("got %q, want %q", sock, want)
		}
	})

	t.Run("env WIDEBOI_SESSION", func(t *testing.T) {
		t.Setenv("WIDEBOI_SESSION", "fromenv")
		t.Setenv("WIDEBOI_SOCK", "")
		inv := commands.Invocation{}
		sock, err := commands.ResolveSocket(inv)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := config.SessionSocketPath("fromenv")
		if sock != want {
			t.Errorf("got %q, want %q", sock, want)
		}
	})

	t.Run("env WIDEBOI_SOCK", func(t *testing.T) {
		sockPath := filepath.Join(t.TempDir(), "env.sock")
		t.Setenv("WIDEBOI_SESSION", "")
		t.Setenv("WIDEBOI_SOCK", sockPath)
		inv := commands.Invocation{}
		sock, err := commands.ResolveSocket(inv)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sock != sockPath {
			t.Errorf("got %q, want %q", sock, sockPath)
		}
	})

	t.Run("both env vars set fails", func(t *testing.T) {
		t.Setenv("WIDEBOI_SESSION", "sess")
		t.Setenv("WIDEBOI_SOCK", "/env/path.sock")
		inv := commands.Invocation{}
		_, err := commands.ResolveSocket(inv)
		if err == nil {
			t.Fatal("expected error when both session and socket are set in env, got nil")
		}
		if !strings.Contains(err.Error(), "set one, not both") {
			t.Errorf("expected error containing 'set one, not both', got %v", err)
		}
	})

	t.Run("defaults when empty", func(t *testing.T) {
		t.Setenv("WIDEBOI_SESSION", "")
		t.Setenv("WIDEBOI_SOCK", "")
		inv := commands.Invocation{}
		sock, err := commands.ResolveSocket(inv)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sock != config.DefaultSocketPath() {
			t.Errorf("got %q, want default %q", sock, config.DefaultSocketPath())
		}
	})
}

func TestDescribeHandshakeErr(t *testing.T) {
	socket := "/tmp/test.sock"

	t.Run("non-mismatch error", func(t *testing.T) {
		err := errors.New("connection reset")
		desc := commands.DescribeHandshakeErr(socket, err, "")
		if !strings.Contains(desc.Error(), "handshake with the wideboi server at /tmp/test.sock failed: connection reset") {
			t.Errorf("unexpected error string: %v", desc)
		}
	})

	t.Run("mismatch error with PID and version", func(t *testing.T) {
		mismatch := &transport.MismatchError{
			Ours:   15,
			Theirs: 14,
			PID:    12345,
		}
		desc := commands.DescribeHandshakeErr(socket, mismatch, "")
		msg := desc.Error()
		if !strings.Contains(msg, "(pid 12345)") {
			t.Errorf("expected PID in %q", msg)
		}
		if !strings.Contains(msg, "speaks protocol v14; this client speaks v15") {
			t.Errorf("expected version info in %q", msg)
		}
		if !strings.Contains(msg, "Detach from it with a matching build") {
			t.Errorf("expected default advice hint in %q", msg)
		}

		var mm *transport.MismatchError
		if !errors.As(desc, &mm) || mm.Theirs != 14 {
			t.Errorf("expected errors.As to unwrap to MismatchError, got %v", mm)
		}
	})

	t.Run("mismatch error older build (theirs=0)", func(t *testing.T) {
		mismatch := &transport.MismatchError{
			Ours:   15,
			Theirs: 0,
		}
		desc := commands.DescribeHandshakeErr(socket, mismatch, "Custom hint.")
		msg := desc.Error()
		if !strings.Contains(msg, "is from a build older than protocol versioning") {
			t.Errorf("expected older build notice in %q", msg)
		}
		if !strings.Contains(msg, "Custom hint.") {
			t.Errorf("expected custom hint in %q", msg)
		}
	})
}

func TestDialServer_NoServer(t *testing.T) {
	nonexistent := filepath.Join(t.TempDir(), "nonexistent.sock")
	_, err := commands.DialServer(context.Background(), nonexistent)
	if err == nil {
		t.Fatal("expected dial error, got nil")
	}
	if !strings.Contains(err.Error(), "no wideboi server running at "+nonexistent) {
		t.Errorf("expected 'no wideboi server running at...', got %v", err)
	}
}

func TestDialServer_Mismatch(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "mismatch.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// Handshake from server with a different protocol version
		_ = writeMismatchHello(conn, 999, 4321)
		// Drain client hello
		var header [4]byte
		if _, err := io.ReadFull(conn, header[:]); err == nil {
			size := binary.BigEndian.Uint32(header[:])
			buf := make([]byte, size)
			_, _ = io.ReadFull(conn, buf)
		}
	}()

	_, err = commands.DialServer(context.Background(), sockPath)
	<-done
	if err == nil {
		t.Fatal("expected handshake error, got nil")
	}

	var mm *transport.MismatchError
	if !errors.As(err, &mm) {
		t.Fatalf("expected MismatchError via errors.As, got %v", err)
	}
	if mm.Theirs != 999 || mm.PID != 4321 {
		t.Errorf("unexpected mismatch fields: %+v", mm)
	}
	if !strings.Contains(err.Error(), "(pid 4321)") || !strings.Contains(err.Error(), "speaks protocol v999") {
		t.Errorf("expected formatted hint in error, got: %v", err)
	}
}

func TestRPCQuery_SuccessAndTimeout(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "rpc.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	// Server goroutine to respond to first RPC query
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		// Server side handshake
		if _, err := transport.Handshake(conn); err != nil {
			return
		}

		// Read request
		req, err := readClientMsg(conn)
		if err != nil {
			return
		}
		if _, ok := req.(protocol.MsgSplitRequest); !ok {
			return
		}

		// Send an unrelated message first (should be skipped by RPCQuery)
		_ = writeServerMsg(conn, protocol.MsgMacrosSnapshot{
			Macros: []protocol.Macro{{Name: "foo"}},
		})

		// Send the expected response
		_ = writeServerMsg(conn, protocol.MsgSplitResponse{
			PaneID: 42,
		})
	}()

	inv := commands.Invocation{Socket: sockPath}
	resp, err := commands.RPCQuery[protocol.MsgSplitResponse](
		context.Background(),
		inv,
		protocol.MsgSplitRequest{Command: "test"},
		2*time.Second,
	)
	if err != nil {
		t.Fatalf("RPCQuery failed: %v", err)
	}
	if resp.PaneID != 42 {
		t.Errorf("got pane ID %d, want 42", resp.PaneID)
	}

	// Now test timeout with a server that accepts and handshakes but never responds
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = transport.Handshake(conn)
		_, _ = readClientMsg(conn)
		// Do not send anything; sleep until closed
		time.Sleep(200 * time.Millisecond)
	}()

	_, err = commands.RPCQuery[protocol.MsgSplitResponse](
		context.Background(),
		inv,
		protocol.MsgSplitRequest{Command: "timeout-test"},
		50*time.Millisecond,
	)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !errors.Is(err, commands.ErrRPCTimeout) {
		t.Errorf("got %v, want ErrRPCTimeout", err)
	}
}

func TestRPCOn_SuccessAndTimeout(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "rpcon.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		// Read client frame
		_, err = readClientMsg(conn)
		if err != nil {
			return
		}
		// Send non-matching message
		_ = writeServerMsg(conn, protocol.MsgMacrosSnapshot{})
		// Send matching response
		_ = writeServerMsg(conn, protocol.MsgSplitResponse{PaneID: 77})

		// For the subsequent timeout test, just read and don't respond
		_, _ = readClientMsg(conn)
		time.Sleep(100 * time.Millisecond)
	}()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cc := transport.NewClientSocketConn(conn, 16)
	cc.RunPumps(ctx)

	resp, err := commands.RPCOn[protocol.MsgSplitResponse](
		ctx,
		cc,
		protocol.MsgSplitRequest{Command: "test"},
		2*time.Second,
	)
	if err != nil {
		t.Fatalf("RPCOn failed: %v", err)
	}
	if resp.PaneID != 77 {
		t.Errorf("got %d, want 77", resp.PaneID)
	}

	// Timeout test
	_, err = commands.RPCOn[protocol.MsgSplitResponse](
		ctx,
		cc,
		protocol.MsgSplitRequest{Command: "test"},
		20*time.Millisecond,
	)
	if !errors.Is(err, commands.ErrRPCTimeout) {
		t.Errorf("got %v, want ErrRPCTimeout", err)
	}
}

func TestSendClientMsgAndSendVerb(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "verb.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	received := make(chan transport.ClientMessage, 2)
	go func() {
		for i := 0; i < 2; i++ {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				if _, err := transport.Handshake(c); err != nil {
					return
				}
				msg, err := readClientMsg(c)
				if err != nil {
					return
				}
				received <- msg
			}(conn)
		}
	}()

	inv := commands.Invocation{
		Socket:       sockPath,
		CallerPaneID: 42,
	}

	if err := commands.SendVerb(context.Background(), inv, protocol.VerbNewColumn); err != nil {
		t.Fatalf("SendVerb failed: %v", err)
	}

	select {
	case msg := <-received:
		verbMsg, ok := msg.(protocol.MsgVerb)
		if !ok {
			t.Fatalf("got %T, want MsgVerb", msg)
		}
		if verbMsg.Verb != protocol.VerbNewColumn || verbMsg.PaneID != 42 {
			t.Errorf("unexpected verb message: %+v", verbMsg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for MsgVerb")
	}

	if err := commands.SendClientMsg(context.Background(), inv, protocol.MsgDetach{}); err != nil {
		t.Fatalf("SendClientMsg failed: %v", err)
	}

	select {
	case msg := <-received:
		if _, ok := msg.(protocol.MsgDetach); !ok {
			t.Fatalf("got %T, want MsgDetach", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for MsgDetach")
	}
}
