package server

// White-box (package server) so it can count registered transports: a
// client that fails the handshake must never become one.

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

// attachOverSocket dials sock as a current client would and waits for
// the server's first message.
func attachOverSocket(t *testing.T, ctx context.Context, sock string) *transport.ClientSocketConn {
	t.Helper()
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if _, err := transport.Handshake(conn); err != nil {
		conn.Close()
		t.Fatalf("handshake: %v", err)
	}
	cc := transport.NewClientSocketConn(conn, 256)
	cc.RunPumps(ctx)
	cc.SendClient(ctx, protocol.MsgAttach{})
	select {
	case <-cc.ServerSendChan():
	case <-time.After(3 * time.Second):
		t.Fatal("attached client never heard from the server")
	}
	return cc
}

func (s *Server) transportCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.transports)
}

// A client from another build must get refused without costing the
// session anything: the server keeps serving the client it has, and a
// matching client can still attach (#174).
func TestMismatchedPeerLeavesSessionRunning(t *testing.T) {
	// Not t.TempDir(): the test name would push the path past the
	// sun_path limit.
	dir, err := os.MkdirTemp("", "wb")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "s.sock")
	sl, err := transport.NewSocketListener(sock)
	if err != nil {
		t.Fatalf("NewSocketListener: %v", err)
	}
	defer sl.Close()
	s := NewServer(nil, "/bin/sh", "")
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.ListenSocket(ctx, sl)
	go func() { _ = s.Run(ctx) }()

	first := attachOverSocket(t, ctx, sock)
	defer first.Close()

	otherVersion := make([]byte, 4+16)
	binary.BigEndian.PutUint32(otherVersion, 16)
	copy(otherVersion[4:], "WIDEBOI\x00")
	binary.BigEndian.PutUint32(otherVersion[12:], protocol.Version+1)
	binary.BigEndian.PutUint32(otherVersion[16:], 99)

	peers := map[string][]byte{
		"other version": otherVersion,
		// The start of a gob stream, as a pre-protobuf client sends it.
		"gob stream": {0xFF, 0xA0, 0x10, 0x00, 0x01, 0x02, 0x03, 0x04},
	}
	for name, greeting := range peers {
		t.Run(name, func(t *testing.T) {
			c, err := net.Dial("unix", sock)
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			defer c.Close()
			if _, err := c.Write(greeting); err != nil {
				t.Fatalf("write: %v", err)
			}
			// The server answers with its own hello and hangs up.
			_ = c.SetReadDeadline(time.Now().Add(transport.HandshakeCeiling + time.Second))
			// On Linux a close with our greeting partly unread is a reset,
			// which is as much a hang-up as EOF.
			if _, err := io.ReadAll(c); err != nil && !errors.Is(err, syscall.ECONNRESET) {
				t.Fatalf("server did not hang up on the mismatched peer: %v", err)
			}
			if n := s.transportCount(); n != 1 {
				t.Fatalf("%d transports registered, want only the matching client", n)
			}
		})
	}

	if s.stoppingLocked() {
		t.Fatal("a mismatched peer stopped the server")
	}
	second := attachOverSocket(t, ctx, sock)
	defer second.Close()
	if n := s.transportCount(); n != 2 {
		t.Fatalf("%d transports registered, want both matching clients", n)
	}
}
