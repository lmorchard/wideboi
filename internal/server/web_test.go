package server_test

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/server"
	"github.com/lmorchard/wideboi/internal/transport"
)

func TestWebServerToggle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "test.sock")

	s := server.NewServer(nil, "/bin/sh", "")
	defer s.Close()

	s.InitWebServer(server.WebServerConfig{
		SocketPath: sockPath,
		TLSEnabled: true,
	})

	// Initial state: not running
	st := s.WebServerStatus()
	if st.Running {
		t.Fatal("expected web server initially not running")
	}

	// Start web server on loopback :0
	resp, err := s.StartWebServer(ctx, protocol.MsgWebServerControlRequest{
		Action: protocol.WebServerActionStart,
		Addr:   "127.0.0.1:0",
	})
	if err != nil {
		t.Fatalf("start web server failed: %v", err)
	}
	if !resp.Running {
		t.Fatal("expected resp.Running to be true")
	}
	if resp.Addr == "" || resp.URL == "" || resp.Token == "" {
		t.Fatalf("expected non-empty Addr, URL, and Token: %+v", resp)
	}
	if !resp.TLSEnabled {
		t.Fatal("expected TLS to be enabled")
	}

	// Check that .web-token file exists and contains token
	tokenFile := server.WebTokenPath(sockPath)
	tokenBytes, err := os.ReadFile(tokenFile)
	if err != nil {
		t.Fatalf("read web-token file: %v", err)
	}
	if strings.TrimSpace(string(tokenBytes)) != resp.Token {
		t.Fatalf("token file content = %q, want %q", string(tokenBytes), resp.Token)
	}

	// Dial WebSocket with valid token and TLS
	wsURL := fmt.Sprintf("wss://%s/ws?token=%s", resp.Addr, resp.Token)
	dialer := websocket.Dialer{
		TLSClientConfig:  &tls.Config{InsecureSkipVerify: true},
		Subprotocols:     []string{fmt.Sprintf("wideboi.v%d", protocol.Version)},
		HandshakeTimeout: 2 * time.Second,
	}
	conn, httpResp, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket failed: %v (resp: %+v)", err, httpResp)
	}
	defer conn.Close()

	// Query status
	st = s.WebServerStatus()
	if !st.Running || st.Addr != resp.Addr || st.Token != resp.Token {
		t.Fatalf("unexpected status: %+v", st)
	}

	// Stop web server
	stopResp, err := s.StopWebServer()
	if err != nil {
		t.Fatalf("stop web server failed: %v", err)
	}
	if stopResp.Running {
		t.Fatal("expected stopResp.Running to be false")
	}

	// Token file should be removed
	if _, err := os.Stat(tokenFile); !os.IsNotExist(err) {
		t.Fatalf("expected token file to be removed, got err: %v", err)
	}

	// Existing WebSocket connection should receive EOF / close
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, readErr := conn.ReadMessage()
	if readErr == nil {
		t.Fatal("expected websocket read to fail after web server stopped")
	}

	// New connection should fail to connect
	_, _, dialErr := dialer.Dial(wsURL, nil)
	if dialErr == nil {
		t.Fatal("expected new connection to fail after stop")
	}
}

func TestWebServerRestartReusesTokenAndRotates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := server.NewServer(nil, "/bin/sh", "")
	defer s.Close()

	s.InitWebServer(server.WebServerConfig{
		TLSEnabled: false,
	})

	// Start 1
	resp1, err := s.StartWebServer(ctx, protocol.MsgWebServerControlRequest{
		Action:     protocol.WebServerActionStart,
		Addr:       "127.0.0.1:0",
		DisableTLS: true,
	})
	if err != nil {
		t.Fatalf("start 1 failed: %v", err)
	}
	t1 := resp1.Token

	// Stop
	_, err = s.StopWebServer()
	if err != nil {
		t.Fatalf("stop failed: %v", err)
	}

	// Restart without rotate_token -> should reuse t1
	resp2, err := s.StartWebServer(ctx, protocol.MsgWebServerControlRequest{
		Action:     protocol.WebServerActionStart,
		Addr:       "127.0.0.1:0",
		DisableTLS: true,
	})
	if err != nil {
		t.Fatalf("start 2 failed: %v", err)
	}
	if resp2.Token != t1 {
		t.Fatalf("expected token reuse %q, got %q", t1, resp2.Token)
	}

	// Restart with rotate_token -> should generate new token
	resp3, err := s.StartWebServer(ctx, protocol.MsgWebServerControlRequest{
		Action:      protocol.WebServerActionStart,
		RotateToken: true,
		DisableTLS:  true,
	})
	if err != nil {
		t.Fatalf("start 3 failed: %v", err)
	}
	if resp3.Token == t1 {
		t.Fatalf("expected rotated token to differ from %q, got same", t1)
	}
}

