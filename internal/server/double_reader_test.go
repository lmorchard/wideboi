package server

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

// Regression test for Issue #266:
// A client admitted before Run() must not receive a second reader loop when Run() starts.
func TestAdmittedTransportDoesNotGetDoubleReaderOnRun(t *testing.T) {
	owner := transport.NewInProcChannel(32)
	s := NewServer(owner, "/bin/sh", "")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Simulate an early client connecting (e.g. via socket before Run)
	earlyClient := transport.NewInProcChannel(32)
	s.mu.Lock()
	s.transports = append(s.transports, earlyClient)
	s.startTransportLoopLocked(ctx, earlyClient)
	if !s.startedTransports[earlyClient] {
		t.Fatal("earlyClient not marked in startedTransports")
	}
	s.mu.Unlock()

	// Start Run in a background goroutine
	runErr := make(chan error, 1)
	go func() {
		runErr <- s.Run(ctx)
	}()

	// Wait for server to enter event loop via StartupComplete signal
	deadline := time.Now().Add(2 * time.Second)
	for !s.StartupComplete() {
		if time.Now().After(deadline) {
			t.Fatal("timeout waiting for server startup")
		}
		time.Sleep(2 * time.Millisecond)
	}

	s.mu.Lock()
	// Both owner and earlyClient should be in startedTransports, exactly once
	if !s.startedTransports[owner] {
		t.Errorf("owner not marked in startedTransports")
	}
	if !s.startedTransports[earlyClient] {
		t.Errorf("earlyClient not marked in startedTransports")
	}
	s.mu.Unlock()

	// Verify messages sent on earlyClient are processed in exact order
	// Attach early client
	earlyClient.SendClient(ctx, protocol.MsgAttach{Cols: 80, Rows: 24})

	select {
	case <-earlyClient.ServerSend:
		// Received layout snapshot
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for attach response on earlyClient")
	}

	// Send an order-sensitive alternating sequence of width updates ending with 75.
	// If messages were consumed out of order by multiple readers, an earlier
	// width could execute after 75 and leave the final width != 75.
	for i := 0; i < 20; i++ {
		w := 40
		if i%2 == 1 {
			w = 60
		}
		earlyClient.SendClient(ctx, protocol.MsgSetPaneWidth{PaneID: 1, Width: w})
	}
	earlyClient.SendClient(ctx, protocol.MsgSetPaneWidth{PaneID: 1, Width: 75})

	// Wait for layout snapshot confirming the final width is 75
	gotFinalWidth := false
	widthTimeout := time.After(2 * time.Second)
	for !gotFinalWidth {
		select {
		case msg := <-earlyClient.ServerSend:
			if snap, ok := msg.(protocol.MsgLayoutSnapshot); ok {
				for _, col := range snap.Columns {
					if col.PaneID == 1 && col.Width == 75 {
						gotFinalWidth = true
					}
				}
			}
		case <-widthTimeout:
			s.mu.Lock()
			w, _ := s.strip.ColumnWidth(1)
			s.mu.Unlock()
			t.Fatalf("timeout waiting for final width 75 snapshot on earlyClient (current strip width %d)", w)
		}
	}

	cancel()
	select {
	case err := <-runErr:
		if err != nil && err != context.Canceled {
			t.Errorf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for Run to stop")
	}
}

// Regression test for Issue #279:
// A real Unix socket client admitted before Run() must not receive a second
// reader loop when Run() starts, ensuring messages arrive once and in exact order.
func TestSocketClientAdmittedBeforeRun(t *testing.T) {
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := NewServer(nil, "/bin/sh", "")
	// Admit socket listener BEFORE Run is started.
	s.ListenSocket(ctx, sl)

	// Connect client to the socket before Run starts.
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if _, err := transport.Handshake(conn); err != nil {
		t.Fatalf("handshake: %v", err)
	}
	cc := transport.NewClientSocketConn(conn, 256)
	cc.RunPumps(ctx)

	// Send Attach to create initial pane and snapshot
	cc.SendClient(ctx, protocol.MsgAttach{Cols: 80, Rows: 24})

	// Wait for client to receive initial server messages (snapshot etc.)
	select {
	case <-cc.ServerSendChan():
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for server message on early socket client")
	}

	// Verify the transport loop is started in Server
	s.mu.Lock()
	if len(s.transports) != 1 {
		t.Fatalf("expected 1 transport, got %d", len(s.transports))
	}
	sConn := s.transports[0]
	if !s.startedTransports[sConn] {
		t.Fatal("sConn not marked in startedTransports before Run")
	}
	s.mu.Unlock()

	// Now start Run in background
	runErr := make(chan error, 1)
	go func() {
		runErr <- s.Run(ctx)
	}()

	// Wait for server to enter event loop via StartupComplete signal
	startupDeadline := time.Now().Add(3 * time.Second)
	for !s.StartupComplete() {
		if time.Now().After(startupDeadline) {
			t.Fatal("timeout waiting for server startup")
		}
		time.Sleep(2 * time.Millisecond)
	}

	// Send an order-sensitive sequence of width changes (from 30 to 70).
	// If messages were consumed out of order by multiple readers, an earlier
	// width could execute after a later one and leave the final width != 70.
	for w := 30; w <= 70; w++ {
		cc.SendClient(ctx, protocol.MsgSetPaneWidth{PaneID: 1, Width: w})
	}

	// Send an order-sensitive sequence of distinct keystrokes into the pane.
	const expectedInput = "abcdefghijklmnopqrstuvwxyz0123456789"
	for _, b := range []byte(expectedInput) {
		cc.SendClient(ctx, protocol.MsgInput{PaneID: 1, Data: []byte{b}})
	}

	// Request capture to verify the pane received keystrokes strictly in order.
	// Wait until capture response contains the full expected sequence.
	cc.SendClient(ctx, protocol.MsgCaptureRequest{PaneID: 1})
	gotInputSequence := false
	captureDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(captureDeadline) && !gotInputSequence {
		select {
		case msg, ok := <-cc.ServerSendChan():
			if !ok {
				t.Fatal("server closed connection unexpectedly")
			}
			if capResp, ok := msg.(protocol.MsgCaptureResponse); ok && capResp.PaneID == 1 {
				if strings.Contains(capResp.Text, expectedInput) {
					gotInputSequence = true
				}
			}
		case <-time.After(100 * time.Millisecond):
			// Resend capture request if child process hasn't finished echoing all bytes yet
			cc.SendClient(ctx, protocol.MsgCaptureRequest{PaneID: 1})
		}
	}
	if !gotInputSequence {
		t.Fatalf("pane did not echo expected input sequence %q in order", expectedInput)
	}

	// Verify the final column width is strictly 70
	s.mu.Lock()
	finalWidth, ok := s.strip.ColumnWidth(1)
	s.mu.Unlock()
	if !ok || finalWidth != 70 {
		t.Fatalf("expected final column width 70, got %d (ok: %v)", finalWidth, ok)
	}

	// Verify with a status request roundtrip that all previous messages were drained and handled
	cc.SendClient(ctx, protocol.MsgStatusRequest{})
	gotStatus := false
	timeout := time.After(3 * time.Second)
	for !gotStatus {
		select {
		case msg, ok := <-cc.ServerSendChan():
			if !ok {
				t.Fatal("server closed connection unexpectedly")
			}
			if _, ok := msg.(protocol.MsgLayoutSnapshot); ok {
				gotStatus = true
			}
		case <-timeout:
			t.Fatal("timeout waiting for status snapshot")
		}
	}

	cancel()
	select {
	case err := <-runErr:
		if err != nil && err != context.Canceled {
			t.Errorf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for Run to exit")
	}
}
