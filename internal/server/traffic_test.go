package server

// White-box, same justification as status_test.go: drives
// handleClientMsg and broadcastPaneUpdates directly against fake grids.

import (
	"context"
	"fmt"
	"image"
	"net"
	"sync/atomic"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/layout"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/server/term"
	"github.com/lmorchard/wideboi/internal/transport"
)

// linesGrid draws one string per row, so scrolling it by a row gives
// BuildPanePatch an unambiguous shift.
type linesGrid struct {
	*statusGrid
	lines atomic.Pointer[[]string]
}

func (g *linesGrid) setLines(lines []string) {
	g.lines.Store(&lines)
	g.bump()
}

func (g *linesGrid) Draw(dst uv.Screen, _ image.Rectangle) {
	lines := g.lines.Load()
	if lines == nil {
		return
	}
	for y, line := range *lines {
		for x, r := range line {
			dst.SetCell(x, y, &uv.Cell{Content: string(r), Width: 1})
		}
	}
}

func (g *linesGrid) DrawAt(dst uv.Screen, area image.Rectangle, _ int) {
	g.Draw(dst, area)
}

// takeTraffic drains tp until it finds a MsgTrafficStats. The requester
// is an ordinary transport, so broadcasts may be queued ahead of it.
func takeTraffic(t *testing.T, tp *transport.InProcChannel) protocol.MsgTrafficStats {
	t.Helper()
	for {
		select {
		case msg := <-tp.ServerSend:
			if stats, ok := msg.(protocol.MsgTrafficStats); ok {
				return stats
			}
		default:
			t.Fatal("no MsgTrafficStats queued for the requester")
			return protocol.MsgTrafficStats{}
		}
	}
}

// requestTraffic asks for a report from a fresh transport that never
// attaches, the way `wideboi status --traffic` does.
func requestTraffic(t *testing.T, s *Server) protocol.MsgTrafficStats {
	t.Helper()
	observer := transport.NewInProcChannel(64)
	s.mu.Lock()
	s.transports = append(s.transports, observer)
	s.mu.Unlock()
	s.handleClientMsg(context.Background(), observer, protocol.MsgTrafficRequest{})
	return takeTraffic(t, observer)
}

// attachedTrafficServer returns a one-pane server whose first transport
// has attached, with the attach broadcast drained. The pane is already
// the size attaching resizes it to: a fake pane has no pty to resize.
func attachedTrafficServer(t *testing.T, g term.Grid) (*Server, *transport.InProcChannel) {
	t.Helper()
	s, _ := serverWithStatuses(t, map[int]protocol.PaneStatus{1: protocol.StatusIdle})
	s.panes[1].grid = g
	s.panes[1].cols, s.panes[1].rows = trafficPaneSize()
	// serverWithStatuses builds a Server literal: maps NewServer would
	// make are nil. MsgAttach writes clientSizes; trafficLocked must
	// make s.traffic lazily itself.
	s.clientSizes = make(map[transport.Transport]protocol.MsgResize)
	client := s.transports[0].(*transport.InProcChannel)
	s.handleClientMsg(context.Background(), client, protocol.MsgAttach{Cols: 80, Rows: 24})
	drainPaneUpdates(client)
	return s, client
}

// trafficPaneSize is serverWithStatuses' column width and the pane
// height an 80x24 attach gives it.
func trafficPaneSize() (cols, rows int) { return 40, layout.AvailHeight(24) }

func TestTrafficCountsKindsPerAttachedClient(t *testing.T) {
	g := &cellGrid{statusGrid: newStatusGrid(protocol.StatusIdle)}
	g.set("a")
	s, client := attachedTrafficServer(t, g)
	ctx := context.Background()

	g.set("b")
	s.broadcastPaneUpdates(ctx, false) // row patch, 1 row
	s.handleClientMsg(ctx, client, protocol.MsgPaneResync{PaneID: 1})
	drainPaneUpdates(client)

	stats := requestTraffic(t, s)
	if len(stats.Clients) != 1 {
		t.Fatalf("got %d clients, want only the attached one: %+v", len(stats.Clients), stats.Clients)
	}
	c := stats.Clients[0]
	if c.RowPatches != 1 || c.ChangedRows != 1 || c.ResyncRequests != 1 {
		t.Errorf("row=%d rows=%d resync=%d, want 1/1/1", c.RowPatches, c.ChangedRows, c.ResyncRequests)
	}
	// Exactly two fulls: the attach broadcast and the resync answer.
	// Equality, so counting a delivery twice fails.
	if c.FullUpdates != 2 {
		t.Errorf("full=%d, want 2", c.FullUpdates)
	}
	if c.ShiftPatches != 0 || c.SendFailures != 0 {
		t.Errorf("shift=%d fail=%d, want 0/0", c.ShiftPatches, c.SendFailures)
	}
	if c.Transport != "inproc" || c.ClientID == 0 {
		t.Errorf("transport=%q id=%d, want inproc and a nonzero id", c.Transport, c.ClientID)
	}
}