func TestWebServerPortConflict(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Occupy a port
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy port: %v", err)
	}
	defer l.Close()
	occupiedAddr := l.Addr().String()

	s := server.NewServer(nil, "/bin/sh", "")
	defer s.Close()

	s.InitWebServer(server.WebServerConfig{
		TLSEnabled: false,
	})

	// Attempt to start web server on occupied port
	resp, err := s.StartWebServer(ctx, protocol.MsgWebServerControlRequest{
		Action:     protocol.WebServerActionStart,
		Addr:       occupiedAddr,
		DisableTLS: true,
	})
	if err == nil {
		t.Fatal("expected error when binding occupied port")
	}
	if resp.Error == "" {
		t.Fatal("expected resp.Error to describe conflict")
	}

	// Server should still report not running
	st := s.WebServerStatus()
	if st.Running {
		t.Fatal("expected server to not be running after conflict")
	}

	// Release port and start again
	_ = l.Close()
	resp2, err := s.StartWebServer(ctx, protocol.MsgWebServerControlRequest{
		Action:     protocol.WebServerActionStart,
		Addr:       occupiedAddr,
		DisableTLS: true,
	})
	if err != nil {
		t.Fatalf("expected success after port released, got: %v", err)
	}
	if !resp2.Running {
		t.Fatal("expected resp2.Running to be true")
	}
}

func TestWebServerDisconnectOnlyWebClients(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	inProcTP := transport.NewInProcChannel(32)
	s := server.NewServer(inProcTP, "/bin/sh", "")
	defer s.Close()

	// Start web server
	resp, err := s.StartWebServer(ctx, protocol.MsgWebServerControlRequest{
		Action:     protocol.WebServerActionStart,
		Addr:       "127.0.0.1:0",
		DisableTLS: true,
	})
	if err != nil {
		t.Fatalf("start web server: %v", err)
	}

	// Connect WebSocket client
	wsURL := url.URL{
		Scheme:   "ws",
		Host:     resp.Addr,
		Path:     "/ws",
		RawQuery: fmt.Sprintf("token=%s", resp.Token),
	}
	dialer := websocket.Dialer{
		Subprotocols: []string{fmt.Sprintf("wideboi.v%d", protocol.Version)},
	}
	wsConn, _, err := dialer.Dial(wsURL.String(), nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer wsConn.Close()

	// Wait briefly for attachment
	time.Sleep(50 * time.Millisecond)

	// Stop web server
	_, err = s.StopWebServer()
	if err != nil {
		t.Fatalf("stop web server: %v", err)
	}

	// WebSocket conn should be closed
	wsConn.SetReadDeadline(time.Now().Add(time.Second))
	_, _, err = wsConn.ReadMessage()
	if err == nil {
		t.Fatal("expected websocket connection to be closed")
	}

	// In-proc transport should remain functional
	if !inProcTP.SendClient(ctx, protocol.MsgStatusRequest{}) {
		t.Fatal("in-proc transport failed to send after web server stop")
	}
}

func TestWebServerExplicitTokenReplacementRevokesClients(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := server.NewServer(nil, "/bin/sh", "")
	defer s.Close()

	// 1. Start web server with initial explicit token
	resp1, err := s.StartWebServer(ctx, protocol.MsgWebServerControlRequest{
		Action:     protocol.WebServerActionStart,
		Addr:       "127.0.0.1:0",
		Token:      "initial-token",
		DisableTLS: true,
	})
	if err != nil {
		t.Fatalf("start web server: %v", err)
	}

	// 2. Connect WebSocket client with initial token
	dialer := websocket.Dialer{
		Subprotocols: []string{fmt.Sprintf("wideboi.v%d", protocol.Version)},
	}
	u1 := fmt.Sprintf("ws://%s/ws?token=%s", resp1.Addr, resp1.Token)
	ws1, _, err := dialer.Dial(u1, nil)
	if err != nil {
		t.Fatalf("dial initial websocket: %v", err)
	}
	defer ws1.Close()

	// 3. Replace token with new explicit token (RotateToken is false)
	resp2, err := s.StartWebServer(ctx, protocol.MsgWebServerControlRequest{
		Action:      protocol.WebServerActionStart,
		Token:       "new-explicit-token",
		RotateToken: false,
		DisableTLS:  true,
	})
	if err != nil {
		t.Fatalf("replace token: %v", err)
	}
	if resp2.Token != "new-explicit-token" {
		t.Fatalf("expected token %q, got %q", "new-explicit-token", resp2.Token)
	}

	// 4. Client authenticated with old token must be disconnected
	_ = ws1.SetReadDeadline(time.Now().Add(time.Second))
	_, _, err = ws1.ReadMessage()
	if err == nil {
		t.Fatal("expected client with old token to be disconnected")
	}

	// 5. Reconnecting with old token fails with 401
	_, respHTTP, err := dialer.Dial(u1, nil)
	if err == nil {
		t.Fatal("expected reconnection with old token to fail")
	}
	if respHTTP == nil || respHTTP.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized with old token, got %v", respHTTP)
	}

	// 6. Connecting with new explicit token succeeds
	u2 := fmt.Sprintf("ws://%s/ws?token=%s", resp2.Addr, resp2.Token)
	ws2, _, err := dialer.Dial(u2, nil)
	if err != nil {
		t.Fatalf("dial new token websocket: %v", err)
	}
	defer ws2.Close()
}

