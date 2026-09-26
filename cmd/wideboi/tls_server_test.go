package main

import (
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lmorchard/wideboi/internal/config"
	"github.com/lmorchard/wideboi/internal/protocol"
)

func getFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("get free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestTLSServerStartupAndConnect(t *testing.T) {
	port := getFreePort(t)
	sockPath := filepath.Join(shortTempDir(t), "s.sock")
	token := "tls-test-token"

	cfg := config.Config{
		Socket:             sockPath,
		Websocket:          fmt.Sprintf("127.0.0.1:%d", port),
		WebsocketToken:     token,
		TLSEnabled:         true,
		AutoCleanupEnabled: false,
	}

	serverDone := make(chan error, 1)
	go func() {
		serverDone <- runServer(cfg, -1)
	}()

	// Wait for server to listen
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(5 * time.Second)
	tlsClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		Timeout: 500 * time.Millisecond,
	}

	ready := false
	for time.Now().Before(deadline) {
		resp, err := tlsClient.Get(fmt.Sprintf("https://%s/", addr))
		if err == nil {
			resp.Body.Close()
			ready = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ready {
		_ = runKillSession(cfg)
		t.Fatalf("server failed to answer HTTPS on %s within deadline", addr)
	}

	// 1. Plain HTTP to HTTPS port returns 400 Bad Request or connection error
	httpClient := &http.Client{Timeout: 500 * time.Millisecond}
	httpResp, err := httpClient.Get(fmt.Sprintf("http://%s/", addr))
	if err == nil {
		defer httpResp.Body.Close()
		if httpResp.StatusCode != http.StatusBadRequest {
			t.Errorf("plain HTTP request to HTTPS server returned status %d, want 400 Bad Request", httpResp.StatusCode)
		}
	}

	// 2. HTTPS request returns 200 OK
	resp, err := tlsClient.Get(fmt.Sprintf("https://%s/", addr))
	if err != nil {
		t.Fatalf("HTTPS GET / failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("HTTPS GET status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "html") {
		t.Errorf("expected HTML body, got: %s", string(body))
	}

	// 3. Connect WebSocket over WSS
	wsURL := fmt.Sprintf("wss://%s/ws", addr)
	protocols := []string{
		"wideboi-token." + base64.RawURLEncoding.EncodeToString([]byte(token)),
		fmt.Sprintf("wideboi.v%d", protocol.Version),
	}
	dialer := websocket.Dialer{
		Subprotocols:    protocols,
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	wsConn, wsResp, err := dialer.Dial(wsURL, nil)
	if err != nil {
		if wsResp != nil {
			t.Fatalf("WSS dial failed with status %d: %v", wsResp.StatusCode, err)
		}
		t.Fatalf("WSS dial failed: %v", err)
	}
	_ = wsConn.Close()

	if err := runKillSession(cfg); err != nil {
		t.Fatalf("runKillSession error: %v", err)
	}

	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("runServer returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runServer did not exit on kill-session")
	}
}

func TestDisabledTLSServerStartupAndConnect(t *testing.T) {
	port := getFreePort(t)
	sockPath := filepath.Join(shortTempDir(t), "s.sock")
	token := "no-tls-token"

	cfg := config.Config{
		Socket:             sockPath,
		Websocket:          fmt.Sprintf("127.0.0.1:%d", port),
		WebsocketToken:     token,
		TLSEnabled:         false,
		AutoCleanupEnabled: false,
	}

	serverDone := make(chan error, 1)
	go func() {
		serverDone <- runServer(cfg, -1)
	}()

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(5 * time.Second)
	httpClient := &http.Client{Timeout: 500 * time.Millisecond}

	ready := false
	for time.Now().Before(deadline) {
		resp, err := httpClient.Get(fmt.Sprintf("http://%s/", addr))
		if err == nil {
			resp.Body.Close()
			ready = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ready {
		_ = runKillSession(cfg)
		t.Fatalf("server failed to answer HTTP on %s within deadline", addr)
	}

	// Plain HTTP succeeds
	resp, err := httpClient.Get(fmt.Sprintf("http://%s/", addr))
	if err != nil {
		t.Fatalf("HTTP GET / failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("HTTP GET status = %d, want 200", resp.StatusCode)
	}

	if err := runKillSession(cfg); err != nil {
		t.Fatalf("runKillSession error: %v", err)
	}

	select {
	case <-serverDone:
	case <-time.After(3 * time.Second):
		t.Fatal("runServer did not exit on kill-session")
	}
}
