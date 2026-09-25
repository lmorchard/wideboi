package server

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/layout"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/server/term"
	"github.com/lmorchard/wideboi/internal/transport"
)

func takeResponse[T any](t *testing.T, tp *transport.InProcChannel) T {
	t.Helper()
	for {
		select {
		case msg := <-tp.ServerSend:
			if resp, ok := msg.(T); ok {
				return resp
			}
		case <-time.After(time.Second):
			var zero T
			t.Fatalf("timeout waiting for %T", zero)
			return zero
		}
	}
}

func TestServerPaneControlRequests(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s := &Server{
		panes:      make(map[int]*Pane),
		cols:       80,
		rows:       24,
		stopCh:     make(chan struct{}),
		shell:      "/bin/sh",
		cwd:        t.TempDir(),
		closeGrace: 10 * time.Millisecond,
		strip:      layout.NewStrip(),
	}

	tp := transport.NewInProcChannel(64)
	s.transports = append(s.transports, tp)

	// Add an initial pane using term.NewVT
	g1 := term.NewVT(40, 20)
	defer g1.Close()
	fmt.Fprintf(g1, "initial output\r\n")

	p1 := &Pane{
		id:     1,
		grid:   g1,
		cols:   40,
		rows:   20,
		closed: make(chan struct{}),
	}
	s.panes[1] = p1
	s.strip.AddColumn(1, 40, 20, 0)

	// 1. Test MsgCaptureRequest on existing pane
	s.handleClientMsg(ctx, tp, protocol.MsgCaptureRequest{PaneID: 1})
	resp1 := takeResponse[protocol.MsgCaptureResponse](t, tp)
	if resp1.Error != "" {
		t.Fatalf("unexpected error: %s", resp1.Error)
	}
	if resp1.PaneID != 1 {
		t.Fatalf("expected PaneID 1, got %d", resp1.PaneID)
	}
	if resp1.Text != "initial output\n" {
		t.Fatalf("unexpected text: %q", resp1.Text)
	}

	// 2. Test MsgCaptureRequest on non-existent pane
	s.handleClientMsg(ctx, tp, protocol.MsgCaptureRequest{PaneID: 999})
	resp2 := takeResponse[protocol.MsgCaptureResponse](t, tp)
	if resp2.Error == "" {
		t.Fatal("expected error for non-existent pane, got none")
	}

	// 3. Test MsgSendInputRequest on non-existent pane
	s.handleClientMsg(ctx, tp, protocol.MsgSendInputRequest{PaneID: 999, Data: []byte("test")})
	resp3 := takeResponse[protocol.MsgSendInputResponse](t, tp)
	if resp3.Error == "" {
		t.Fatal("expected error for non-existent pane, got none")
	}

	// 4. Test MsgSplitRequest with invalid after_pane_id
	s.handleClientMsg(ctx, tp, protocol.MsgSplitRequest{AfterPaneID: 999})
	resp4 := takeResponse[protocol.MsgSplitResponse](t, tp)
	if resp4.Error == "" {
		t.Fatal("expected error for invalid after_pane_id, got none")
	}

	// 5. Test MsgClosePaneRequest on non-existent pane
	s.handleClientMsg(ctx, tp, protocol.MsgClosePaneRequest{PaneID: 999})
	resp5 := takeResponse[protocol.MsgClosePaneResponse](t, tp)
	if resp5.Error == "" {
		t.Fatal("expected error for non-existent pane, got none")
	}

	// 6. Test MsgSplitRequest success
	s.handleClientMsg(ctx, tp, protocol.MsgSplitRequest{Command: "sleep 10", Cwd: s.cwd})
	resp6 := takeResponse[protocol.MsgSplitResponse](t, tp)
	if resp6.Error != "" {
		t.Fatalf("unexpected error spawning pane: %s", resp6.Error)
	}
	if resp6.PaneID <= 0 {
		t.Fatalf("expected positive PaneID, got %d", resp6.PaneID)
	}
	spawnedID := resp6.PaneID

	// Verify pane exists
	if _, ok := s.panes[spawnedID]; !ok {
		t.Fatalf("expected pane %d to exist in s.panes", spawnedID)
	}

	// 7. Test MsgSendInputRequest success
	s.handleClientMsg(ctx, tp, protocol.MsgSendInputRequest{PaneID: spawnedID, Data: []byte("hello\n")})
	resp7 := takeResponse[protocol.MsgSendInputResponse](t, tp)
	if resp7.Error != "" {
		t.Fatalf("unexpected error sending input: %s", resp7.Error)
	}
	if resp7.PaneID != spawnedID {
		t.Fatalf("expected PaneID %d, got %d", spawnedID, resp7.PaneID)
	}

	// 8. Test MsgClosePaneRequest on existing pane
	s.handleClientMsg(ctx, tp, protocol.MsgClosePaneRequest{PaneID: 1})
	resp8 := takeResponse[protocol.MsgClosePaneResponse](t, tp)
	if resp8.Error != "" {
		t.Fatalf("unexpected error: %s", resp8.Error)
	}
	if resp8.PaneID != 1 {
		t.Fatalf("expected PaneID 1, got %d", resp8.PaneID)
	}

	// Verify pane was removed from server
	if _, ok := s.panes[1]; ok {
		t.Fatal("pane 1 still exists in s.panes")
	}

	// Clean up spawned pane
	s.handleClientMsg(ctx, tp, protocol.MsgClosePaneRequest{PaneID: spawnedID})
	takeResponse[protocol.MsgClosePaneResponse](t, tp)
}
