package server_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/server"
	"github.com/lmorchard/wideboi/internal/transport"
)

func dialTestWebSocket(t *testing.T, srvURL string) *websocket.Conn {
	t.Helper()
	wsURL, err := url.Parse(srvURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	wsURL.Scheme = "ws"
	wsURL.Path = "/ws"

	dialer := websocket.Dialer{
		HandshakeTimeout: 2 * time.Second,
		Subprotocols:     []string{fmt.Sprintf("wideboi.v%d", protocol.Version)},
	}
	conn, resp, err := dialer.Dial(wsURL.String(), nil)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("dial websocket failed (status %d): %v", status, err)
	}
	return conn
}

func sendWS(t *testing.T, conn *websocket.Conn, msg any) {
	t.Helper()
	payload, err := protocol.MarshalClient(msg)
	if err != nil {
		t.Fatalf("encode %T: %v", msg, err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, payload); err != nil {
		t.Fatalf("write %T: %v", msg, err)
	}
}

func readWS(t *testing.T, conn *websocket.Conn, timeout time.Duration) any {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	kind, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if kind != websocket.BinaryMessage {
		t.Fatalf("frame kind %d, want binary", kind)
	}
	msg, err := protocol.UnmarshalServer(payload)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return msg
}

// Regression test for Issue #272:
// A remote WebSocket peer cannot trigger session shutdown.
func TestRemoteWebSocketPeerCannotShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := server.NewServer(nil, "/bin/sh", "")
	mux := http.NewServeMux()
	s.ListenWebSocket(ctx, mux, "")

	ts := httptest.NewServer(mux)
	defer ts.Close()

	runErr := make(chan error, 1)
	go func() {
		runErr <- s.Run(ctx)
	}()

	ws := dialTestWebSocket(t, ts.URL)
	defer ws.Close()

	// Attach and drain initial snapshot
	sendWS(t, ws, protocol.MsgAttach{Cols: 80, Rows: 24})
	gotSnapshot := false
	for i := 0; i < 5 && !gotSnapshot; i++ {
		msg := readWS(t, ws, 2*time.Second)
		if _, ok := msg.(protocol.MsgLayoutSnapshot); ok {
			gotSnapshot = true
		}
	}
	if !gotSnapshot {
		t.Fatal("never received initial snapshot on websocket client")
	}

	// Attempt MsgShutdown from remote peer
	sendWS(t, ws, protocol.MsgShutdown{})

	// Wait briefly and verify server is still running and serving requests
	time.Sleep(100 * time.Millisecond)

	select {
	case err := <-runErr:
		t.Fatalf("server exited prematurely after remote MsgShutdown: %v", err)
	default:
	}

	// Verify WebSocket connection is still functional
	sendWS(t, ws, protocol.MsgStatusRequest{})
	gotStatus := false
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !gotStatus {
		msg := readWS(t, ws, 2*time.Second)
		if _, ok := msg.(protocol.MsgLayoutSnapshot); ok {
			gotStatus = true
		}
	}
	if !gotStatus {
		t.Fatal("websocket client stopped receiving messages after remote MsgShutdown refusal")
	}

	cancel()
	select {
	case err := <-runErr:
		if err != nil && err != context.Canceled {
			t.Errorf("Run error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for Run to stop after cancel")
	}
}

// Regression test for Issue #272:
// A remote WebSocket peer cannot execute binary upgrades.
func TestRemoteWebSocketPeerCannotUpgrade(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := server.NewServer(nil, "/bin/sh", "")
	mux := http.NewServeMux()
	s.ListenWebSocket(ctx, mux, "")

	ts := httptest.NewServer(mux)
	defer ts.Close()

	runErr := make(chan error, 1)
	go func() {
		runErr <- s.Run(ctx)
	}()

	ws := dialTestWebSocket(t, ts.URL)
	defer ws.Close()

	sendWS(t, ws, protocol.MsgUpgradeRequest{BinPath: "/bin/sh"})

	msg := readWS(t, ws, 2*time.Second)
	resp, ok := msg.(protocol.MsgUpgradeResponse)
	if !ok {
		t.Fatalf("got %T, want MsgUpgradeResponse", msg)
	}
	if resp.Error != "upgrade is restricted to local peers" {
		t.Fatalf("got error %q, want %q", resp.Error, "upgrade is restricted to local peers")
	}

	// Server should still be running
	select {
	case err := <-runErr:
		t.Fatalf("server exited unexpectedly: %v", err)
	default:
	}

	cancel()
	<-runErr
}

// Regression test for Issue #272:
// A remote WebSocket peer cannot issue web server control commands.
func TestRemoteWebSocketPeerCannotControlWebServer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := server.NewServer(nil, "/bin/sh", "")
	mux := http.NewServeMux()
	s.ListenWebSocket(ctx, mux, "")

	ts := httptest.NewServer(mux)
	defer ts.Close()

	runErr := make(chan error, 1)
	go func() {
		runErr <- s.Run(ctx)
	}()

	ws := dialTestWebSocket(t, ts.URL)
	defer ws.Close()

	sendWS(t, ws, protocol.MsgWebServerControlRequest{Action: protocol.WebServerActionStop})

	msg := readWS(t, ws, 2*time.Second)
	resp, ok := msg.(protocol.MsgWebServerControlResponse)
	if !ok {
		t.Fatalf("got %T, want MsgWebServerControlResponse", msg)
	}
	if resp.Error != "web server control is restricted to local peers" {
		t.Fatalf("got error %q, want %q", resp.Error, "web server control is restricted to local peers")
	}

	// Server should still be running
	select {
	case err := <-runErr:
		t.Fatalf("server exited unexpectedly: %v", err)
	default:
	}

	cancel()
	<-runErr
}

// Verification that Unix socket peer is NOT restricted by remote peer policy.
func TestLocalSocketPeerCanShutdownAndUpgrade(t *testing.T) {
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

	s := server.NewServer(nil, "/bin/sh", "")
	s.ListenSocket(ctx, sl)

	runErr := make(chan error, 1)
	go func() {
		runErr <- s.Run(ctx)
	}()

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

	// 1. Web server control: local peer gets normal response, not "restricted to local peers"
	cc.SendClient(ctx, protocol.MsgWebServerControlRequest{Action: protocol.WebServerActionStatus})
	select {
	case msg := <-cc.ServerSendChan():
		resp, ok := msg.(protocol.MsgWebServerControlResponse)
		if !ok {
			t.Fatalf("got %T, want MsgWebServerControlResponse", msg)
		}
		if resp.Error == "web server control is restricted to local peers" {
			t.Fatal("local peer was incorrectly refused web server control")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for web server status response")
	}

	// 2. Upgrade request: local peer gets normal error about bad path, not "restricted to local peers"
	cc.SendClient(ctx, protocol.MsgUpgradeRequest{BinPath: "/nonexistent/test/upgrade/bin"})
	select {
	case msg := <-cc.ServerSendChan():
		resp, ok := msg.(protocol.MsgUpgradeResponse)
		if !ok {
			t.Fatalf("got %T, want MsgUpgradeResponse", msg)
		}
		if resp.Error == "upgrade is restricted to local peers" {
			t.Fatal("local peer was incorrectly refused upgrade as remote")
		}
		if resp.Error == "" {
			t.Fatal("expected error for nonexistent binary")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for upgrade response")
	}

	// 3. MsgShutdown: local peer actually closes the server
	cc.SendClient(ctx, protocol.MsgShutdown{})
	select {
	case err := <-runErr:
		if err != nil {
			t.Errorf("Run exit error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not shut down after local MsgShutdown")
	}
}