func TestWebServerReapplyingSameTokenDoesNotDisconnectClients(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := server.NewServer(nil, "/bin/sh", "")
	defer s.Close()

	// 1. Start web server with token
	resp1, err := s.StartWebServer(ctx, protocol.MsgWebServerControlRequest{
		Action:     protocol.WebServerActionStart,
		Addr:       "127.0.0.1:0",
		Token:      "stable-token",
		DisableTLS: true,
	})
	if err != nil {
		t.Fatalf("start web server: %v", err)
	}

	// 2. Connect client
	dialer := websocket.Dialer{
		Subprotocols: []string{fmt.Sprintf("wideboi.v%d", protocol.Version)},
	}
	u := fmt.Sprintf("ws://%s/ws?token=%s", resp1.Addr, resp1.Token)
	ws, _, err := dialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer ws.Close()

	// 3. Re-apply exact same token without other changes
	resp2, err := s.StartWebServer(ctx, protocol.MsgWebServerControlRequest{
		Action:     protocol.WebServerActionStart,
		Token:      "stable-token",
		DisableTLS: true,
	})
	if err != nil {
		t.Fatalf("reapply token: %v", err)
	}
	if resp2.Token != "stable-token" {
		t.Fatalf("expected token %q, got %q", "stable-token", resp2.Token)
	}

	// 4. Client must remain connected
	attachMsg, _ := protocol.MarshalClient(protocol.MsgAttach{Cols: 80, Rows: 24})
	if err := ws.WriteMessage(websocket.BinaryMessage, attachMsg); err != nil {
		t.Fatalf("expected client to remain connected and writable: %v", err)
	}
}

