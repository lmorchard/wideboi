package main

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

	// Verify unknown files are kept
	if _, err := os.Stat(filepath.Join(dir, "unknown.txt")); err != nil {
		t.Errorf("unknown.txt should be kept: %v", err)
	}
}
