package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/server/term"
	"github.com/lmorchard/wideboi/internal/transport"
)

type snapshotCloseGrid struct {
	*blockingGrid
	entered chan struct{}
	release chan struct{}
	closed  chan struct{}
}

func (g *snapshotCloseGrid) HistoryRows() (int, []string) {
	close(g.entered)
	<-g.release
	return 0, nil
}

func (g *snapshotCloseGrid) Close() error {
	close(g.closed)
	return nil
}

func TestHistorySnapshotFinishesBeforeGridClose(t *testing.T) {
	g := &snapshotCloseGrid{blockingGrid: newBlockingGrid(), entered: make(chan struct{}),
		release: make(chan struct{}), closed: make(chan struct{})}
	p := &Pane{id: 1, grid: g, closed: make(chan struct{})}
	snapshotDone := make(chan struct{})
	go func() { p.HistoryRows(); close(snapshotDone) }()
	<-g.entered
	closeDone := make(chan struct{})
	go func() { _ = p.Close(); close(closeDone) }()
	select {
	case <-g.closed:
		t.Fatal("grid closed while history snapshot was reading")
	case <-time.After(20 * time.Millisecond):
	}
	close(g.release)
	<-snapshotDone
	<-closeDone
}

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
