package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lmorchard/wideboi/internal/config"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/server"
	"github.com/lmorchard/wideboi/internal/transport"
)

func TestWebCommandE2E(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "web.sock")

	sl, err := transport.NewSocketListener(sockPath)
	if err != nil {
		t.Fatalf("NewSocketListener failed: %v", err)
	}
	defer sl.Close()

	srv := server.NewServer(nil, "/bin/sh", "")
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv.InitWebServer(server.WebServerConfig{
		SocketPath: sockPath,
		TLSEnabled: true,
	})
	srv.ListenSocket(ctx, sl)
	go func() {
		_ = srv.Run(ctx)
	}()

	cfg := config.Config{
		Socket: sockPath,
	}

	// 1. Initial status -> disabled
	var out, errOut bytes.Buffer
	err = runWeb(cfg, []string{"status"}, &out, &errOut)
	if err != nil {
		t.Fatalf("runWeb status failed: %v", err)
	}
	if !strings.Contains(out.String(), "web server: disabled") {
		t.Fatalf("expected web server: disabled, got: %q", out.String())
	}

	// 2. Initial status --json -> running: false
	out.Reset()
	errOut.Reset()
	err = runWeb(cfg, []string{"status", "--json"}, &out, &errOut)
	if err != nil {
		t.Fatalf("runWeb status --json failed: %v", err)
	}
	var jsonStatus protocol.MsgWebServerControlResponse
	if err := json.Unmarshal(out.Bytes(), &jsonStatus); err != nil {
		t.Fatalf("json unmarshal failed: %v (raw: %s)", err, out.String())
	}
	if jsonStatus.Running {
		t.Fatal("expected Running=false")
	}

	// 3. Start web server on loopback :0
	out.Reset()
	errOut.Reset()
	err = runWeb(cfg, []string{"start", "--addr", "127.0.0.1:0"}, &out, &errOut)
	if err != nil {
		t.Fatalf("runWeb start failed: %v", err)
	}
	if !strings.Contains(out.String(), "wideboi: web client listening at https://") {
		t.Fatalf("unexpected start output: %q", out.String())
	}

	// 4. Query status -> enabled
	out.Reset()
	errOut.Reset()
	err = runWeb(cfg, []string{"status"}, &out, &errOut)
	if err != nil {
		t.Fatalf("runWeb status failed: %v", err)
	}
	if !strings.Contains(out.String(), "web server: enabled") || !strings.Contains(out.String(), "tls:        enabled") {
		t.Fatalf("unexpected status output: %q", out.String())
	}

	// 5. Query status via `wideboi status --json` -> includes web_server
	out.Reset()
	err = runStatus(cfg, true, &out)
	if err != nil {
		t.Fatalf("runStatus --json failed: %v", err)
	}
	var overallStatus statusOutput
	if err := json.Unmarshal(out.Bytes(), &overallStatus); err != nil {
		t.Fatalf("overall status unmarshal failed: %v", err)
	}
	if overallStatus.WebServer == nil || !overallStatus.WebServer.Running {
		t.Fatalf("expected overallStatus.WebServer.Running=true, got %+v", overallStatus.WebServer)
	}

	// 6. Stop web server
	out.Reset()
	errOut.Reset()
	err = runWeb(cfg, []string{"stop"}, &out, &errOut)
	if err != nil {
		t.Fatalf("runWeb stop failed: %v", err)
	}
	if !strings.Contains(out.String(), "wideboi: web server stopped") {
		t.Fatalf("unexpected stop output: %q", out.String())
	}

	// 7. Verify stopped
	out.Reset()
	errOut.Reset()
	err = runWeb(cfg, []string{"status"}, &out, &errOut)
	if err != nil {
		t.Fatalf("runWeb status failed: %v", err)
	}
	if !strings.Contains(out.String(), "web server: disabled") {
		t.Fatalf("expected web server: disabled after stop, got: %q", out.String())
	}
}
