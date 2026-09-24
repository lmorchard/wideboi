package server

// White-box, same justification as status_test.go: drives
// broadcastPaneUpdates directly against fake grids.

import (
	"context"
	"fmt"
	"image"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/server/ptyx"
	"github.com/lmorchard/wideboi/internal/server/term"
	"github.com/lmorchard/wideboi/internal/transport"
)

type blockingDrawGrid struct {
	*statusGrid
	started chan struct{}
	release chan struct{}
}

type gatedDrawGrid struct {
	term.Grid
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (g *gatedDrawGrid) Draw(dst uv.Screen, area image.Rectangle) {
	g.once.Do(func() {
		close(g.started)
		<-g.release
	})
	g.Grid.Draw(dst, area)
}

func (g *gatedDrawGrid) DrawAt(dst uv.Screen, area image.Rectangle, offset int) {
	g.once.Do(func() {
		close(g.started)
		<-g.release
	})
	g.Grid.DrawAt(dst, area, offset)
}

type cellGrid struct {
	*statusGrid
	content atomic.Pointer[string]
}

func (g *cellGrid) set(s string) {
	g.content.Store(&s)
	g.bump()
}

func (g *cellGrid) Draw(dst uv.Screen, _ image.Rectangle) {
	if text := g.content.Load(); text != nil {
		dst.SetCell(0, 0, &uv.Cell{Content: *text, Width: 1})
	}
}

func (g *cellGrid) DrawAt(dst uv.Screen, area image.Rectangle, _ int) {
	g.Draw(dst, area)
}

func takePaneMessage(t *testing.T, tp *transport.InProcChannel) any {
	t.Helper()
	select {
	case msg := <-tp.ServerSend:
		return msg
	default:
		t.Fatal("expected pane message")
		return nil
	}
}

func TestPanePatchPerClientBaselineAndRecovery(t *testing.T) {
	s, _ := serverWithStatuses(t, map[int]protocol.PaneStatus{1: protocol.StatusIdle})
	g := &cellGrid{statusGrid: newStatusGrid(protocol.StatusIdle)}
	g.set("a")
	s.panes[1].grid = g
	s.panes[1].cols, s.panes[1].rows = 4, 4
	healthy := s.transports[0].(*transport.InProcChannel)
	slow := transport.NewInProcChannel(2)
	s.transports = append(s.transports, slow)
	ctx := context.Background()

	s.broadcastPaneUpdates(ctx, false)
	first, ok := takePaneMessage(t, healthy).(protocol.MsgPaneUpdate)
	if !ok {
		t.Fatal("first healthy delivery was not a full snapshot")
	}
	if _, ok := takePaneMessage(t, slow).(protocol.MsgPaneUpdate); !ok {
		t.Fatal("first slow delivery was not a full snapshot")
	}

	g.set("b")
	for len(slow.ServerSend) < cap(slow.ServerSend) {
		slow.ServerSend <- struct{}{}
	}
	s.broadcastPaneUpdates(ctx, false)
	patch, ok := takePaneMessage(t, healthy).(protocol.MsgPanePatch)
	if !ok || len(patch.ChangedRows) != 1 {
		t.Fatalf("healthy client did not get one changed row: %+v", patch)
	}
	drainPaneUpdates(slow)
	s.broadcastPaneUpdates(ctx, false)
	if _, ok := takePaneMessage(t, slow).(protocol.MsgPaneUpdate); !ok {
		t.Fatal("client that missed a patch did not get a full recovery snapshot")
	}
	if got := drainPaneUpdates(healthy); len(got) != 0 {
		t.Fatalf("healthy client got redundant update: %v", got)
	}

	late := transport.NewInProcChannel(2)
	s.mu.Lock()
	s.transports = append(s.transports, late)
	s.mu.Unlock()
	s.broadcastPaneUpdates(ctx, false)
	if _, ok := takePaneMessage(t, late).(protocol.MsgPaneUpdate); !ok {
		t.Fatal("late client did not get a full snapshot")
	}

	// Model a transport that accepted the first patch but lost it after
	// SendServer returned true. The next patch names generation 2, while
	// this client still holds the initial generation 1 snapshot.
	g.set("c")
	s.broadcastPaneUpdates(ctx, false)
	nextPatch, ok := takePaneMessage(t, healthy).(protocol.MsgPanePatch)
	if !ok || nextPatch.BaseGeneration != patch.Generation {
		t.Fatalf("next patch did not advance from accepted baseline: %+v", nextPatch)
	}
	if _, ok := protocol.ApplyPanePatch(first, nextPatch); ok {
		t.Fatal("client accepted a patch after losing its predecessor")
	}
	s.handleClientMsg(ctx, healthy, protocol.MsgPaneResync{PaneID: 1})
	if _, ok := takePaneMessage(t, healthy).(protocol.MsgPaneUpdate); !ok {
		t.Fatal("resync request did not produce a full snapshot")
	}
}

// The server must keep accepting input and resize work while snapshots
// are rendered and one client never drains its transport. Pane closure
// racing those operations must not retain a stale delivery baseline.
func TestConcurrentInputResizeCloseWithSlowTransport(t *testing.T) {
	healthy := transport.NewInProcChannel(4096)
	s := NewServer(healthy, "/bin/sh", "")
	s.closeGrace = 10 * time.Millisecond
	slow := transport.NewInProcChannel(1)
	slow.ServerSend <- struct{}{}
	s.transports = append(s.transports, slow)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p, err := NewPane(1, []string{"/bin/sh"}, 40, 22, "")
	if err != nil {
		t.Fatal(err)
	}
	p.closeGrace = s.closeGrace
	s.mu.Lock()
	s.panes[1] = p
	s.nextPaneID = 1
	s.strip.AddColumn(1, 40, 22, 0)
	s.mu.Unlock()
	s.handleClientMsg(ctx, healthy, protocol.MsgAttach{Cols: 80, Rows: 24})
	g := &gatedDrawGrid{Grid: p.grid, started: make(chan struct{}), release: make(chan struct{})}
	p.grid = g // Install before Start reads the grid from its worker goroutines.
	p.Start(func() { s.onPaneExit(1) })
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(g.release) }) }
	defer release()
	renderDone := make(chan struct{})
	go func() {
		s.broadcastPaneUpdates(ctx, true)
		close(renderDone)
	}()
	select {
	case <-g.started:
	case <-ctx.Done():
		t.Fatal("pane render did not start")
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	run := func(fn func()) {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; fn() }()
	}
	run(func() {
		for i := 0; i < 30; i++ {
			s.handleClientMsg(ctx, healthy, protocol.MsgInput{PaneID: 1, Data: []byte("x")})
		}
	})
	run(func() {
		for i := 0; i < 15; i++ {
			s.handleClientMsg(ctx, healthy, protocol.MsgResize{Cols: 80 + i%2*20, Rows: 24 + i%2*4})
		}
	})
	run(func() {
		for i := 0; i < 30; i++ {
			s.broadcastPaneUpdates(ctx, false)
		}
	})
	close(start)
	// The pane must begin closing while its render is still held. This
	// directly observes the overlap instead of relying on scheduling time.
	killDone := make(chan struct{})
	go func() {
		s.handleClientMsg(ctx, healthy, protocol.MsgVerb{Verb: protocol.VerbKillPane, PaneID: 1})
		close(killDone)
	}()
	select {
	case <-p.closed:
	case <-ctx.Done():
		t.Fatal("pane close did not begin during render")
	}
	release()
	select {
	case <-killDone:
	case <-ctx.Done():
		t.Fatal("pane kill did not finish")
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("concurrent pane work did not finish")
	}
	select {
	case <-renderDone:
	case <-ctx.Done():
		t.Fatal("held render did not finish")
	}
	closeDone := make(chan struct{})
	go func() { _ = s.Close(); close(closeDone) }()
	select {
	case <-closeDone:
	case <-ctx.Done():
		t.Fatal("server close stalled after concurrent pane work")
	}
}

