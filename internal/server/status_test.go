package server

// White-box (package server, not server_test): these drive unexported
// server internals and build *Pane values with a fake Grid directly,
// which only this package can do. Same justification as
// pane_wedge_test.go.

import (
	"context"
	"image"
	"sync/atomic"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/layout"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/server/term"
	"github.com/lmorchard/wideboi/internal/transport"
)

// statusGrid is a term.Grid that reports whatever status the test sets.
// Every other method is a cheap stub; these tests do not exercise them.
type statusGrid struct {
	status atomic.Int32
	title  atomic.Pointer[string]
}

func newStatusGrid(st term.PaneStatus) *statusGrid {
	g := &statusGrid{}
	g.status.Store(int32(st))
	return g
}

func (g *statusGrid) set(st term.PaneStatus) { g.status.Store(int32(st)) }

func (g *statusGrid) setTitle(s string) { g.title.Store(&s) }

func (g *statusGrid) Title() string {
	if t := g.title.Load(); t != nil {
		return *t
	}
	return ""
}

func (g *statusGrid) Status() term.PaneStatus { return term.PaneStatus(g.status.Load()) }

func (g *statusGrid) Write(p []byte) (int, error)     { return len(p), nil }
func (g *statusGrid) Read(p []byte) (int, error)      { return 0, nil }
func (g *statusGrid) SendKey(uv.KeyEvent)             {}
func (g *statusGrid) SendText(string)                 {}
func (g *statusGrid) MouseTracking() bool             { return false }
func (g *statusGrid) SendMouse(uv.MouseEvent)         {}
func (g *statusGrid) CursorPosition() image.Point     { return image.Point{} }
func (g *statusGrid) CursorVisible() bool             { return false }
func (g *statusGrid) ScrollbackLen() int              { return 0 }
func (g *statusGrid) ScrollOffset() int               { return 0 }
func (g *statusGrid) SetScrollOffset(int)             {}
func (g *statusGrid) Draw(uv.Screen, image.Rectangle) {}
func (g *statusGrid) CellAt(x, y int) *uv.Cell        { return nil }
func (g *statusGrid) Size() (int, int)                { return 10, 10 }
func (g *statusGrid) Resize(cols, rows int)           {}
func (g *statusGrid) Close() error                    { return nil }

// serverWithStatuses builds a server holding one pane per entry, with
// pane IDs taken from the map keys.
func serverWithStatuses(t *testing.T, statuses map[int]term.PaneStatus) (*Server, map[int]*statusGrid) {
	t.Helper()
	s := &Server{
		strip:      layout.NewStrip(),
		panes:      make(map[int]*Pane),
		cols:       80,
		rows:       24,
		escapees:   make(map[int]string),
		stopCh:     make(chan struct{}),
		transports: []transport.Transport{transport.NewInProcChannel(64)},
	}
	grids := make(map[int]*statusGrid, len(statuses))
	for id, st := range statuses {
		g := newStatusGrid(st)
		grids[id] = g
		s.panes[id] = &Pane{id: id, grid: g, cols: 40, rows: 22}
		s.strip.AddColumn(id, 40, 22)
	}
	return s, grids
}

// PaneStatuses rides on MsgLayoutSnapshot, which is otherwise only sent
// for verbs, spawns and kills. Without a status-driven broadcast, a
// glyph change from OSC 133 sits invisible until the user happens to
// press a verb key -- which is exactly what happened when the handler
// was first fixed: unit tests green, nothing on the wire.
func TestStatusChangeTriggersALayoutBroadcast(t *testing.T) {
	s, grids := serverWithStatuses(t, map[int]term.PaneStatus{1: term.StatusIdle})
	ctx := context.Background()

	// First call always reports a change: lastStatuses starts nil.
	if !s.broadcastLayoutIfStatusChanged(ctx) {
		t.Fatal("first call did not broadcast; it should establish the baseline")
	}
	// Nothing moved, so nothing should be sent.
	if s.broadcastLayoutIfStatusChanged(ctx) {
		t.Error("broadcast fired with no status change")
	}

	grids[1].set(term.StatusFailed)
	if !s.broadcastLayoutIfStatusChanged(ctx) {
		t.Error("broadcast did not fire after a status change")
	}
	if s.broadcastLayoutIfStatusChanged(ctx) {
		t.Error("broadcast fired twice for one status change")
	}
}

