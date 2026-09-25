package server

import (
	"context"
	"fmt"
	"testing"

	"github.com/lmorchard/wideboi/internal/layout"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/server/term"
	"github.com/lmorchard/wideboi/internal/transport"
)

func drainServerMessages(tp *transport.InProcChannel) []any {
	var msgs []any
	for {
		select {
		case msg := <-tp.ServerSend:
			msgs = append(msgs, msg)
		default:
			return msgs
		}
	}
}

func takeLatestPaneUpdate(t *testing.T, tp *transport.InProcChannel) (scrollOffset int, unread bool, gen uint64) {
	t.Helper()
	msgs := drainServerMessages(tp)
	if len(msgs) == 0 {
		t.Fatal("expected at least one server message")
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		switch m := msgs[i].(type) {
		case protocol.MsgPaneUpdate:
			return m.ScrollOffset, m.UnreadOutput, m.Generation
		case protocol.MsgPanePatch:
			return m.ScrollOffset, m.UnreadOutput, m.Generation
		}
	}
	t.Fatalf("no pane update or patch in %d messages", len(msgs))
	return 0, false, 0
}

func TestIndependentScrollTwoClients(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	grid := term.NewVT(20, 5)
	t.Cleanup(func() { _ = grid.Close() })
	for i := 0; i < 20; i++ {
		fmt.Fprintf(grid, "line %02d\r\n", i)
	}

	pane := &Pane{id: 1, grid: grid, cols: 20, rows: 5}
	s := &Server{
		panes: map[int]*Pane{1: pane},
		cols:  80,
		rows:  24,
	}

	tpA := transport.NewInProcChannel(16)
	tpB := transport.NewInProcChannel(16)
	s.transports = []transport.Transport{tpA, tpB}

	// Initial broadcast sends full updates to both clients at offset 0
	s.broadcastPaneUpdates(ctx, false)

	offsetA, unreadA, _ := takeLatestPaneUpdate(t, tpA)
	if offsetA != 0 || unreadA {
		t.Fatalf("client A initial: offset=%d unread=%v, want 0, false", offsetA, unreadA)
	}
	offsetB, unreadB, _ := takeLatestPaneUpdate(t, tpB)
	if offsetB != 0 || unreadB {
		t.Fatalf("client B initial: offset=%d unread=%v, want 0, false", offsetB, unreadB)
	}

	// Client A scrolls up 5 lines
	s.handleClientMsg(ctx, tpA, protocol.MsgScroll{PaneID: 1, Delta: 5})
	s.broadcastPaneUpdates(ctx, false)

	offsetA, unreadA, _ = takeLatestPaneUpdate(t, tpA)
	if offsetA != 5 || unreadA {
		t.Errorf("client A after scroll: offset=%d unread=%v, want 5, false", offsetA, unreadA)
	}

	// Client B must NOT have received any message
	if msgs := drainServerMessages(tpB); len(msgs) != 0 {
		t.Errorf("client B received %d unexpected messages after client A scrolled", len(msgs))
	}

	// Client B scrolls up 2 lines independently
	s.handleClientMsg(ctx, tpB, protocol.MsgScroll{PaneID: 1, Delta: 2})
	s.broadcastPaneUpdates(ctx, false)

	offsetB, unreadB, _ = takeLatestPaneUpdate(t, tpB)
	if offsetB != 2 || unreadB {
		t.Errorf("client B after scroll: offset=%d unread=%v, want 2, false", offsetB, unreadB)
	}

	// Client A must NOT have received any message
	if msgs := drainServerMessages(tpA); len(msgs) != 0 {
		t.Errorf("client A received %d unexpected messages after client B scrolled", len(msgs))
	}
}

