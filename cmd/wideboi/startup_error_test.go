package main

import (
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lmorchard/wideboi/internal/config"
	"github.com/lmorchard/wideboi/internal/logger"
)

func TestParseSlogAttr(t *testing.T) {
	tests := []struct {
		name string
		line string
		key  string
		want string
	}{
		{
			name: "quoted value with spaces",
			line: `time=2026-09-24T18:25:51.170-07:00 level=ERROR msg="cannot listen on websocket address" err="listen tcp :8089: bind: address already in use"`,
			key:  "msg",
			want: "cannot listen on websocket address",
		},
		{
			name: "quoted error value",
			line: `time=2026-09-24T18:25:51.170-07:00 level=ERROR msg="cannot listen on websocket address" err="listen tcp :8089: bind: address already in use"`,
			key:  "err",
			want: "listen tcp :8089: bind: address already in use",
		},
		{
			name: "unquoted value",
			line: `time=2026-09-24T18:25:51.170-07:00 level=ERROR msg="handshake failed" err=EOF`,
			key:  "err",
			want: "EOF",
		},
		{
			name: "escaped quotes inside value",
			line: `time=... level=ERROR msg="failed \"nested\" test"`,
			key:  "msg",
			want: `failed "nested" test`,
		},
		{
			name: "key not found",
			line: `time=... level=INFO msg="starting"`,
			key:  "err",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseSlogAttr(tt.line, tt.key); got != tt.want {
				t.Errorf("parseSlogAttr(%q, %q) = %q, want %q", tt.line, tt.key, got, tt.want)
			}
		})
	}
}

func TestExtractStartupReason(t *testing.T) {
	dir := t.TempDir()

	// Case 1: Slog error with msg and err
	logPath1 := filepath.Join(dir, "slog_err.log")
	content1 := `time=2026-09-24T18:25:51.168-07:00 level=INFO msg="starting wideboi server" socketPath=/tmp/test.sock ownerFD=3
time=2026-09-24T18:25:51.170-07:00 level=ERROR msg="cannot listen on websocket address" err="listen tcp :8089: bind: address already in use"
`
	if err := os.WriteFile(logPath1, []byte(content1), 0600); err != nil {
		t.Fatal(err)
	}
	want1 := "cannot listen on websocket address: listen tcp :8089: bind: address already in use"
	if got := extractStartupReason(logPath1); got != want1 {
		t.Errorf("extractStartupReason() = %q, want %q", got, want1)
	}

	// Case 2: Panic trace
	logPath2 := filepath.Join(dir, "panic.log")
	content2 := `time=... level=INFO msg="starting"
panic: nil pointer dereference
goroutine 1 [running]:
main.runServer(...)
`
	if err := os.WriteFile(logPath2, []byte(content2), 0600); err != nil {
		t.Fatal(err)
	}
	want2 := "panic: nil pointer dereference"
	if got := extractStartupReason(logPath2); got != want2 {
		t.Errorf("extractStartupReason() = %q, want %q", got, want2)
	}

	// Case 3: wideboi fatal line
	logPath3 := filepath.Join(dir, "fatal.log")
	content3 := `wideboi: failed to bind socket: permission denied
`
	if err := os.WriteFile(logPath3, []byte(content3), 0600); err != nil {
		t.Fatal(err)
	}
	want3 := "failed to bind socket: permission denied"
	if got := extractStartupReason(logPath3); got != want3 {
		t.Errorf("extractStartupReason() = %q, want %q", got, want3)
	}

	// Case 4: Nonexistent file returns empty
	if got := extractStartupReason(filepath.Join(dir, "nonexistent.log")); got != "" {
		t.Errorf("extractStartupReason(nonexistent) = %q, want empty", got)
	}
}

func TestStartupExitError(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "test.sock")
	logPath := filepath.Join(dir, "test.server.log")

	// When log contains an error
	_ = os.WriteFile(logPath, []byte(`time=... level=ERROR msg="cannot listen" err="port taken"`+"\n"), 0600)
	err := startupExitError(sock)
	if err == nil {
		t.Fatal("startupExitError returned nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "cannot listen: port taken") {
		t.Errorf("startupExitError() missing error reason: %s", msg)
	}
	if !strings.Contains(msg, logPath) {
		t.Errorf("startupExitError() missing log path: %s", msg)
	}

	// When log file doesn't exist
	_ = os.Remove(logPath)
	err = startupExitError(sock)
	if err == nil {
		t.Fatal("startupExitError returned nil")
	}
	msg = err.Error()
	if !strings.HasPrefix(msg, "wideboi server exited during startup; see ") {
		t.Errorf("startupExitError() unexpected format without reason: %s", msg)
	}
}

func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "wb")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestStartupWebsocketCollisionReportsReason(t *testing.T) {
	// 1. Occupy a local TCP port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen failed: %v", err)
	}
	defer ln.Close()

	dir := shortTempDir(t)
	sockPath := filepath.Join(dir, "col.sock")

	cfg := config.Config{
		Socket:             sockPath,
		AutoCleanupEnabled: true,
		LogLevel:           slog.LevelInfo,
		Shell:              "/bin/sh",
		Websocket:          ln.Addr().String(),
	}

	// 2. Start server - should fail to bind WebSocket
	srvErr := runServer(cfg, -1)
	if srvErr == nil {
		t.Fatal("expected runServer to fail with occupied port")
	}

	// 3. Verify startupExitError extracts the reason from the actual generated server log
	exitErr := startupExitError(sockPath)
	if exitErr == nil {
		t.Fatal("startupExitError returned nil")
	}
	msg := exitErr.Error()
	if !strings.Contains(msg, "cannot listen on websocket address") {
		t.Errorf("startupExitError missing 'cannot listen on websocket address': %s", msg)
	}
	if !strings.Contains(msg, "address already in use") {
		t.Errorf("startupExitError missing 'address already in use': %s", msg)
	}

	// 4. Verify log was preserved and not deleted by auto-cleanup
	serverLog := logger.Path(sockPath, "server")
	if _, err := os.Stat(serverLog); err != nil {
		t.Errorf("server log should be kept on startup exit: %v", err)
	}
}