// An idle session must stay quiet: the frame ticker runs at 33ms, and a
// snapshot on every tick would be 30 full layout messages a second per
// client for no reason.
func TestUnchangedStatusesDoNotBroadcast(t *testing.T) {
	s, _ := serverWithStatuses(t, map[int]term.PaneStatus{
		1: term.StatusWorking,
		2: term.StatusIdle,
	})
	ctx := context.Background()
	s.broadcastLayoutIfStatusChanged(ctx) // baseline

	for i := 0; i < 20; i++ {
		if s.broadcastLayoutIfStatusChanged(ctx) {
			t.Fatalf("broadcast fired on tick %d with no status change", i)
		}
	}
}

// A status broadcast is edge-triggered: unlike the 33ms pane updates it
// does not repeat, so a dropped snapshot would leave the client stale
// until some later, unrelated status change. Marking the glyph set
// delivered before the send succeeds is what would cause that.
func TestUndeliveredStatusBroadcastIsRetried(t *testing.T) {
	s, grids := serverWithStatuses(t, map[int]term.PaneStatus{1: term.StatusIdle})
	ctx := context.Background()

	// Drain nothing and fill the buffer, so SendServer's non-blocking
	// send fails for every attempt.
	tp := s.transports[0].(*transport.InProcChannel)
	for len(tp.ServerSend) < cap(tp.ServerSend) {
		tp.ServerSend <- struct{}{}
	}

	grids[1].set(term.StatusFailed)
	if !s.broadcastLayoutIfStatusChanged(ctx) {
		t.Fatal("no broadcast attempted after a status change")
	}

	// Undelivered, so the next tick must try again rather than treat
	// the change as sent.
	if !s.broadcastLayoutIfStatusChanged(ctx) {
		t.Error("status change was marked delivered despite every send failing")
	}

	// Once the client drains, the retry lands and the set goes clean.
	for len(tp.ServerSend) > 0 {
		<-tp.ServerSend
	}
	if !s.broadcastLayoutIfStatusChanged(ctx) {
		t.Fatal("expected the retry to fire")
	}
	if s.broadcastLayoutIfStatusChanged(ctx) {
		t.Error("kept broadcasting after a successful delivery")
	}
}

// The toggle is a server-side flip of shared session state; clients
// learn about it from the next snapshot.
func TestToggleCardsFlipsLayoutMode(t *testing.T) {
	s, _ := serverWithStatuses(t, map[int]term.PaneStatus{1: term.StatusIdle, 2: term.StatusIdle})
	ctx := context.Background()

	if s.layout != protocol.LayoutScroll {
		t.Fatalf("initial layout = %v, want %v", s.layout, protocol.LayoutScroll)
	}
	s.handleClientMsg(ctx, protocol.MsgVerb{Verb: protocol.VerbToggleCards})
	if s.layout != protocol.LayoutCards {
		t.Errorf("after one toggle layout = %v, want %v", s.layout, protocol.LayoutCards)
	}
	s.handleClientMsg(ctx, protocol.MsgVerb{Verb: protocol.VerbToggleCards})
	if s.layout != protocol.LayoutScroll {
		t.Errorf("after two toggles layout = %v, want %v", s.layout, protocol.LayoutScroll)
	}
}

