package main

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/config"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/server"
	"github.com/lmorchard/wideboi/internal/transport"
)

func TestPaletteCancelEsc(t *testing.T) {
	var in bytes.Buffer
	var out, errOut bytes.Buffer

	in.WriteString("\x1b") // Esc key
	cfg := paletteTestConfig(t)

	err := runPalette(cfg, []string{"--caller-pane=1"}, &in, &out, &errOut)
	if err != nil {
		t.Fatalf("runPalette with Esc returned error: %v", err)
	}
}

func TestPaletteCancelCtrlC(t *testing.T) {
	var in bytes.Buffer
	var out, errOut bytes.Buffer

	in.WriteString("\x03") // Ctrl+C
	cfg := paletteTestConfig(t)

	err := runPalette(cfg, []string{"--caller-pane=1"}, &in, &out, &errOut)
	if err != nil {
		t.Fatalf("runPalette with Ctrl+C returned error: %v", err)
	}
}

func TestPaletteFilterAndRender(t *testing.T) {
	var in bytes.Buffer
	var out, errOut bytes.Buffer

	// Type "quit" and press Enter
	in.WriteString("quit\n")
	cfg := paletteTestConfig(t)

	_ = runPalette(cfg, []string{"--caller-pane=1"}, &in, &out, &errOut)
	// Render output should display query and matching command
	output := out.String()
	if !strings.Contains(output, "quit") {
		t.Errorf("palette output missing 'quit', got:\n%s", output)
	}
}

func TestPaletteFuzzySubsequence(t *testing.T) {
	var in bytes.Buffer
	var out, errOut bytes.Buffer

	// Type subsequence "ncl" which should match "new-column"
	in.WriteString("ncl\n")
	cfg := paletteTestConfig(t)

	_ = runPalette(cfg, []string{"--caller-pane=1"}, &in, &out, &errOut)
	output := out.String()
	if !strings.Contains(output, "new-column") {
		t.Errorf("palette output missing 'new-column' for fuzzy query 'ncl', got:\n%s", output)
	}
}

func TestBuildExecLine(t *testing.T) {
	tests := []struct {
		cmdName  string
		rawQuery string
		want     string
	}{
		{"set-width", "width 60", "set-width 60"},
		{"set-width", "set-width 80", "set-width 80"},
		{"set-width", "width", "set-width"},
		{"detach", "detach", "detach"},
		{"detach", "d", "detach"},
		{"run", "run printf '%s' 'hello'", "run printf '%s' 'hello'"},
		{"run", "run   echo 'a  b'", "run echo 'a  b'"},
	}

	for _, tc := range tests {
		got := buildExecLine(tc.cmdName, tc.rawQuery)
		if got != tc.want {
			t.Errorf("buildExecLine(%q, %q) = %q, want %q", tc.cmdName, tc.rawQuery, got, tc.want)
		}
	}
}

func TestPaletteQueryWithArguments(t *testing.T) {
	var in bytes.Buffer
	var out, errOut bytes.Buffer

	in.WriteString("width 65\n")
	cfg := paletteTestConfig(t)

	_ = runPalette(cfg, []string{"--caller-pane=1"}, &in, &out, &errOut)
	output := out.String()
	if !strings.Contains(output, "set-width") {
		t.Errorf("palette output missing 'set-width' for query 'width 65', got:\n%s", output)
	}
}

func TestPaletteExecutesSetWidth(t *testing.T) {
	dir := shortTempDir(t)
	sock := filepath.Join(dir, "s.sock")
	sl, err := transport.NewSocketListener(sock)
	if err != nil {
		t.Fatalf("NewSocketListener: %v", err)
	}
	defer sl.Close()

	s := server.NewServer(nil, "/bin/sh", "")
	defer s.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.ListenSocket(ctx, sl)
	go func() { _ = s.Run(ctx) }()

	// Attach a client so the server has active session geometry and default panes
	clientConn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial client: %v", err)
	}
	defer clientConn.Close()
	if _, err := transport.Handshake(clientConn); err != nil {
		t.Fatalf("handshake client: %v", err)
	}
	if err := transport.WriteClientFrame(clientConn, protocol.MsgAttach{Cols: 100, Rows: 30}); err != nil {
		t.Fatalf("write attach: %v", err)
	}

	// Read until initial layout snapshot arrives
	var paneID int
	_ = clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		msg, err := transport.ReadServerFrame(clientConn)
		if err != nil {
			t.Fatalf("reading initial server frame: %v", err)
		}
		if snap, ok := msg.(protocol.MsgLayoutSnapshot); ok && len(snap.Columns) > 0 {
			paneID = snap.Columns[0].PaneID
			break
		}
	}

	var in bytes.Buffer
	var out, errOut bytes.Buffer
	in.WriteString("width 65\n")
	cfg := config.Config{Socket: sock}

	err = runPalette(cfg, []string{fmt.Sprintf("--caller-pane=%d", paneID), "--socket=" + sock}, &in, &out, &errOut)
	if err != nil {
		t.Fatalf("runPalette failed: %v", err)
	}

	// Read frames until layout snapshot with updated width arrives
	var updatedWidth int
	_ = clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		msg, err := transport.ReadServerFrame(clientConn)
		if err != nil {
			t.Fatalf("reading server frame after palette set-width: %v", err)
		}
		if snap, ok := msg.(protocol.MsgLayoutSnapshot); ok {
			for _, col := range snap.Columns {
				if col.PaneID == paneID && col.Width == 65 {
					updatedWidth = col.Width
					break
				}
			}
			if updatedWidth == 65 {
				break
			}
		}
	}

	if updatedWidth != 65 {
		t.Fatalf("column width = %d, want 65", updatedWidth)
	}
}

func TestPaletteDetachFile(t *testing.T) {
	var in bytes.Buffer
	var out, errOut bytes.Buffer

	dir := shortTempDir(t)
	detachFile := filepath.Join(dir, "detach.tmp")

	in.WriteString("detach\n")
	cfg := paletteTestConfig(t)

	err := runPalette(cfg, []string{"--caller-pane=1", "--detach-file=" + detachFile}, &in, &out, &errOut)
	if err != nil {
		t.Fatalf("runPalette failed: %v", err)
	}

	data, err := os.ReadFile(detachFile)
	if err != nil {
		t.Fatalf("reading detachFile: %v", err)
	}
	if !strings.Contains(string(data), "detach") {
		t.Errorf("detachFile content = %q, want 'detach'", string(data))
	}
}