func TestWebServerGeneratedRotationRevokesClients(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := server.NewServer(nil, "/bin/sh", "")
	defer s.Close()

	// 1. Start web server with generated token
	resp1, err := s.StartWebServer(ctx, protocol.MsgWebServerControlRequest{
		Action:      protocol.WebServerActionStart,
		Addr:        "127.0.0.1:0",
		RotateToken: true,
		DisableTLS:  true,
	})
	if err != nil {
		t.Fatalf("start web server: %v", err)
	}

	// 2. Connect client with initial token
	dialer := websocket.Dialer{
		Subprotocols: []string{fmt.Sprintf("wideboi.v%d", protocol.Version)},
	}
	u1 := fmt.Sprintf("ws://%s/ws?token=%s", resp1.Addr, resp1.Token)
	ws1, _, err := dialer.Dial(u1, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer ws1.Close()

	// 3. Rotate token
	resp2, err := s.StartWebServer(ctx, protocol.MsgWebServerControlRequest{
		Action:      protocol.WebServerActionStart,
		RotateToken: true,
		DisableTLS:  true,
	})
	if err != nil {
		t.Fatalf("rotate token: %v", err)
	}
	if resp2.Token == resp1.Token {
		t.Fatal("expected token to change on rotation")
	}

	// 4. Old client must be disconnected
	_ = ws1.SetReadDeadline(time.Now().Add(time.Second))
	_, _, err = ws1.ReadMessage()
	if err == nil {
		t.Fatal("expected old client to be disconnected after rotation")
	}

	// 5. Old token fails, new token succeeds
	_, respHTTP, err := dialer.Dial(u1, nil)
	if err == nil || respHTTP == nil || respHTTP.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 with old token, got err=%v resp=%v", err, respHTTP)
	}

	u2 := fmt.Sprintf("ws://%s/ws?token=%s", resp2.Addr, resp2.Token)
	ws2, _, err := dialer.Dial(u2, nil)
	if err != nil {
		t.Fatalf("dial new rotated token: %v", err)
	}
	defer ws2.Close()
}

func TestWebServerSimultaneousAddressAndTokenChangeRevokesClients(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := server.NewServer(nil, "/bin/sh", "")
	defer s.Close()

	// 1. Start web server on addr 1
	resp1, err := s.StartWebServer(ctx, protocol.MsgWebServerControlRequest{
		Action:     protocol.WebServerActionStart,
		Addr:       "127.0.0.1:0",
		Token:      "addr-token-1",
		DisableTLS: true,
	})
	if err != nil {
		t.Fatalf("start web server: %v", err)
	}

	// 2. Connect client 1
	dialer := websocket.Dialer{
		Subprotocols: []string{fmt.Sprintf("wideboi.v%d", protocol.Version)},
	}
	u1 := fmt.Sprintf("ws://%s/ws?token=%s", resp1.Addr, resp1.Token)
	ws1, _, err := dialer.Dial(u1, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer ws1.Close()

	// Find an unused port for new address
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	newAddr := l.Addr().String()
	_ = l.Close()

	// 3. Move to new address and new token simultaneously
	resp2, err := s.StartWebServer(ctx, protocol.MsgWebServerControlRequest{
		Action:     protocol.WebServerActionStart,
		Addr:       newAddr,
		Token:      "addr-token-2",
		DisableTLS: true,
	})
	if err != nil {
		t.Fatalf("change address and token: %v", err)
	}

	// 4. Old client must be disconnected
	_ = ws1.SetReadDeadline(time.Now().Add(time.Second))
	_, _, err = ws1.ReadMessage()
	if err == nil {
		t.Fatal("expected old client to be disconnected after address and token change")
	}

	// 5. Connect to new address with new token succeeds
	u2 := fmt.Sprintf("ws://%s/ws?token=%s", resp2.Addr, resp2.Token)
	ws2, _, err := dialer.Dial(u2, nil)
	if err != nil {
		t.Fatalf("dial new address: %v", err)
	}
	defer ws2.Close()
}

func TestWebServerInFlightHandshakeInvalidatedOnTokenChange(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := server.NewServer(nil, "/bin/sh", "")
	defer s.Close()

	resp1, err := s.StartWebServer(ctx, protocol.MsgWebServerControlRequest{
		Action:     protocol.WebServerActionStart,
		Addr:       "127.0.0.1:0",
		Token:      "flight-token-1",
		DisableTLS: true,
	})
	if err != nil {
		t.Fatalf("start web server: %v", err)
	}

	// While we simulate or perform rapid token change during connection:
	// Verify that any connection holding the old epoch cannot survive.
	// Rotate token right as client dials:
	dialer := websocket.Dialer{
		Subprotocols: []string{fmt.Sprintf("wideboi.v%d", protocol.Version)},
	}
	u1 := fmt.Sprintf("ws://%s/ws?token=%s", resp1.Addr, resp1.Token)

	// Perform rotation
	_, err = s.StartWebServer(ctx, protocol.MsgWebServerControlRequest{
		Action:     protocol.WebServerActionStart,
		Token:      "flight-token-2",
		DisableTLS: true,
	})
	if err != nil {
		t.Fatalf("rotate token: %v", err)
	}

	// Dial with the old token
	wsOld, respHTTP, dialErr := dialer.Dial(u1, nil)
	if dialErr == nil {
		defer wsOld.Close()
		// If dial somehow completed before server restarted, read must fail because it got revoked
		_ = wsOld.SetReadDeadline(time.Now().Add(time.Second))
		_, _, readErr := wsOld.ReadMessage()
		if readErr == nil {
			t.Fatal("expected old token connection to be disconnected or refused")
		}
	} else if respHTTP != nil && respHTTP.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %v", respHTTP.StatusCode)
	}
}

