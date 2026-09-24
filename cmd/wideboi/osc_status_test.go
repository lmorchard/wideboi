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
	"time"

	"github.com/lmorchard/wideboi/internal/config"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

func TestEndToEndPTYMetadataReporting(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "wb-osc")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sockPath := filepath.Join(dir, "s.sock")

	cfg := config.Config{
		Socket: sockPath,
		Shell:  "/bin/sh",
		Startup: []config.StartupPane{
			{
				Command: "printf '\\033]7;file:///test/working/dir\\007\\033]1337;SetUserVar=branch=bWFpbg==\\007'; sleep 5",
			},
		},
	}

	serverCtx, serverCancel := context.WithCancel(context.Background())
	defer serverCancel()

	serverDone := make(chan error, 1)
	go func() {
		serverDone <- runServer(cfg, -1)
	}()

	// Wait for socket to accept connections
	var conn net.Conn
	for i := 0; i < 20; i++ {
		conn, err = net.Dial("unix", sockPath)
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("could not dial server: %v", err)
	}

	// Attach to spawn the startup pane
	if err := handshakeServer(conn, sockPath); err != nil {
		t.Fatalf("handshakeServer: %v", err)
	}
	attachCtx, attachCancel := context.WithCancel(serverCtx)
	defer attachCancel()

	cc := transport.NewClientSocketConn(conn, 256)
	cc.RunPumps(attachCtx)
	cc.SendClient(attachCtx, protocol.MsgAttach{Cols: 80, Rows: 24})
	defer func() {
		cc.SendClient(context.Background(), protocol.MsgShutdown{})
		select {
		case <-serverDone:
		case <-time.After(2 * time.Second):
		}
	}()

	// Wait up to 3 seconds for the child to print and metadata to be processed
	var jsonBuf bytes.Buffer
	var parsed struct {
		Columns      []protocol.ColumnData            `json:"columns"`
		PaneStatuses map[int]protocol.PaneStatus      `json:"pane_statuses"`
		PaneTitles   map[int]string                   `json:"pane_titles"`
		PaneMetadata map[int]protocol.MsgPaneMetadata `json:"pane_metadata"`
	}

	deadline := time.Now().Add(3 * time.Second)
	found := false

	for time.Now().Before(deadline) {
		jsonBuf.Reset()
		if err := runStatus(cfg, true, &jsonBuf); err == nil {
			if err := json.Unmarshal(jsonBuf.Bytes(), &parsed); err == nil {
				for _, meta := range parsed.PaneMetadata {
					if meta.CWD == "/test/working/dir" && meta.UserVars["branch"] == "main" {
						found = true
						break
					}
				}
			}
		}
		if found {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !found {
		t.Fatalf("did not find expected metadata in status --json within deadline. Last output:\n%s", jsonBuf.String())
	}

	// Also verify human-readable table output
	var tableBuf bytes.Buffer
	if err := runStatus(cfg, false, &tableBuf); err != nil {
		t.Fatalf("runStatus table error: %v", err)
	}
	tableOut := tableBuf.String()
	if !strings.Contains(tableOut, "CWD") {
		t.Errorf("table missing CWD header: %s", tableOut)
	}
	if !strings.Contains(tableOut, "/test/working/dir") {
		t.Errorf("table missing working dir /test/working/dir: %s", tableOut)
	}
}