func TestAbsoluteScrollIgnoresOffsetShiftFromNewOutput(t *testing.T) {
	grid := term.NewVT(20, 5)
	t.Cleanup(func() { _ = grid.Close() })
	for i := 0; i < 20; i++ {
		fmt.Fprintf(grid, "line %02d\r\n", i)
	}
	s := &Server{panes: map[int]*Pane{1: {id: 1, grid: grid, cols: 20, rows: 5}}}
	tp := transport.NewInProcChannel(16)
	s.clientScrollOffsets = map[transport.Transport]map[int]int{tp: {1: 9}}
	s.handleClientMsg(context.Background(), tp, protocol.MsgScroll{PaneID: 1, SetAbsolute: true, Offset: 2})
	if got := s.clientScrollOffsets[tp][1]; got != 2 {
		t.Fatalf("absolute scroll landed at %d, want 2", got)
	}
}

func TestScrolledClientStaysInHistoryWithUnreadOutput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	grid := term.NewVT(20, 5)
	t.Cleanup(func() { _ = grid.Close() })
	for i := 0; i < 20; i++ {
		fmt.Fprintf(grid, "line %02d\r\n", i)
	}

	pane := &Pane{id: 1, grid: grid, cols: 20, rows: 5}
	s := &Server{
		panes: map[int]*Pane{1: pane},
		cols:  80,
		rows:  24,
	}

	tpA := transport.NewInProcChannel(16)
	tpB := transport.NewInProcChannel(16)
	s.transports = []transport.Transport{tpA, tpB}

	s.broadcastPaneUpdates(ctx, false)
	drainServerMessages(tpA)
	drainServerMessages(tpB)

	// Client A scrolls up 5 lines
	s.handleClientMsg(ctx, tpA, protocol.MsgScroll{PaneID: 1, Delta: 5})
	s.broadcastPaneUpdates(ctx, false)
	offsetA, unreadA, _ := takeLatestPaneUpdate(t, tpA)
	if offsetA != 5 || unreadA {
		t.Fatalf("client A: offset=%d unread=%v, want 5, false", offsetA, unreadA)
	}

	// New output arrives on the pane
	fmt.Fprintf(grid, "new line 21\r\n")

	s.broadcastPaneUpdates(ctx, false)

	// Client B receives update at offset 0, no unread flag
	offsetB, unreadB, _ := takeLatestPaneUpdate(t, tpB)
	if offsetB != 0 || unreadB {
		t.Errorf("client B after output: offset=%d unread=%v, want 0, false", offsetB, unreadB)
	}

	// Client A receives update staying in history (offset >= 5) with unreadOutput = true
	offsetA, unreadA, _ = takeLatestPaneUpdate(t, tpA)
	if offsetA < 5 {
		t.Errorf("client A after output: offset=%d, want >= 5", offsetA)
	}
	if !unreadA {
		t.Errorf("client A after output: unread=%v, want true", unreadA)
	}

	// Client A scrolls down to bottom
	s.handleClientMsg(ctx, tpA, protocol.MsgScroll{PaneID: 1, Delta: -offsetA})
	s.broadcastPaneUpdates(ctx, false)

	offsetA, unreadA, _ = takeLatestPaneUpdate(t, tpA)
	if offsetA != 0 || unreadA {
		t.Errorf("client A after returning to bottom: offset=%d unread=%v, want 0, false", offsetA, unreadA)
	}
}

func TestTypingSnapsScrolledClientToLive(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	grid := term.NewVT(20, 5)
	t.Cleanup(func() { _ = grid.Close() })
	for i := 0; i < 20; i++ {
		fmt.Fprintf(grid, "line %02d\r\n", i)
	}

	pane := &Pane{id: 1, grid: grid, cols: 20, rows: 5}
	s := &Server{
		panes: map[int]*Pane{1: pane},
		cols:  80,
		rows:  24,
	}

	tpA := transport.NewInProcChannel(16)
	s.transports = []transport.Transport{tpA}

	s.broadcastPaneUpdates(ctx, false)
	drainServerMessages(tpA)

	// Client A scrolls up 5 lines
	s.handleClientMsg(ctx, tpA, protocol.MsgScroll{PaneID: 1, Delta: 5})
	s.broadcastPaneUpdates(ctx, false)
	offsetA, _, _ := takeLatestPaneUpdate(t, tpA)
	if offsetA != 5 {
		t.Fatalf("client A: offset=%d, want 5", offsetA)
	}

	// Client A types into pane
	s.handleClientMsg(ctx, tpA, protocol.MsgInput{PaneID: 1, Data: []byte("ls\n")})
	s.broadcastPaneUpdates(ctx, false)

	offsetA, unreadA, _ := takeLatestPaneUpdate(t, tpA)
	if offsetA != 0 || unreadA {
		t.Errorf("client A after typing: offset=%d unread=%v, want 0, false", offsetA, unreadA)
	}
}

