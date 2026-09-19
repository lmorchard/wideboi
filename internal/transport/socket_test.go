package transport_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

func TestSocketListenerAndConnRoundTrip(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "wb")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(dir)
	sockPath := filepath.Join(dir, "t.sock")

	sl, err := transport.NewSocketListener(sockPath)
	if err != nil {
		t.Fatalf("NewSocketListener failed: %v", err)
	}
	defer sl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	connErr := make(chan error, 1)
	var clientConn *transport.ClientSocketConn

	go func() {
		conn, err := net.Dial("unix", sockPath)
		if err != nil {
			connErr <- err
			return
		}
		clientConn = transport.NewClientSocketConn(conn, 16)
		clientConn.RunPumps(ctx)
		connErr <- nil
	}()

	srvConnRaw, err := sl.Accept()
	if err != nil {
		t.Fatalf("Accept failed: %v", err)
	}
	srvConn := transport.NewServerSocketConn(srvConnRaw, 16)
	srvConn.RunPumps(ctx)

	if err := <-connErr; err != nil {
		t.Fatalf("net.Dial failed: %v", err)
	}

	// Send client -> server message (MsgAttach)
	attachMsg := protocol.MsgAttach{Cols: 80, Rows: 24}
	if !clientConn.SendClient(ctx, attachMsg) {
		t.Fatal("client SendClient failed")
	}

	select {
	case msg := <-srvConn.ClientSendChan():
		got, ok := msg.(protocol.MsgAttach)
		if !ok || got.Cols != 80 || got.Rows != 24 {
			t.Fatalf("got server msg %+v, want MsgAttach {80, 24}", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for client message on server")
	}

	// Send server -> client message (MsgLayoutSnapshot)
	snapMsg := protocol.MsgLayoutSnapshot{FocusPaneID: 42}
	if !srvConn.SendServer(ctx, snapMsg) {
		t.Fatal("server SendServer failed")
	}

	select {
	case msg := <-clientConn.ServerSendChan():
		got, ok := msg.(protocol.MsgLayoutSnapshot)
		if !ok || got.FocusPaneID != 42 {
			t.Fatalf("got client msg %+v, want MsgLayoutSnapshot {FocusPaneID: 42}", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for server message on client")
	}

	clientConn.Close()
	srvConn.Close()
}
