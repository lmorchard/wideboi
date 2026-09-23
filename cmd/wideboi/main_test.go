package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/client"
	"github.com/lmorchard/wideboi/internal/config"
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

	if cli1.FocusedPaneID() == 0 {
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

	if cli2.FocusedPaneID() != cli1.FocusedPaneID() {
		t.Errorf("reattached client FocusPaneID=%d, want %d", cli2.FocusedPaneID(), cli1.FocusedPaneID())
	}

	cConn2.Close()
	_ = srv.Close()
}

// kill-session must end the session, not just the connection it came in
// on: the server stops, and a client attached alongside is hung up on.
//
// SetCloseGrace is test-only inside package server, so this runs with the
// real grace -- /bin/sh ignores SIGTERM, so expect a couple of seconds.
func TestKillSessionShutsDownAServer(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "s.sock")
	sl, err := transport.NewSocketListener(sockPath)
	if err != nil {
		t.Fatalf("NewSocketListener: %v", err)
	}
	defer sl.Close()
	srv := server.NewServer(nil, "/bin/sh", "")
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.ListenSocket(ctx, sl)
	runDone := make(chan error, 1)
	go func() { runDone <- srv.Run(ctx) }()

	// One attached client, so there are panes to reap and a bystander
	// to be hung up on.
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	cc := transport.NewClientSocketConn(conn, 256)
	cc.RunPumps(ctx)
	client.NewClient(cc, 80, 24, "C-b").Attach(ctx)
	select {
	case <-cc.ServerSendChan():
	case <-time.After(3 * time.Second):
		t.Fatal("attached client never heard from the server")
	}

	if err := runKillSession(config.Config{Socket: sockPath}); err != nil {
		t.Fatalf("kill-session: %v", err)
	}
	select {
	case <-runDone:
	case <-time.After(shutdownCeiling):
		t.Fatal("server kept running after kill-session")
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-cc.ServerSendChan():
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("kill-session did not hang up on the attached client")
		}
	}
}

// An idle session must go quiet on the wire. Before #85 the server sent
// every pane to every client each 33ms frame whether or not it had
// changed, so no quiet second ever came. Change-only sends must still
// deliver a change, which the second half checks.
func TestIdleSessionStopsSendingPaneUpdates(t *testing.T) {
	// Not t.TempDir: this test's name makes that path longer than the
	// 104 bytes darwin allows a unix socket.
	dir, err := os.MkdirTemp("", "wb")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
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
	go func() { _ = srv.Run(ctx) }()
	defer srv.Close()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("net.Dial failed: %v", err)
	}
	cc := transport.NewClientSocketConn(conn, 256)
	cc.RunPumps(ctx)
	defer cc.Close()
	cli := client.NewClient(cc, 80, 24, "C-b")
	cli.Attach(ctx)

	// waitQuiet reports whether a stretch of `quiet` with no
	// MsgPaneUpdate arrives before `ceiling`. Every message still goes
	// through the client, so the focus pane is known for SendInput.
	waitQuiet := func(quiet, ceiling time.Duration) bool {
		deadline := time.After(ceiling)
		timer := time.NewTimer(quiet)
		defer timer.Stop()
		for {
			select {
			case msg := <-cc.ServerSendChan():
				cli.HandleServerMsg(msg)
				if _, ok := msg.(protocol.MsgPaneUpdate); ok {
					timer.Reset(quiet)
				}
			case <-timer.C:
				return true
			case <-deadline:
				return false
			}
		}
	}
	// The ceiling covers shell startup and the Working->Idle status
	// decay at ~3s, whose snapshot forces one more round of updates.
	if !waitQuiet(time.Second, 8*time.Second) {
		t.Fatal("an idle session never went a full second without a MsgPaneUpdate")
	}

	// Typing into an idle pane flips its status to Working, and that
	// status snapshot forces every pane to be resent -- which would
	// deliver the keystroke even if Write never advanced the generation.
	// So the first key only wakes the pane. The second is typed well
	// inside the ~3s idle decay, with nothing left to change status, so
	// it has to come through on the generation alone.
	awaitUpdate := func(key string) (afterSnapshot bool) {
		deadline := time.After(3 * time.Second)
		for {
			select {
			case msg := <-cc.ServerSendChan():
				cli.HandleServerMsg(msg)
				switch msg.(type) {
				case protocol.MsgLayoutSnapshot:
					afterSnapshot = true
				case protocol.MsgPaneUpdate:
					return afterSnapshot
				}
			case <-deadline:
				t.Fatalf("typing %q produced no MsgPaneUpdate", key)
			}
		}
	}
	cli.SendInput(ctx, []byte("x"))
	awaitUpdate("x")
	if !waitQuiet(300*time.Millisecond, 2*time.Second) {
		t.Fatal("the pane never settled after the first keystroke")
	}
	cli.SendInput(ctx, []byte("y"))
	if awaitUpdate("y") {
		t.Fatal("a layout snapshot preceded the second keystroke's update, so this run cannot show the generation delivered it")
	}
}