func TestWebServerControlRPC(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "control.sock")

	sl, err := transport.NewSocketListener(sockPath)
	if err != nil {
		t.Fatalf("NewSocketListener: %v", err)
	}
	defer sl.Close()

	s := server.NewServer(nil, "/bin/sh", "")
	defer s.Close()

	s.InitWebServer(server.WebServerConfig{
		SocketPath: sockPath,
		TLSEnabled: false,
	})
	s.ListenSocket(ctx, sl)
	go func() { _ = s.Run(ctx) }()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial unix: %v", err)
	}
	defer conn.Close()

	if _, err := transport.Handshake(conn); err != nil {
		t.Fatalf("handshake: %v", err)
	}

	cc := transport.NewClientSocketConn(conn, 256)
	cc.RunPumps(ctx)

	// Send status request
	if !cc.SendClient(ctx, protocol.MsgWebServerControlRequest{Action: protocol.WebServerActionStatus}) {
		t.Fatal("failed to send status request")
	}
	var stResp protocol.MsgWebServerControlResponse
	select {
	case msg := <-cc.ServerSendChan():
		resp, ok := msg.(protocol.MsgWebServerControlResponse)
		if !ok {
			t.Fatalf("expected MsgWebServerControlResponse, got %T", msg)
		}
		stResp = resp
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for status response")
	}
	if stResp.Running {
		t.Fatal("expected web server initially not running")
	}

	// Send start request
	if !cc.SendClient(ctx, protocol.MsgWebServerControlRequest{
		Action:     protocol.WebServerActionStart,
		Addr:       "127.0.0.1:0",
		DisableTLS: true,
	}) {
		t.Fatal("failed to send start request")
	}
	var startResp protocol.MsgWebServerControlResponse
	select {
	case msg := <-cc.ServerSendChan():
		resp, ok := msg.(protocol.MsgWebServerControlResponse)
		if !ok {
			t.Fatalf("expected MsgWebServerControlResponse, got %T", msg)
		}
		startResp = resp
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for start response")
	}
	if !startResp.Running || startResp.Addr == "" || startResp.Token == "" {
		t.Fatalf("unexpected start response: %+v", startResp)
	}

	// Send stop request
	if !cc.SendClient(ctx, protocol.MsgWebServerControlRequest{Action: protocol.WebServerActionStop}) {
		t.Fatal("failed to send stop request")
	}
	select {
	case msg := <-cc.ServerSendChan():
		resp, ok := msg.(protocol.MsgWebServerControlResponse)
		if !ok {
			t.Fatalf("expected MsgWebServerControlResponse, got %T", msg)
		}
		if resp.Running {
			t.Fatalf("expected running false after stop, got %+v", resp)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for stop response")
	}

	// Send unknown action
	if !cc.SendClient(ctx, protocol.MsgWebServerControlRequest{Action: protocol.WebServerAction(999)}) {
		t.Fatal("failed to send unknown action request")
	}
	select {
	case msg := <-cc.ServerSendChan():
		resp, ok := msg.(protocol.MsgWebServerControlResponse)
		if !ok {
			t.Fatalf("expected MsgWebServerControlResponse, got %T", msg)
		}
		if resp.Error == "" {
			t.Fatalf("expected error for unknown action, got %+v", resp)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for unknown action response")
	}
}

func TestWebServerSpecialTokenEscaping(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := server.NewServer(nil, "/bin/sh", "")
	defer s.Close()

	s.InitWebServer(server.WebServerConfig{
		TLSEnabled: false,
	})

	specialToken := "secret#token&with=symbols"
	resp, err := s.StartWebServer(ctx, protocol.MsgWebServerControlRequest{
		Action:     protocol.WebServerActionStart,
		Addr:       "127.0.0.1:0",
		Token:      specialToken,
		DisableTLS: true,
	})
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}

	if !strings.Contains(resp.URL, "token=secret%23token%26with%3Dsymbols") {
		t.Fatalf("expected URL to contain escaped token, got %q", resp.URL)
	}

	// Dial with the special token
	wsURL := fmt.Sprintf("ws://%s/ws?token=%s", resp.Addr, url.QueryEscape(specialToken))
	dialer := websocket.Dialer{
		Subprotocols: []string{fmt.Sprintf("wideboi.v%d", protocol.Version)},
	}
	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial with special token failed: %v", err)
	}
	conn.Close()
}

