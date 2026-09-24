package server

// White-box, same justification as status_test.go: drives
// broadcastPaneUpdates directly against fake grids.

import (
	"context"
	"image"
	"slices"
	"sort"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/server/term"
	"github.com/lmorchard/wideboi/internal/transport"
)

type blockingDrawGrid struct {
	*statusGrid
	started chan struct{}
	release chan struct{}
}

type changingDrawGrid struct{ *statusGrid }

func (g *changingDrawGrid) Draw(uv.Screen, image.Rectangle) { g.bump() }

func TestGenerationChangingDuringRenderIsRetried(t *testing.T) {
	s, _, tp := twoIdlePanes(t)
	g := &changingDrawGrid{newStatusGrid(term.StatusIdle)}
	s.panes[1].grid = g
	s.broadcastPaneUpdates(context.Background(), false)
	drainPaneUpdates(tp)
	s.broadcastPaneUpdates(context.Background(), false)
	if got := drainPaneUpdates(tp); !slices.Equal(got, []int{1}) {
		t.Errorf("pane changed during render; retry sent %v, want [1]", got)
	}
}

func (g *blockingDrawGrid) Draw(uv.Screen, image.Rectangle) {
	close(g.started)
	<-g.release
}

// A slow grid render must not hold the server mutex. The same mutex
// protects input, focus, pane lifecycle, and shutdown bookkeeping.
func TestPaneRenderDoesNotHoldServerMutex(t *testing.T) {
	s, _, _ := twoIdlePanes(t)
	g := &blockingDrawGrid{statusGrid: newStatusGrid(term.StatusIdle), started: make(chan struct{}), release: make(chan struct{})}
	s.panes[1].grid = g
	done := make(chan struct{})
	go func() {
		s.broadcastPaneUpdates(context.Background(), false)
		close(done)
	}()
	select {
	case <-g.started:
	case <-time.After(time.Second):
		t.Fatal("pane render did not start")
	}
	locked := make(chan struct{})
	go func() {
		s.mu.Lock()
		s.mu.Unlock()
		close(locked)
	}()
	select {
	case <-locked:
	case <-time.After(200 * time.Millisecond):
		t.Error("server mutex remained locked during pane render")
	}
	close(g.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("pane broadcast did not finish")
	}
}

// drainPaneUpdates empties tp and returns the pane IDs of the updates
// it held, sorted. Anything else in the buffer is discarded.
func drainPaneUpdates(tp *transport.InProcChannel) []int {
	var ids []int
	for {
		select {
		case msg := <-tp.ServerSend:
			if u, ok := msg.(protocol.MsgPaneUpdate); ok {
				ids = append(ids, u.PaneID)
			}
		default:
			sort.Ints(ids)
			return ids
		}
	}
}

func twoIdlePanes(t *testing.T) (*Server, map[int]*statusGrid, *transport.InProcChannel) {
	t.Helper()
	s, grids := serverWithStatuses(t, map[int]term.PaneStatus{1: term.StatusIdle, 2: term.StatusIdle})
	return s, grids, s.transports[0].(*transport.InProcChannel)
}

// The point of #85: an idle session used to send every pane to every
// client ~30 times a second.
func TestUnchangedPanesAreNotResent(t *testing.T) {
	s, grids, tp := twoIdlePanes(t)
	ctx := context.Background()

	s.broadcastPaneUpdates(ctx, false)
	if got := drainPaneUpdates(tp); !slices.Equal(got, []int{1, 2}) {
		t.Fatalf("first tick sent %v, want [1 2]", got)
	}
	for i := 0; i < 5; i++ {
		s.broadcastPaneUpdates(ctx, false)
		if got := drainPaneUpdates(tp); len(got) != 0 {
			t.Fatalf("tick %d resent unchanged panes %v", i, got)
		}
	}
	grids[1].bump()
	s.broadcastPaneUpdates(ctx, false)
	if got := drainPaneUpdates(tp); !slices.Equal(got, []int{1}) {
		t.Errorf("after pane 1 changed, sent %v, want [1]", got)
	}
}

