package main

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/client"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/server"
	"github.com/lmorchard/wideboi/internal/transport"
)

func TestServerAndAttachViaUnixSocket(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "s.sock")

	sl, err := transport.NewSocketListener(sockPath)
	if err != nil {
		t.Fatalf("NewSocketListener failed: %v", err)
	}
	defer sl.Close()

	srv := server.NewServer(nil, "/bin/sh", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv.ListenSocket(ctx, sl)
	go func() {
		_ = srv.Run(ctx)
	}()

	// Connect first client
	conn1, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("first net.Dial failed: %v", err)
	}
	cConn1 := transport.NewClientSocketConn(conn1, 256)
	cConn1.RunPumps(ctx)

	cli1 := client.NewClient(cConn1, 80, 24, "C-b")
	cli1.Attach(ctx)

	// Verify client 1 receives layout snapshot
	select {
	case msg := <-cConn1.ServerSendChan():
		cli1.HandleServerMsg(msg)
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for client 1 initial message")
	}

	if cli1.FocusPaneID() == 0 {
		t.Fatal("client 1 FocusPaneID should be set after snapshot")
	}

	// Detach first client
	cConn1.Close()

	// Connect second client (reattach)
	conn2, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("second net.Dial failed: %v", err)
	}
	cConn2 := transport.NewClientSocketConn(conn2, 256)
	cConn2.RunPumps(ctx)

	cli2 := client.NewClient(cConn2, 80, 24, "C-b")
	cli2.Attach(ctx)

	// Verify reattached client 2 receives layout snapshot and pane update
	gotSnapshot := false
	gotPaneUpdate := false
	deadline := time.After(3 * time.Second)

	for !gotSnapshot || !gotPaneUpdate {
		select {
		case msg := <-cConn2.ServerSendChan():
			switch m := msg.(type) {
			case protocol.MsgLayoutSnapshot:
				gotSnapshot = true
				cli2.HandleServerMsg(m)
			case protocol.MsgPaneUpdate:
				gotPaneUpdate = true
				cli2.HandleServerMsg(m)
			}
		case <-deadline:
			t.Fatalf("timeout waiting for reattached client 2 messages (gotSnap=%v, gotUpdate=%v)", gotSnapshot, gotPaneUpdate)
		}
	}

	if cli2.FocusPaneID() != cli1.FocusPaneID() {
		t.Errorf("reattached client FocusPaneID=%d, want %d", cli2.FocusPaneID(), cli1.FocusPaneID())
	}

	cConn2.Close()
	_ = srv.Close()
}