func TestWebServerRestartConflictPreservesRunningService(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := server.NewServer(nil, "/bin/sh", "")
	defer s.Close()

	s.InitWebServer(server.WebServerConfig{
		TLSEnabled: false,
	})

	// Start successfully on loopback :0
	resp1, err := s.StartWebServer(ctx, protocol.MsgWebServerControlRequest{
		Action:     protocol.WebServerActionStart,
		Addr:       "127.0.0.1:0",
		DisableTLS: true,
	})
	if err != nil {
		t.Fatalf("start 1 failed: %v", err)
	}

	// Occupy another port
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy port: %v", err)
	}
	defer l.Close()
	conflictAddr := l.Addr().String()

	// Connect a client to original server
	wsURL1 := fmt.Sprintf("ws://%s/ws?token=%s", resp1.Addr, resp1.Token)
	dialer := websocket.Dialer{
		Subprotocols: []string{fmt.Sprintf("wideboi.v%d", protocol.Version)},
	}
	conn, _, err := dialer.Dial(wsURL1, nil)
	if err != nil {
		t.Fatalf("dial original server failed: %v", err)
	}
	defer conn.Close()

	// Try to move to conflicting address
	resp2, err := s.StartWebServer(ctx, protocol.MsgWebServerControlRequest{
		Action:     protocol.WebServerActionStart,
		Addr:       conflictAddr,
		DisableTLS: true,
	})
	if err == nil {
		t.Fatal("expected error when moving to conflicting port")
	}
	if resp2.Error == "" {
		t.Fatal("expected error in response")
	}

	// Original server and connection should still be intact
	st := s.WebServerStatus()
	if !st.Running || st.Addr != resp1.Addr {
		t.Fatalf("expected original server to still be running at %s, got: %+v", resp1.Addr, st)
	}

	// Connection should still be open
	conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	_, _, err = conn.ReadMessage()
	// Timeout error means connection is still active and waiting for messages
	if err != nil && !strings.Contains(err.Error(), "timeout") && !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("expected connection to still be alive, got: %v", err)
	}
}

func TestWebServerStartAfterCloseFails(t *testing.T) {
	s := server.NewServer(nil, "/bin/sh", "")
	s.InitWebServer(server.WebServerConfig{
		TLSEnabled: false,
	})
	_ = s.Close()

	resp, err := s.StartWebServer(context.Background(), protocol.MsgWebServerControlRequest{
		Action:     protocol.WebServerActionStart,
		Addr:       "127.0.0.1:0",
		DisableTLS: true,
	})
	if err == nil {
		t.Fatal("expected error starting after server close")
	}
	if !strings.Contains(resp.Error, "shutting down") {
		t.Fatalf("expected shutting down error, got: %+v", resp)
	}
}

func TestWriteWebToken(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "session.sock")
	for _, token := range []string{"first", "rotated"} {
		if err := server.WriteWebToken(socket, token); err != nil {
			t.Fatal(err)
		}
		path := server.WebTokenPath(socket)
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(contents) != token+"\n" {
			t.Fatalf("token file = %q, want %q", contents, token)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("token file permissions = %o, want 600", info.Mode().Perm())
		}
	}
}

