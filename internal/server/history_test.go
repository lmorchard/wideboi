package server

import (
	"context"
	"strings"
	"testing"

	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/server/term"
	"github.com/lmorchard/wideboi/internal/transport"
)

func TestHistoryRequestIsReadOnlyAndClientSpecific(t *testing.T) {
	g := term.NewVT(16, 3)
	t.Cleanup(func() { _ = g.Close() })
	_, _ = g.Write([]byte("old needle\r\nnext\r\nlive\r\nlast"))
	p := &Pane{id: 1, grid: g, cols: 16, rows: 3}
	s := &Server{panes: map[int]*Pane{1: p}}
	first, second := transport.NewInProcChannel(8), transport.NewInProcChannel(8)
	s.handleClientMsg(context.Background(), first, protocol.MsgHistoryRequest{PaneID: 1})
	select {
	case msg := <-first.ServerSend:
		history, ok := msg.(protocol.MsgHistorySnapshot)
		if !ok || history.PaneID != 1 || history.ScrollbackLen != 1 ||
			len(history.Rows) != 4 || !strings.Contains(history.Rows[0], "needle") {
			t.Fatalf("history reply = %#v", msg)
		}
	default:
		t.Fatal("requester received no history")
	}
	if len(second.ServerSend) != 0 || len(s.clientScrollOffsets) != 0 {
		t.Fatal("history request affected another client or scroll state")
	}
}