func TestTrafficCountsShiftPatchesAndFailures(t *testing.T) {
	// Every row distinct, so a scroll by one is an unambiguous shift.
	_, height := trafficPaneSize()
	numbered := func(prefix string, from int) []string {
		lines := make([]string, height)
		for y := range lines {
			lines[y] = fmt.Sprintf("%s%d", prefix, from+y)
		}
		return lines
	}
	g := &linesGrid{statusGrid: newStatusGrid(protocol.StatusIdle)}
	g.setLines(numbered("r", 0))
	s, client := attachedTrafficServer(t, g)
	ctx := context.Background()

	g.setLines(numbered("r", 1))
	s.broadcastPaneUpdates(ctx, false)
	if msg, ok := (<-client.ServerSend).(protocol.MsgPanePatch); !ok || msg.ShiftRows != -1 {
		t.Fatalf("scroll by one did not produce a shift patch: %+v", msg)
	}

	for len(client.ServerSend) < cap(client.ServerSend) {
		client.ServerSend <- struct{}{}
	}
	g.setLines(numbered("x", 0))
	s.broadcastPaneUpdates(ctx, false)
	for len(client.ServerSend) > 0 {
		<-client.ServerSend
	}

	stats := requestTraffic(t, s)
	if len(stats.Clients) != 1 {
		t.Fatalf("got %d clients, want 1", len(stats.Clients))
	}
	c := stats.Clients[0]
	if c.ShiftPatches != 1 || c.ChangedRows != 1 || c.SendFailures != 1 || c.RowPatches != 0 {
		t.Errorf("shift=%d rows=%d fail=%d row=%d, want 1/1/1/0", c.ShiftPatches, c.ChangedRows, c.SendFailures, c.RowPatches)
	}
}

func TestTrafficDepartedClientsFoldIntoSession(t *testing.T) {
	g := &cellGrid{statusGrid: newStatusGrid(protocol.StatusIdle)}
	g.set("a")
	s, client := attachedTrafficServer(t, g)
	ctx := context.Background()

	s.dropClient(ctx, client)

	stats := requestTraffic(t, s)
	if len(stats.Clients) != 0 {
		t.Fatalf("departed client still listed: %+v", stats.Clients)
	}
	if stats.Departed.FullUpdates == 0 {
		t.Errorf("departed aggregate lost the client's counts: %+v", stats.Departed)
	}
}

// A connection that never attaches -- status, kill-session, this very
// request -- is not a client, even after it has received broadcasts.
func TestTrafficExcludesUnattachedConnections(t *testing.T) {
	g := &cellGrid{statusGrid: newStatusGrid(protocol.StatusIdle)}
	g.set("a")
	s, _ := attachedTrafficServer(t, g)
	first := requestTraffic(t, s)
	g.set("b")
	s.broadcastPaneUpdates(context.Background(), false)
	second := requestTraffic(t, s)
	// The first observer is still connected and got the second
	// broadcast; it must not appear in the second report.
	if len(first.Clients) != 1 || len(second.Clients) != 1 {
		t.Fatalf("clients = %d then %d, want 1 and 1", len(first.Clients), len(second.Clients))
	}
}

// Timing is opt-in: a broadcast with a patch records render and patch
// build durations only once SetTrafficTiming(true) has been called.
func TestTrafficTimingOnlyWhenEnabled(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint("enabled=", enabled), func(t *testing.T) {
			g := &cellGrid{statusGrid: newStatusGrid(protocol.StatusIdle)}
			g.set("a")
			s, client := attachedTrafficServer(t, g)
			s.SetTrafficTiming(enabled)
			g.set("b")
			s.broadcastPaneUpdates(context.Background(), false) // render + row patch
			drainPaneUpdates(client)

			stats := requestTraffic(t, s)
			if stats.TimingEnabled != enabled {
				t.Errorf("TimingEnabled=%v, want %v", stats.TimingEnabled, enabled)
			}
			if got := stats.Render.Count > 0 && stats.BuildPatch.Count > 0; got != enabled {
				t.Errorf("render n=%d build n=%d, want nonzero only when enabled", stats.Render.Count, stats.BuildPatch.Count)
			}
			if !enabled && (stats.Render.Count != 0 || stats.BuildPatch.Count != 0) {
				t.Errorf("render n=%d build n=%d with timing off, want 0/0", stats.Render.Count, stats.BuildPatch.Count)
			}
		})
	}
}