// The no-shrink premise is what this whole project rests on: a pane's
// logical width is its column's width, independent of what is visible.
// Switching presentation must not touch it.
func TestToggleCardsLeavesColumnWidthsAlone(t *testing.T) {
	s, _ := serverWithStatuses(t, map[int]term.PaneStatus{1: term.StatusIdle, 2: term.StatusIdle, 3: term.StatusIdle})
	ctx := context.Background()

	before := map[int]int{}
	for _, id := range s.strip.PaneIDs() {
		w, ok := s.strip.ColumnWidth(id)
		if !ok {
			t.Fatalf("no width for pane %d", id)
		}
		before[id] = w
	}

	s.handleClientMsg(ctx, protocol.MsgVerb{Verb: protocol.VerbToggleCards})

	for id, want := range before {
		got, ok := s.strip.ColumnWidth(id)
		if !ok {
			t.Fatalf("pane %d lost its column across the toggle", id)
		}
		if got != want {
			t.Errorf("pane %d width %d -> %d across a layout toggle; "+
				"presentation must not resize", id, want, got)
		}
	}
}

// Titles ride the same change-detected broadcast as status glyphs:
// they are the other thing a card sliver renders, and they change on
// their own schedule (an agent harness rewrites its title mid-turn).
// A title change with no status change must still reach the client.
func TestTitleChangeTriggersALayoutBroadcast(t *testing.T) {
	s, grids := serverWithStatuses(t, map[int]term.PaneStatus{1: term.StatusIdle})
	ctx := context.Background()

	s.broadcastLayoutIfStatusChanged(ctx) // baseline
	if s.broadcastLayoutIfStatusChanged(ctx) {
		t.Fatal("broadcast fired with nothing changed")
	}

	grids[1].setTitle("building")
	if !s.broadcastLayoutIfStatusChanged(ctx) {
		t.Error("a title change did not trigger a broadcast")
	}
	if s.broadcastLayoutIfStatusChanged(ctx) {
		t.Error("kept broadcasting after the title change was delivered")
	}
}

// The title has to survive the trip, not just trigger a send.
func TestPaneTitlesReachTheSnapshot(t *testing.T) {
	s, grids := serverWithStatuses(t, map[int]term.PaneStatus{1: term.StatusIdle})
	grids[1].setTitle("◑ Pong reply")

	s.broadcastLayout(context.Background())

	tp := s.transports[0].(*transport.InProcChannel)
	var snap protocol.MsgLayoutSnapshot
	for len(tp.ServerSend) > 0 {
		if m, ok := (<-tp.ServerSend).(protocol.MsgLayoutSnapshot); ok {
			snap = m
		}
	}
	if got := snap.PaneTitles[1]; got != "◑ Pong reply" {
		t.Errorf("snapshot PaneTitles[1] = %q, want %q", got, "◑ Pong reply")
	}
}

// "Delivered" has to mean every attached client got it, not any one.
// A status or title broadcast is edge-triggered, so a client whose
// buffer was full when it fired never sees that change again -- the
// session goes inconsistent between clients and nothing retries.
func TestUndeliveredToOneOfTwoClientsIsRetried(t *testing.T) {
	s, grids := serverWithStatuses(t, map[int]term.PaneStatus{1: term.StatusIdle})
	ctx := context.Background()

	healthy := s.transports[0].(*transport.InProcChannel)
	wedged := transport.NewInProcChannel(4)
	s.transports = append(s.transports, wedged)
	for len(wedged.ServerSend) < cap(wedged.ServerSend) {
		wedged.ServerSend <- struct{}{}
	}

	grids[1].set(term.StatusFailed)
	if !s.broadcastLayoutIfStatusChanged(ctx) {
		t.Fatal("no broadcast attempted after a status change")
	}
	// The healthy client got it; the wedged one did not, so the
	// change is not done.
	for len(healthy.ServerSend) > 0 {
		<-healthy.ServerSend
	}
	if !s.broadcastLayoutIfStatusChanged(ctx) {
		t.Error("marked delivered while one attached client never received it")
	}

	// Once it drains, the retry lands and the set finally goes clean.
	for len(wedged.ServerSend) > 0 {
		<-wedged.ServerSend
	}
	if !s.broadcastLayoutIfStatusChanged(ctx) {
		t.Fatal("expected the retry to fire")
	}
	if s.broadcastLayoutIfStatusChanged(ctx) {
		t.Error("kept broadcasting after every client accepted")
	}
}
