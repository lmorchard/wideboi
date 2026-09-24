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

func mockServer(t *testing.T, dir string, snap protocol.MsgLayoutSnapshot) string {
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
		sc := transport.NewServerSocketConn(conn, 1)
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		sc.RunPumps(ctx)

		// Wait for the client's request
		select {
		case msg := <-sc.ClientSendChan():
			if _, ok := msg.(protocol.MsgStatusRequest); ok {
				sc.SendServer(ctx, snap)
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

	sockPath := mockServer(t, dir, snap)

	var buf bytes.Buffer
	cfg := config.Config{Socket: sockPath}

	err = runStatus(cfg, false, &buf)
	if err != nil {
		t.Fatalf("runStatus error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "PANE ID") {
		t.Errorf("expected header in output, got:\n%s", out)
	}
	if !strings.Contains(out, "1") || !strings.Contains(out, "vim") || !strings.Contains(out, "done") {
		t.Errorf("expected pane 1 info in output, got:\n%s", out)
	}
	if !strings.Contains(out, "2") || !strings.Contains(out, "npm start") || !strings.Contains(out, "working") {
		t.Errorf("expected pane 2 info in output, got:\n%s", out)
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

	sockPath := mockServer(t, dir, snap)

	var buf bytes.Buffer
	cfg := config.Config{Socket: sockPath}

	err = runStatus(cfg, true, &buf)
	if err != nil {
		t.Fatalf("runStatus error: %v", err)
	}

	var parsed protocol.MsgLayoutSnapshot
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("failed to parse JSON output: %v\nOutput was:\n%s", err, buf.String())
	}
	if len(parsed.Columns) != 1 || parsed.PaneStatuses[3] != protocol.StatusIdle {
		t.Errorf("parsed JSON did not match expected structure: %+v", parsed)
	}
}
