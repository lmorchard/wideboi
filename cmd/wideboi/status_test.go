package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lmorchard/wideboi/internal/config"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

func mockServer(t *testing.T, dir string, snap protocol.MsgLayoutSnapshot, metas ...protocol.MsgPaneMetadata) string {
	sockPath := filepath.Join(dir, "test.sock")
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })

	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		if _, err := transport.Handshake(conn); err != nil {
			conn.Close()
			return
		}
		sc := transport.NewServerSocketConn(conn, 16)
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		sc.RunPumps(ctx)

		// Wait for the client's request
		select {
		case msg := <-sc.ClientSendChan():
			if _, ok := msg.(protocol.MsgStatusRequest); ok {
				sc.SendServer(ctx, snap)
				for _, meta := range metas {
					sc.SendServer(ctx, meta)
				}
			}
		case <-ctx.Done():
		}
	}()

	return sockPath
}

func TestRunStatus(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "wb-status")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	snap := protocol.MsgLayoutSnapshot{
		Columns: []protocol.ColumnData{
			{PaneID: 1, Width: 80, Height: 24},
			{PaneID: 2, Width: 40, Height: 24},
		},
		PaneStatuses: map[int]protocol.PaneStatus{
			1: protocol.StatusDone,
			2: protocol.StatusWorking,
		},
		PaneTitles: map[int]string{
			1: "vim",
			2: "npm start",
		},
	}

	metas := []protocol.MsgPaneMetadata{
		{PaneID: 1, CWD: "/home/user/vim", UserVars: map[string]string{"foo": "bar"}},
		{PaneID: 2, CWD: "", UserVars: nil},
	}

	sockPath := mockServer(t, dir, snap, metas...)

	var buf bytes.Buffer
	cfg := config.Config{Socket: sockPath}

	err = runStatus(cfg, false, &buf)
	if err != nil {
		t.Fatalf("runStatus error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "PANE ID") || !strings.Contains(out, "CWD") {
		t.Errorf("expected header with CWD in output, got:\n%s", out)
	}
	if !strings.Contains(out, "1") || !strings.Contains(out, "vim") || !strings.Contains(out, "done") || !strings.Contains(out, "/home/user/vim") {
		t.Errorf("expected pane 1 info with CWD in output, got:\n%s", out)
	}
	if !strings.Contains(out, "2") || !strings.Contains(out, "npm start") || !strings.Contains(out, "working") || !strings.Contains(out, "-") {
		t.Errorf("expected pane 2 info with unset CWD in output, got:\n%s", out)
	}
}

func TestRunStatusJSON(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "wb-status")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	snap := protocol.MsgLayoutSnapshot{
		Columns: []protocol.ColumnData{
			{PaneID: 3, Width: 100, Height: 50},
		},
		PaneStatuses: map[int]protocol.PaneStatus{3: protocol.StatusIdle},
		PaneTitles:   map[int]string{3: "bash"},
	}

	metas := []protocol.MsgPaneMetadata{
		{PaneID: 3, CWD: "/home/user/bash", UserVars: map[string]string{"agent": "claude"}},
	}

	sockPath := mockServer(t, dir, snap, metas...)

	var buf bytes.Buffer
	cfg := config.Config{Socket: sockPath}

	err = runStatus(cfg, true, &buf)
	if err != nil {
		t.Fatalf("runStatus error: %v", err)
	}

	var parsed struct {
		Columns      []protocol.ColumnData            `json:"columns"`
		PaneStatuses map[int]protocol.PaneStatus      `json:"pane_statuses"`
		PaneTitles   map[int]string                   `json:"pane_titles"`
		PaneMetadata map[int]protocol.MsgPaneMetadata `json:"pane_metadata"`
	}
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("failed to parse JSON output: %v\nOutput was:\n%s", err, buf.String())
	}
	if len(parsed.Columns) != 1 || parsed.PaneStatuses[3] != protocol.StatusIdle {
		t.Errorf("parsed JSON did not match expected structure: %+v", parsed)
	}
	if parsed.PaneMetadata[3].CWD != "/home/user/bash" {
		t.Errorf("parsed JSON CWD = %q, want /home/user/bash", parsed.PaneMetadata[3].CWD)
	}
	if parsed.PaneMetadata[3].UserVars["agent"] != "claude" {
		t.Errorf("parsed JSON UserVars[agent] = %q, want claude", parsed.PaneMetadata[3].UserVars["agent"])
	}

	// Verify exact snake_case JSON field names
	var raw map[string]any
	if err := json.Unmarshal(buf.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"columns", "pane_statuses", "pane_titles", "pane_metadata"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("JSON output missing top-level key %q", key)
		}
	}
	paneMeta := raw["pane_metadata"].(map[string]any)["3"].(map[string]any)
	for _, key := range []string{"pane_id", "cwd", "user_vars"} {
		if _, ok := paneMeta[key]; !ok {
			t.Errorf("pane_metadata missing key %q: %+v", key, paneMeta)
		}
	}

	// Verify legacy unmarshal into MsgLayoutSnapshot still works
	var legacy protocol.MsgLayoutSnapshot
	if err := json.Unmarshal(buf.Bytes(), &legacy); err != nil {
		t.Fatalf("failed to unmarshal into MsgLayoutSnapshot: %v", err)
	}
	if len(legacy.Columns) != 1 || legacy.PaneStatuses[3] != protocol.StatusIdle {
		t.Errorf("legacy unmarshal failed: %+v", legacy)
	}
}
