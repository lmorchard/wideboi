package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/config"
	"github.com/lmorchard/wideboi/internal/server"
	"github.com/lmorchard/wideboi/internal/transport"
)

func TestControlSubcommands(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "control.sock")

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

	cfg := config.Config{
		Socket: sockPath,
	}

	// 1. Split a new pane with a command that prints a known message and waits
	var splitOut, splitErr bytes.Buffer
	err = runSplit(cfg, []string{"echo hello from split && sleep 5"}, &splitOut, &splitErr)
	if err != nil {
		t.Fatalf("runSplit failed: %v, stderr: %s", err, splitErr.String())
	}

	paneIDStr := strings.TrimSpace(splitOut.String())
	if paneIDStr == "" {
		t.Fatal("expected pane ID from runSplit, got empty output")
	}

	// Wait briefly for the child process to write output to the PTY
	var captureOut, captureErr bytes.Buffer
	deadline := time.Now().Add(3 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		captureOut.Reset()
		captureErr.Reset()
		if err := runCapture(cfg, []string{paneIDStr}, &captureOut, &captureErr); err == nil {
			if strings.Contains(captureOut.String(), "hello from split") {
				found = true
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !found {
		t.Fatalf("expected captured text to contain 'hello from split', got %q", captureOut.String())
	}

	// 2. Test send with trailing -e flag
	var sendErr bytes.Buffer
	err = runSend(cfg, []string{paneIDStr, "echo extra", "-e"}, &sendErr)
	if err != nil {
		t.Fatalf("runSend failed: %v, stderr: %s", err, sendErr.String())
	}

	// 3. Test send to unknown pane
	sendErr.Reset()
	err = runSend(cfg, []string{"999", "echo extra"}, &sendErr)
	if err == nil {
		t.Fatal("expected error sending to unknown pane 999, got nil")
	}

	// 4. Test capture from unknown pane
	captureOut.Reset()
	captureErr.Reset()
	err = runCapture(cfg, []string{"999"}, &captureOut, &captureErr)
	if err == nil {
		t.Fatal("expected error capturing from unknown pane 999, got nil")
	}

	// 5. Test close
	var closeErr bytes.Buffer
	err = runClose(cfg, []string{paneIDStr}, &closeErr)
	if err != nil {
		t.Fatalf("runClose failed: %v, stderr: %s", err, closeErr.String())
	}

	// 6. Test close on already closed pane
	closeErr.Reset()
	err = runClose(cfg, []string{paneIDStr}, &closeErr)
	if err == nil {
		t.Fatal("expected error closing already closed pane, got nil")
	}
}

func TestReorderFlags(t *testing.T) {
	tests := []struct {
		in   []string
		want []string
	}{
		{
			in:   []string{"1", "hello", "-e"},
			want: []string{"-e", "1", "hello"},
		},
		{
			in:   []string{"1", "-S", "-n", "10"},
			want: []string{"-S", "-n", "10", "1"},
		},
		{
			in:   []string{"1", "--", "-literal"},
			want: []string{"1", "-literal"},
		},
		{
			in:   []string{"--cwd", "/tmp", "-L", "test", "echo", "hi"},
			want: []string{"--cwd", "/tmp", "-L", "test", "echo", "hi"},
		},
	}
	for _, tt := range tests {
		got := reorderFlags(tt.in)
		if strings.Join(got, " ") != strings.Join(tt.want, " ") {
			t.Errorf("reorderFlags(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