func TestExposureWarning(t *testing.T) {
	for _, tc := range []struct {
		name       string
		ip         net.IP
		tlsEnabled bool
		warn       bool
	}{
		{"IPv4 loopback no tls", net.ParseIP("127.0.0.1"), false, false},
		{"IPv6 loopback no tls", net.ParseIP("::1"), false, false},
		{"IPv4 wildcard no tls", net.IPv4zero, false, true},
		{"IPv6 wildcard no tls", net.IPv6zero, false, true},
		{"network address no tls", net.ParseIP("192.0.2.1"), false, true},
		{"IPv4 wildcard with tls", net.IPv4zero, true, false},
		{"IPv6 wildcard with tls", net.IPv6zero, true, false},
		{"network address with tls", net.ParseIP("192.0.2.1"), true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			warn := server.ExposureWarning(&net.TCPAddr{IP: tc.ip, Port: 8080}, tc.tlsEnabled)
			if got := warn != ""; got != tc.warn {
				t.Errorf("warning present = %t, want %t (msg: %q)", got, tc.warn, warn)
			}
		})
	}
}

func TestStartWebServerExposureWarning(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := server.NewServer(nil, "/bin/sh", "")
	defer s.Close()
	s.InitWebServer(server.WebServerConfig{})

	// Start exposed on 0.0.0.0:0 with TLS disabled -> should return warning
	resp, err := s.StartWebServer(ctx, protocol.MsgWebServerControlRequest{
		Action:     protocol.WebServerActionStart,
		Addr:       "0.0.0.0:0",
		DisableTLS: true,
	})
	if err != nil {
		t.Fatalf("StartWebServer: %v", err)
	}
	defer s.StopWebServer()

	if resp.Warning == "" {
		t.Fatal("expected non-empty Warning in response when exposed with TLS disabled")
	}
	if !strings.Contains(resp.Warning, "WARNING: web client is exposed beyond loopback") {
		t.Fatalf("unexpected warning: %q", resp.Warning)
	}

	// Status should also carry the warning
	st := s.WebServerStatus()
	if st.Warning != resp.Warning {
		t.Fatalf("status warning = %q, want %q", st.Warning, resp.Warning)
	}
}

func TestWebServerRotateTokenOnFixedPort(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Find an available port
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	fixedAddr := l.Addr().String()
	_ = l.Close()

	s := server.NewServer(nil, "/bin/sh", "")
	defer s.Close()
	s.InitWebServer(server.WebServerConfig{
		TLSEnabled: true,
	})

	// 1. Start on fixed port
	resp1, err := s.StartWebServer(ctx, protocol.MsgWebServerControlRequest{
		Action: protocol.WebServerActionStart,
		Addr:   fixedAddr,
	})
	if err != nil {
		t.Fatalf("start on fixed port failed: %v", err)
	}
	if !resp1.Running || resp1.Addr != fixedAddr {
		t.Fatalf("unexpected start response: %+v", resp1)
	}

	// Connect WebSocket client
	wsURL1 := fmt.Sprintf("wss://%s/ws?token=%s", resp1.Addr, resp1.Token)
	dialer := websocket.Dialer{
		TLSClientConfig:  &tls.Config{InsecureSkipVerify: true},
		Subprotocols:     []string{fmt.Sprintf("wideboi.v%d", protocol.Version)},
		HandshakeTimeout: 2 * time.Second,
	}
	conn1, _, err := dialer.Dial(wsURL1, nil)
	if err != nil {
		t.Fatalf("dial websocket 1 failed: %v", err)
	}
	defer conn1.Close()

	// 2. Rotate token without specifying addr (or same addr) on the running server
	resp2, err := s.StartWebServer(ctx, protocol.MsgWebServerControlRequest{
		Action:      protocol.WebServerActionStart,
		RotateToken: true,
	})
	if err != nil {
		t.Fatalf("rotate token on fixed port failed: %v", err)
	}
	if !resp2.Running {
		t.Fatal("expected resp2.Running to be true")
	}
	if resp2.Addr != fixedAddr {
		t.Fatalf("expected port to stay %s, got %s", fixedAddr, resp2.Addr)
	}
	if resp2.Token == resp1.Token {
		t.Fatalf("expected rotated token, got same: %s", resp2.Token)
	}

	// Old connection should be closed
	conn1.SetReadDeadline(time.Now().Add(time.Second))
	_, _, err = conn1.ReadMessage()
	if err == nil {
		t.Fatal("expected old connection to be disconnected after token rotation")
	}

	// New connection with rotated token should succeed on the same port
	wsURL2 := fmt.Sprintf("wss://%s/ws?token=%s", resp2.Addr, resp2.Token)
	conn2, _, err := dialer.Dial(wsURL2, nil)
	if err != nil {
		t.Fatalf("dial websocket 2 with rotated token failed: %v", err)
	}
	conn2.Close()
}