type changingDrawGrid struct{ *statusGrid }

func (g *changingDrawGrid) Draw(uv.Screen, image.Rectangle)                   { g.bump() }
func (g *changingDrawGrid) DrawAt(dst uv.Screen, area image.Rectangle, _ int) { g.Draw(dst, area) }

type closeAwareGrid struct {
	*statusGrid
	drawing chan struct{}
	release chan struct{}
	closed  chan struct{}
}

func (g *closeAwareGrid) Draw(uv.Screen, image.Rectangle)                   { close(g.drawing); <-g.release }
func (g *closeAwareGrid) DrawAt(dst uv.Screen, area image.Rectangle, _ int) { g.Draw(dst, area) }

func (g *closeAwareGrid) Close() error {
	close(g.closed)
	return nil
}

func TestPaneCloseWaitsForActiveRender(t *testing.T) {
	pty, err := ptyx.Spawn([]string{"/bin/cat"}, 10, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	g := &closeAwareGrid{statusGrid: newStatusGrid(protocol.StatusIdle), drawing: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{})}
	p := &Pane{id: 1, pty: pty, grid: g, cols: 10, rows: 10, closed: make(chan struct{}), closeGrace: 10 * time.Millisecond}
	rendered := make(chan struct{})
	go func() {
		_, _ = p.UpdateMessage()
		close(rendered)
	}()
	select {
	case <-g.drawing:
	case <-time.After(time.Second):
		t.Fatal("render did not start")
	}
	closed := make(chan struct{})
	go func() {
		_ = p.Close()
		close(closed)
	}()
	select {
	case <-p.closed:
	case <-time.After(time.Second):
		t.Fatal("close did not start")
	}
	select {
	case <-g.closed:
		t.Error("grid closed while render was active")
	case <-time.After(30 * time.Millisecond):
	}
	close(g.release)
	select {
	case <-rendered:
	case <-time.After(time.Second):
		t.Fatal("render did not finish")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("close did not finish")
	}
}

