package server_test

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
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
