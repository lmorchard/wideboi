package main

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/config"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

func init() {
	if os.Getenv("TEST_SERVER_AUTOCLEANUP_CHILD") == "1" {
		cfg := config.Config{
			Socket:             os.Getenv("TEST_SERVER_SOCKET"),
			AutoCleanupEnabled: true,
			LogLevel:           slog.LevelInfo,
			Shell:              "/bin/sh",
		}
		_ = runServer(cfg, -1)
		os.Exit(0)
	}
}

func TestRunCleanup(t *testing.T) {
	dir := t.TempDir()

	// 1. Create legacy logs
	os.WriteFile(filepath.Join(dir, "server.log"), []byte("legacy server"), 0644)
	os.WriteFile(filepath.Join(dir, "client.log"), []byte("legacy client"), 0644)

	// 2. Create dead session artifacts
	os.WriteFile(filepath.Join(dir, "dead.sock"), []byte("socket bytes"), 0644)
	os.WriteFile(filepath.Join(dir, "dead.server.log"), []byte("dead server"), 0644)
	os.WriteFile(filepath.Join(dir, "dead.client.log"), []byte("dead client"), 0644)
	os.WriteFile(filepath.Join(dir, "dead.sock.lock"), []byte("lock bytes"), 0644)
	os.WriteFile(filepath.Join(dir, "dead.web-token"), []byte("old secret"), 0600)

	// 3. Create active session artifacts
	activeSockPath := filepath.Join(dir, "active.sock")
	l, err := net.Listen("unix", activeSockPath)
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer l.Close()
	os.WriteFile(filepath.Join(dir, "active.server.log"), []byte("active server"), 0644)
	os.WriteFile(filepath.Join(dir, "active.client.log"), []byte("active client"), 0644)
	os.WriteFile(filepath.Join(dir, "active.sock.lock"), []byte("lock bytes"), 0644)
	os.WriteFile(filepath.Join(dir, "active.web-token"), []byte("current secret"), 0600)

	// 4. Create stray/unknown files
	os.WriteFile(filepath.Join(dir, "unknown.txt"), []byte("unknown"), 0644)

	var buf bytes.Buffer
	err = runCleanup(&buf, dir)
	if err != nil {
		t.Fatalf("runCleanup failed: %v", err)
	}

	out := buf.String()

	// Verify legacy logs are removed
	if _, err := os.Stat(filepath.Join(dir, "server.log")); !os.IsNotExist(err) {
		t.Errorf("server.log should be removed")
	}
	if _, err := os.Stat(filepath.Join(dir, "client.log")); !os.IsNotExist(err) {
		t.Errorf("client.log should be removed")
	}
	if !strings.Contains(out, "removed legacy server.log") {
		t.Errorf("expected output for server.log, got: %q", out)
	}

	// Verify dead artifacts are removed (but lock remains)
	if _, err := os.Stat(filepath.Join(dir, "dead.sock")); !os.IsNotExist(err) {
		t.Errorf("dead.sock should be removed")
	}
	if _, err := os.Stat(filepath.Join(dir, "dead.server.log")); !os.IsNotExist(err) {
		t.Errorf("dead.server.log should be removed")
	}
	if _, err := os.Stat(filepath.Join(dir, "dead.client.log")); !os.IsNotExist(err) {
		t.Errorf("dead.client.log should be removed")
	}
	if _, err := os.Stat(filepath.Join(dir, "dead.sock.lock")); err != nil {
		t.Errorf("dead.sock.lock should be kept: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "dead.web-token")); !os.IsNotExist(err) {
		t.Errorf("dead.web-token should be removed")
	}

	// Verify active artifacts are kept
	if _, err := os.Stat(activeSockPath); err != nil {
		t.Errorf("active.sock should be kept: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "active.server.log")); err != nil {
		t.Errorf("active.server.log should be kept: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "active.client.log")); err != nil {
		t.Errorf("active.client.log should be kept: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "active.web-token")); err != nil {
		t.Errorf("active.web-token should be kept: %v", err)
	}

	// Verify unknown files are kept
	if _, err := os.Stat(filepath.Join(dir, "unknown.txt")); err != nil {
		t.Errorf("unknown.txt should be kept: %v", err)
	}
}

func TestServerAutoCleanup(t *testing.T) {
	// Case 1: AutoCleanupEnabled = true on clean shutdown deletes logs
	{
		dir := t.TempDir()
		sockPath := filepath.Join(dir, "clean.sock")
		clientLog := filepath.Join(dir, "clean.client.log")
		if err := os.WriteFile(clientLog, []byte("client log bytes\n"), 0644); err != nil {
			t.Fatal(err)
		}

		cfg := config.Config{
			Socket:             sockPath,
			AutoCleanupEnabled: true,
			LogLevel:           slog.LevelInfo,
			Shell:              "/bin/sh",
		}

		errCh := make(chan error, 1)
		go func() {
			errCh <- runServer(cfg, -1)
		}()

		// Dial until server is listening
		var conn net.Conn
		var err error
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			conn, err = net.Dial("unix", sockPath)
			if err == nil {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if err != nil {
			t.Fatalf("failed to connect to server socket: %v", err)
		}

		if _, err := transport.Handshake(conn); err != nil {
			conn.Close()
			t.Fatalf("handshake failed: %v", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		cc := transport.NewClientSocketConn(conn, 256)
		cc.RunPumps(ctx)

		// Send MsgShutdown and wait for server hangup
		if !hangUp(ctx, cc, protocol.MsgShutdown{}, shutdownCeiling) {
			t.Fatalf("hangUp failed")
		}
		cancel()

		select {
		case srvErr := <-errCh:
			if srvErr != nil {
				t.Fatalf("runServer returned error: %v", srvErr)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for runServer to exit")
		}

		// Verify server.log and client.log are removed
		serverLog := filepath.Join(dir, "clean.server.log")
		if _, err := os.Stat(serverLog); !os.IsNotExist(err) {
			t.Errorf("clean.server.log should be removed by auto-cleanup")
		}
		if _, err := os.Stat(clientLog); !os.IsNotExist(err) {
			t.Errorf("clean.client.log should be removed by auto-cleanup")
		}
	}

	// Case 2: AutoCleanupEnabled = false preserves logs
	{
		dir := t.TempDir()
		sockPath := filepath.Join(dir, "noclean.sock")
		clientLog := filepath.Join(dir, "noclean.client.log")
		if err := os.WriteFile(clientLog, []byte("client log bytes\n"), 0644); err != nil {
			t.Fatal(err)
		}

		cfg := config.Config{
			Socket:             sockPath,
			AutoCleanupEnabled: false,
			LogLevel:           slog.LevelInfo,
			Shell:              "/bin/sh",
		}

		errCh := make(chan error, 1)
		go func() {
			errCh <- runServer(cfg, -1)
		}()

		var conn net.Conn
		var err error
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			conn, err = net.Dial("unix", sockPath)
			if err == nil {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if err != nil {
			t.Fatalf("failed to connect to server socket: %v", err)
		}

		if _, err := transport.Handshake(conn); err != nil {
			conn.Close()
			t.Fatalf("handshake failed: %v", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		cc := transport.NewClientSocketConn(conn, 256)
		cc.RunPumps(ctx)

		if !hangUp(ctx, cc, protocol.MsgShutdown{}, shutdownCeiling) {
			t.Fatalf("hangUp failed")
		}
		cancel()

		select {
		case srvErr := <-errCh:
			if srvErr != nil {
				t.Fatalf("runServer returned error: %v", srvErr)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for runServer to exit")
		}

		serverLog := filepath.Join(dir, "noclean.server.log")
		if _, err := os.Stat(serverLog); err != nil {
			t.Errorf("noclean.server.log should be kept when auto_cleanup is false: %v", err)
		}
		if _, err := os.Stat(clientLog); err != nil {
			t.Errorf("noclean.client.log should be kept when auto_cleanup is false: %v", err)
		}
	}

	// Case 3: Signal termination preserves logs even when AutoCleanupEnabled = true
	{
		dir := t.TempDir()
		sockPath := filepath.Join(dir, "sig.sock")
		clientLog := filepath.Join(dir, "sig.client.log")
		if err := os.WriteFile(clientLog, []byte("client log bytes\n"), 0644); err != nil {
			t.Fatal(err)
		}

		cmd := exec.Command(os.Args[0], "-test.run=TestServerAutoCleanup")
		cmd.Env = append(os.Environ(),
			"TEST_SERVER_AUTOCLEANUP_CHILD=1",
			"TEST_SERVER_SOCKET="+sockPath,
		)
		if err := cmd.Start(); err != nil {
			t.Fatalf("start subprocess: %v", err)
		}

		deadline := time.Now().Add(3 * time.Second)
		var conn net.Conn
		var err error
		for time.Now().Before(deadline) {
			conn, err = net.Dial("unix", sockPath)
			if err == nil {
				conn.Close()
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if err != nil {
			_ = cmd.Process.Kill()
			t.Fatalf("failed to dial child server: %v", err)
		}

		// Send SIGTERM to the child process
		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
			t.Fatalf("send SIGTERM: %v", err)
		}

		_ = cmd.Wait()

		serverLog := filepath.Join(dir, "sig.server.log")
		if _, err := os.Stat(serverLog); err != nil {
			t.Errorf("sig.server.log should be kept when server exits by signal: %v", err)
		}
		if _, err := os.Stat(clientLog); err != nil {
			t.Errorf("sig.client.log should be kept when server exits by signal: %v", err)
		}
	}

	// Case 4: Server exit with error preserves logs even when AutoCleanupEnabled = true
	{
		dir := t.TempDir()
		sockPath := filepath.Join(dir, "err.sock")
		clientLog := filepath.Join(dir, "err.client.log")
		if err := os.WriteFile(clientLog, []byte("client log bytes\n"), 0644); err != nil {
			t.Fatal(err)
		}

		cfg := config.Config{
			Socket:             sockPath,
			AutoCleanupEnabled: true,
			LogLevel:           slog.LevelInfo,
			Shell:              "/bin/sh",
			// Invalid websocket address causes runServer to return error
			Websocket: "invalid-host-that-cannot-listen:99999",
		}

		err := runServer(cfg, -1)
		if err == nil {
			t.Fatal("expected runServer to fail with invalid websocket address")
		}

		serverLog := filepath.Join(dir, "err.server.log")
		if _, err := os.Stat(serverLog); err != nil {
			t.Errorf("err.server.log should be kept on error exit: %v", err)
		}
		if _, err := os.Stat(clientLog); err != nil {
			t.Errorf("err.client.log should be kept on error exit: %v", err)
		}
	}
}