func TestReconnectStartsAtOffsetZero(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	grid := term.NewVT(20, 5)
	t.Cleanup(func() { _ = grid.Close() })
	for i := 0; i < 20; i++ {
		fmt.Fprintf(grid, "line %02d\r\n", i)
	}

	pane := &Pane{id: 1, grid: grid, cols: 20, rows: 5}
	s := &Server{
		strip: layout.NewStrip(),
		panes: map[int]*Pane{1: pane},
		cols:  80,
		rows:  24,
	}

	tp1 := transport.NewInProcChannel(16)
	s.transports = []transport.Transport{tp1}

	s.broadcastPaneUpdates(ctx, false)
	drainServerMessages(tp1)

	// Client 1 scrolls up 5 lines
	s.handleClientMsg(ctx, tp1, protocol.MsgScroll{PaneID: 1, Delta: 5})
	s.broadcastPaneUpdates(ctx, false)
	offset1, _, _ := takeLatestPaneUpdate(t, tp1)
	if offset1 != 5 {
		t.Fatalf("client 1: offset=%d, want 5", offset1)
	}

	// Client 1 disconnects
	s.dropClient(ctx, tp1)

	// Client 2 connects (reconnect scenario)
	tp2 := transport.NewInProcChannel(16)
	s.mu.Lock()
	s.transports = append(s.transports, tp2)
	s.mu.Unlock()

	s.broadcastPaneUpdates(ctx, false)
	offset2, unread2, _ := takeLatestPaneUpdate(t, tp2)
	if offset2 != 0 || unread2 {
		t.Errorf("reconnected client: offset=%d unread=%v, want 0, false", offset2, unread2)
	}
}

func TestResizeDoesNotTriggerUnreadOutputWhenScrolled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	grid := term.NewVT(20, 5)
	t.Cleanup(func() { _ = grid.Close() })
	for i := 0; i < 20; i++ {
		fmt.Fprintf(grid, "line %02d\r\n", i)
	}

	pane := &Pane{id: 1, grid: grid, cols: 20, rows: 22}
	s := &Server{
		strip: layout.NewStrip(),
		panes: map[int]*Pane{1: pane},
		cols:  80,
		rows:  24,
	}
	s.strip.AddColumn(1, 20, 22, 0)

	tp := transport.NewInProcChannel(16)
	s.transports = []transport.Transport{tp}

	s.broadcastPaneUpdates(ctx, false)
	drainServerMessages(tp)

	// Client scrolls up 5 lines
	s.handleClientMsg(ctx, tp, protocol.MsgScroll{PaneID: 1, Delta: 5})
	s.broadcastPaneUpdates(ctx, false)
	offset, unread, _ := takeLatestPaneUpdate(t, tp)
	if offset != 5 || unread {
		t.Fatalf("client initial scroll: offset=%d unread=%v, want 5, false", offset, unread)
	}

	// Session/pane resize occurs without new output
	s.handleClientMsg(ctx, tp, protocol.MsgResize{Cols: 90, Rows: 30})
	s.broadcastPaneUpdates(ctx, false)

	// Offset must still be 5, and unreadOutput must still be false
	offset, unread, _ = takeLatestPaneUpdate(t, tp)
	if offset != 5 {
		t.Errorf("offset after resize = %d, want 5", offset)
	}
	if unread {
		t.Errorf("unreadOutput after resize = %v, want false (no new child output arrived)", unread)
	}
}