// The in-process channel counts no bytes, so the tests above never
// reach withTransportStats. A real socket conn over a pipe checks that
// its byte counts and encode timing make it into both the live report
// and, once it drops, the departed aggregate.
func TestTrafficReportsSocketTransportStats(t *testing.T) {
	g := &cellGrid{statusGrid: newStatusGrid(protocol.StatusIdle)}
	g.set("a")
	s, inproc := attachedTrafficServer(t, g)
	// Before the socket's traffic entry exists: that is when encode
	// timing is switched on for it.
	s.SetTrafficTiming(true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverEnd, clientEnd := net.Pipe()
	sc := transport.NewServerSocketConn(serverEnd, 64)
	sc.RunPumps(ctx)
	defer sc.Close()
	peer := transport.NewClientSocketConn(clientEnd, 64)
	peer.RunPumps(ctx)

	s.mu.Lock()
	s.transports = append(s.transports, sc)
	s.mu.Unlock()
	s.handleClientMsg(ctx, sc, protocol.MsgAttach{Cols: 80, Rows: 24})
	g.set("b")
	s.broadcastPaneUpdates(ctx, false)
	drainPaneUpdates(inproc)

	// A sentinel behind everything queued: once the peer has read it
	// and the write pump has recorded as many messages as the peer
	// read, the socket's counts are final.
	const sentinel = 999
	if !sc.SendServer(ctx, protocol.MsgPaneCreated{PaneID: sentinel}) {
		t.Fatal("sentinel send refused")
	}
	var received, paneMsgs uint64
	timeout := time.After(5 * time.Second)
	for done := false; !done; {
		select {
		case msg, ok := <-peer.ServerSend:
			if !ok {
				t.Fatalf("peer closed before the sentinel: %v", peer.Err())
			}
			received++
			switch m := msg.(type) {
			case protocol.MsgPaneUpdate, protocol.MsgPanePatch:
				paneMsgs++
			case protocol.MsgPaneCreated:
				done = m.PaneID == sentinel
			}
		case <-timeout:
			t.Fatal("peer never saw the sentinel")
		}
	}
	if paneMsgs == 0 {
		t.Fatal("socket client received no pane updates")
	}
	for deadline := time.Now().Add(5 * time.Second); sc.TransportStats().Messages != received; {
		if time.Now().After(deadline) {
			t.Fatalf("write pump recorded %d messages, peer read %d", sc.TransportStats().Messages, received)
		}
		time.Sleep(time.Millisecond)
	}

	stats := requestTraffic(t, s)
	var c *protocol.ClientTraffic
	for i := range stats.Clients {
		if stats.Clients[i].Transport == "socket" {
			c = &stats.Clients[i]
		}
	}
	if c == nil {
		t.Fatalf("no socket client in report: %+v", stats.Clients)
	}
	if c.Messages != received || c.PayloadBytes == 0 || c.PanePayloadBytes == 0 || c.WireBytes == 0 {
		t.Errorf("messages=%d payload=%d pane=%d wire=%d, want %d messages and nonzero bytes",
			c.Messages, c.PayloadBytes, c.PanePayloadBytes, c.WireBytes, received)
	}
	if c.WireBytes != c.PayloadBytes+4*c.Messages {
		t.Errorf("wire=%d, want payload %d + 4 x %d messages", c.WireBytes, c.PayloadBytes, c.Messages)
	}
	if c.Encode.Count == 0 {
		t.Errorf("encode timing count=0 with timing on")
	}

	s.dropClient(ctx, sc)
	stats = requestTraffic(t, s)
	if stats.Departed.PayloadBytes == 0 || stats.Departed.WireBytes == 0 {
		t.Errorf("departed lost the socket's bytes: payload=%d wire=%d", stats.Departed.PayloadBytes, stats.Departed.WireBytes)
	}
}