func TestGenerationChangingDuringRenderIsRetried(t *testing.T) {
	s, _, tp := twoIdlePanes(t)
	g := &changingDrawGrid{newStatusGrid(protocol.StatusIdle)}
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

func (g *blockingDrawGrid) DrawAt(dst uv.Screen, area image.Rectangle, _ int) {
	g.Draw(dst, area)
}

// A slow grid render must not hold the server mutex. The same mutex
// protects input, focus, pane lifecycle, and shutdown bookkeeping.
func TestPaneRenderDoesNotHoldServerMutex(t *testing.T) {
	s, _, _ := twoIdlePanes(t)
	g := &blockingDrawGrid{statusGrid: newStatusGrid(protocol.StatusIdle), started: make(chan struct{}), release: make(chan struct{})}
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
			switch u := msg.(type) {
			case protocol.MsgPaneUpdate:
				ids = append(ids, u.PaneID)
			case protocol.MsgPanePatch:
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
	s, grids := serverWithStatuses(t, map[int]protocol.PaneStatus{1: protocol.StatusIdle, 2: protocol.StatusIdle})
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
	if _, ok := s.paneFrames[tp][2]; ok {
		t.Error("baseline for exited pane 2 survived a broadcast")
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
	if n := len(s.paneFrames[tp]); n != 0 {
		t.Errorf("%d baseline(s) survived the last pane exiting", n)
	}

	s.dropClient(context.Background(), tp)
	if _, ok := s.paneGens[tp]; ok {
		t.Error("records for a dropped client survived dropClient")
	}
	if _, ok := s.paneFrames[tp]; ok {
		t.Error("baselines for a dropped client survived dropClient")
	}
}

// A forced resend goes to clients that may already hold the pane's
// current generation. If it is dropped, the record still looks current,
// so without invalidating it no later tick would retry -- and the
// snapshot in front of it may just have pruned or blanked that mirror.
func TestDroppedForcedResendIsRetried(t *testing.T) {
	s, _ := serverWithStatuses(t, map[int]protocol.PaneStatus{1: protocol.StatusIdle, 2: protocol.StatusIdle})
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
	s.mu.Lock()
	s.strip.GrowWidth(1, 10)
	s.resizePanesLocked()
	s.mu.Unlock()
	s.broadcastLayout(ctx)
	drainPaneUpdates(tp) // filler plus the snapshot; both pane updates were dropped

	s.broadcastPaneUpdates(ctx, false)
	if got := drainPaneUpdates(tp); !slices.Equal(got, []int{1, 2}) {
		t.Errorf("after a dropped forced resend, next tick sent %v, want [1 2]", got)
	}
}

func TestPaneUpdateMessageForOffset(t *testing.T) {
	grid := term.NewVT(20, 5)
	t.Cleanup(func() { _ = grid.Close() })
	for i := 0; i < 10; i++ {
		if i > 0 {
			fmt.Fprint(grid, "\r\n")
		}
		fmt.Fprintf(grid, "line %02d", i)
	}
	pane := &Pane{id: 1, grid: grid, cols: 20, rows: 5}

	// Offset 0: live view, cursor visible
	msg0, ok := pane.UpdateMessageForOffset(0, false)
	if !ok {
		t.Fatal("UpdateMessageForOffset(0) failed")
	}
	if msg0.ScrollOffset != 0 {
		t.Errorf("msg0.ScrollOffset = %d, want 0", msg0.ScrollOffset)
	}
	if !msg0.CursorVisible {
		t.Error("msg0.CursorVisible = false, want true at offset 0")
	}
	if msg0.UnreadOutput {
		t.Error("msg0.UnreadOutput = true, want false")
	}
	var bottom0 strings.Builder
	for _, c := range msg0.Lines[4] {
		bottom0.WriteString(c.Content)
	}
	if got := strings.TrimRight(bottom0.String(), " "); got != "line 09" {
		t.Errorf("msg0 bottom row = %q, want %q", got, "line 09")
	}

	// Offset 3: scrolled up, cursor suppressed, unreadOutput flag propagated
	msg3, ok := pane.UpdateMessageForOffset(3, true)
	if !ok {
		t.Fatal("UpdateMessageForOffset(3) failed")
	}
	if msg3.ScrollOffset != 3 {
		t.Errorf("msg3.ScrollOffset = %d, want 3", msg3.ScrollOffset)
	}
	if msg3.CursorVisible {
		t.Error("msg3.CursorVisible = true, want false when scrolled up")
	}
	if !msg3.UnreadOutput {
		t.Error("msg3.UnreadOutput = false, want true")
	}
	var bottom3 strings.Builder
	for _, c := range msg3.Lines[4] {
		bottom3.WriteString(c.Content)
	}
	if got := strings.TrimRight(bottom3.String(), " "); got != "line 06" {
		t.Errorf("msg3 bottom row = %q, want %q", got, "line 06")
	}
}

// An unattached connection (such as `wideboi status` or `wideboi ls`)
// dialing in and hanging up must not trigger a layout broadcast or
// force pane resends to existing attached clients.
func TestUnattachedDisconnectDoesNotResendPanes(t *testing.T) {
	s, _, tpAttached := twoIdlePanes(t)
	ctx := context.Background()

	// Attached client sends MsgAttach and consumes initial updates
	s.handleClientMsg(ctx, tpAttached, protocol.MsgAttach{Cols: 80, Rows: 24})
	s.broadcastPaneUpdates(ctx, false)
	drainPaneUpdates(tpAttached)

	// An unattached query connection connects
	tpQuery := transport.NewInProcChannel(16)
	s.mu.Lock()
	s.transports = append(s.transports, tpQuery)
	s.mu.Unlock()

	// Query connection leaves without ever sending MsgAttach
	s.dropClient(ctx, tpQuery)

	// Attached client should receive nothing: no layout snapshot, no pane updates
	if got := drainPaneUpdates(tpAttached); len(got) != 0 {
		t.Fatalf("unattached disconnect triggered pane resends: %v, want none", got)
	}

	// Verify no layout snapshot was queued for the attached client either
	select {
	case msg := <-tpAttached.ServerSendChan():
		t.Fatalf("unattached disconnect sent unexpected message: %+v", msg)
	default:
	}
}

// A layout change that only updates status or title must not force
// resends of unchanged panes.
func TestStatusOnlyChangeDoesNotResendUnchangedPanes(t *testing.T) {
	s, grids, tp := twoIdlePanes(t)
	ctx := context.Background()

	// Initial broadcast establishes baseline layout, statuses, and pane generations
	s.broadcastLayout(ctx)
	drainPaneUpdates(tp)
	// Clear snapshot message
	select {
	case <-tp.ServerSendChan():
	default:
	}

	// Flip pane 1 status from idle to working (e.g. prompt command started)
	grids[1].set(protocol.StatusWorking)
	if !s.broadcastLayoutIfStatusChanged(ctx) {
		t.Fatal("broadcastLayoutIfStatusChanged reported false after status change")
	}

	// Attached client must receive the layout snapshot reflecting the new status
	select {
	case msg := <-tp.ServerSendChan():
		snap, ok := msg.(protocol.MsgLayoutSnapshot)
		if !ok {
			t.Fatalf("got %T, want MsgLayoutSnapshot", msg)
		}
		if snap.PaneStatuses[1] != protocol.StatusWorking {
			t.Errorf("pane 1 status = %v, want StatusWorking", snap.PaneStatuses[1])
		}
	default:
		t.Fatal("did not receive layout snapshot after status change")
	}

	// Unchanged panes (pane 1 and pane 2 have no cell/cursor changes) must not be resent
	if got := drainPaneUpdates(tp); len(got) != 0 {
		t.Fatalf("status-only broadcast resent unchanged panes: %v, want none", got)
	}
}

// When a column is added, removed, or resized, broadcastLayout must
// force pane resends so clients can reallocate/populate their mirrors.
func TestColumnOrSizeChangeForcesPaneResend(t *testing.T) {
	s, _, tp := twoIdlePanes(t)
	ctx := context.Background()

	// Initial broadcast
	s.broadcastLayout(ctx)
	drainPaneUpdates(tp)
	// Drain snapshot
	for len(tp.ServerSend) > 0 {
		<-tp.ServerSendChan()
	}

	// Resize pane 1 width
	s.mu.Lock()
	s.strip.GrowWidth(1, 10)
	s.resizePanesLocked()
	s.mu.Unlock()

	s.broadcastLayout(ctx)

	// Since pane dimensions changed, broadcastLayout must force resend
	got := drainPaneUpdates(tp)
	sort.Ints(got)
	if !slices.Equal(got, []int{1, 2}) {
		t.Fatalf("size change did not force resend of all panes: got %v, want [1 2]", got)
	}
}
