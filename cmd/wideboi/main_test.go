package main

import (
	"context"
	"net"
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

// A typo in WIDEBOI_LAYOUT must be an error, not a silent fallback to
// the default. An unmatchable config value that looks exactly like an
// absent one is how the pgdn binding shipped dead -- see
// docs/LESSONS.md, "A binding nobody typed is a binding nobody
// verified."
func TestParseLayoutRejectsUnknown(t *testing.T) {
	for _, name := range []string{"card", "Cards", "fan", "scrolling"} {
		if _, err := parseLayout(name); err == nil {
			t.Errorf("parseLayout(%q) accepted an unknown value; want an error", name)
		}
	}
}

func TestParseLayoutAcceptsKnown(t *testing.T) {
	cases := map[string]protocol.LayoutMode{
		"":       protocol.LayoutCards,
		"scroll": protocol.LayoutScroll,
		"cards":  protocol.LayoutCards,
	}
	for name, want := range cases {
		got, err := parseLayout(name)
		if err != nil {
			t.Errorf("parseLayout(%q): %v", name, err)
			continue
		}
		if got != want {
			t.Errorf("parseLayout(%q) = %v, want %v", name, got, want)
		}
	}
}