// Change-only sends lose the old self-healing: a dropped update is no
// longer repaired by the next frame's resend. The client that missed
// it must get it again; the one that didn't must not.
func TestDroppedPaneUpdateIsRetriedForThatClientOnly(t *testing.T) {
	s, _, healthy := twoIdlePanes(t)
	ctx := context.Background()
	wedged := transport.NewInProcChannel(4)
	s.transports = append(s.transports, wedged)
	for len(wedged.ServerSend) < cap(wedged.ServerSend) {
		wedged.ServerSend <- struct{}{}
	}

	s.broadcastPaneUpdates(ctx, false)
	if got := drainPaneUpdates(healthy); !slices.Equal(got, []int{1, 2}) {
		t.Fatalf("healthy client got %v, want [1 2]", got)
	}
	drainPaneUpdates(wedged) // discard the filler; nothing real got in

	s.broadcastPaneUpdates(ctx, false)
	if got := drainPaneUpdates(healthy); len(got) != 0 {
		t.Errorf("healthy client was resent %v", got)
	}
	if got := drainPaneUpdates(wedged); !slices.Equal(got, []int{1, 2}) {
		t.Errorf("wedged client got %v on retry, want [1 2]", got)
	}
}

// A client attaching to a running session has seen nothing yet.
func TestNewClientGetsEveryPane(t *testing.T) {
	s, _, tp := twoIdlePanes(t)
	ctx := context.Background()
	s.broadcastPaneUpdates(ctx, false)
	drainPaneUpdates(tp)

	late := transport.NewInProcChannel(64)
	s.transports = append(s.transports, late)
	s.broadcastPaneUpdates(ctx, false)
	if got := drainPaneUpdates(late); !slices.Equal(got, []int{1, 2}) {
		t.Errorf("new client got %v, want [1 2]", got)
	}
	if got := drainPaneUpdates(tp); len(got) != 0 {
		t.Errorf("existing client was resent %v", got)
	}
}

// A snapshot can prune or blank a client's mirrors, so every layout
// broadcast must be followed by every pane, changed or not.
func TestLayoutBroadcastResendsEveryPane(t *testing.T) {
	s, _, tp := twoIdlePanes(t)
	ctx := context.Background()
	s.broadcastPaneUpdates(ctx, false)
	drainPaneUpdates(tp)

	s.broadcastLayout(ctx)
	if got := drainPaneUpdates(tp); !slices.Equal(got, []int{1, 2}) {
		t.Errorf("layout broadcast was followed by %v, want [1 2]", got)
	}
}

// Records must not outlive what they describe: a detached client or an
// exited pane would otherwise accumulate forever in a long-lived server.
func TestDeliveryRecordsAreForgotten(t *testing.T) {
	s, grids, tp := twoIdlePanes(t)
	ctx := context.Background()
	s.broadcastPaneUpdates(ctx, false)

	s.mu.Lock()
	delete(s.panes, 2)
	s.mu.Unlock()
	grids[1].bump()
	s.broadcastPaneUpdates(ctx, false)
	if _, ok := s.paneGens[tp][2]; ok {
		t.Error("record for exited pane 2 survived a broadcast")
	}

	// With no panes left nothing is delivered, and the last record
	// must still go.
	s.mu.Lock()
	delete(s.panes, 1)
	s.mu.Unlock()
	s.broadcastPaneUpdates(ctx, false)
	if n := len(s.paneGens[tp]); n != 0 {
		t.Errorf("%d record(s) survived the last pane exiting", n)
	}

	s.dropClient(context.Background(), tp)
	if _, ok := s.paneGens[tp]; ok {
		t.Error("records for a dropped client survived dropClient")
	}
}

// A forced resend goes to clients that may already hold the pane's
// current generation. If it is dropped, the record still looks current,
// so without invalidating it no later tick would retry -- and the
// snapshot in front of it may just have pruned or blanked that mirror.
func TestDroppedForcedResendIsRetried(t *testing.T) {
	s, _ := serverWithStatuses(t, map[int]term.PaneStatus{1: term.StatusIdle, 2: term.StatusIdle})
	ctx := context.Background()
	tp := transport.NewInProcChannel(4)
	s.transports = []transport.Transport{tp}
	s.broadcastLayoutIfStatusChanged(ctx) // baseline statuses, so only broadcastLayout sends a snapshot
	drainPaneUpdates(tp)
	s.broadcastPaneUpdates(ctx, false)
	if got := drainPaneUpdates(tp); len(got) != 0 {
		t.Fatalf("baseline not settled: resent %v", got)
	}

	// Leave room for the snapshot and nothing else.
	for len(tp.ServerSend) < cap(tp.ServerSend)-1 {
		tp.ServerSend <- struct{}{}
	}
	s.broadcastLayout(ctx)
	drainPaneUpdates(tp) // filler plus the snapshot; both pane updates were dropped

	s.broadcastPaneUpdates(ctx, false)
	if got := drainPaneUpdates(tp); !slices.Equal(got, []int{1, 2}) {
		t.Errorf("after a dropped forced resend, next tick sent %v, want [1 2]", got)
	}
}