func TestWebServerToggleTLS(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := server.NewServer(nil, "/bin/sh", "")
	defer s.Close()
	s.InitWebServer(server.WebServerConfig{
		TLSEnabled: true,
	})

	// 1. Initial start with TLS enabled
	resp1, err := s.StartWebServer(ctx, protocol.MsgWebServerControlRequest{
		Action: protocol.WebServerActionStart,
		Addr:   "127.0.0.1:0",
	})
	if err != nil {
		t.Fatalf("start with TLS failed: %v", err)
	}
	if !resp1.TLSEnabled {
		t.Fatal("expected TLS to be enabled initially")
	}
	if !strings.HasPrefix(resp1.URL, "https://") {
		t.Fatalf("expected https URL, got %s", resp1.URL)
	}

	// Dial with TLS succeeds
	tlsDialer := websocket.Dialer{
		TLSClientConfig:  &tls.Config{InsecureSkipVerify: true},
		Subprotocols:     []string{fmt.Sprintf("wideboi.v%d", protocol.Version)},
		HandshakeTimeout: 2 * time.Second,
	}
	conn1, _, err := tlsDialer.Dial(fmt.Sprintf("wss://%s/ws?token=%s", resp1.Addr, resp1.Token), nil)
	if err != nil {
		t.Fatalf("dial wss failed: %v", err)
	}
	conn1.Close()

	// 2. Disable TLS on same port
	resp2, err := s.StartWebServer(ctx, protocol.MsgWebServerControlRequest{
		Action:     protocol.WebServerActionStart,
		DisableTLS: true,
	})
	if err != nil {
		t.Fatalf("disable TLS failed: %v", err)
	}
	if resp2.TLSEnabled {
		t.Fatal("expected TLS to be disabled")
	}
	if !strings.HasPrefix(resp2.URL, "http://") {
		t.Fatalf("expected http URL, got %s", resp2.URL)
	}
	if resp2.Addr != resp1.Addr {
		t.Fatalf("expected same port %s, got %s", resp1.Addr, resp2.Addr)
	}

	// Dial plain WS succeeds
	plainDialer := websocket.Dialer{
		Subprotocols:     []string{fmt.Sprintf("wideboi.v%d", protocol.Version)},
		HandshakeTimeout: 2 * time.Second,
	}
	conn2, _, err := plainDialer.Dial(fmt.Sprintf("ws://%s/ws?token=%s", resp2.Addr, resp2.Token), nil)
	if err != nil {
		t.Fatalf("dial ws failed: %v", err)
	}
	conn2.Close()

	// 3. Re-enable TLS on same port
	resp3, err := s.StartWebServer(ctx, protocol.MsgWebServerControlRequest{
		Action:    protocol.WebServerActionStart,
		EnableTLS: true,
	})
	if err != nil {
		t.Fatalf("re-enable TLS failed: %v", err)
	}
	if !resp3.TLSEnabled {
		t.Fatal("expected TLS to be re-enabled")
	}
	if !strings.HasPrefix(resp3.URL, "https://") {
		t.Fatalf("expected https URL, got %s", resp3.URL)
	}
	if resp3.Addr != resp1.Addr {
		t.Fatalf("expected same port %s, got %s", resp1.Addr, resp3.Addr)
	}

	// Dial with TLS succeeds again
	conn3, _, err := tlsDialer.Dial(fmt.Sprintf("wss://%s/ws?token=%s", resp3.Addr, resp3.Token), nil)
	if err != nil {
		t.Fatalf("dial wss after re-enabling TLS failed: %v", err)
	}
	conn3.Close()
}

func TestWebServerConflictingTLSOptions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := server.NewServer(nil, "/bin/sh", "")
	defer s.Close()
	s.InitWebServer(server.WebServerConfig{})

	_, err := s.StartWebServer(ctx, protocol.MsgWebServerControlRequest{
		Action:     protocol.WebServerActionStart,
		Addr:       "127.0.0.1:0",
		EnableTLS:  true,
		DisableTLS: true,
	})
	if err == nil {
		t.Fatal("expected error with both EnableTLS and DisableTLS")
	}
	if !strings.Contains(err.Error(), "cannot specify both") {
		t.Fatalf("unexpected error message: %v", err)
	}
}
