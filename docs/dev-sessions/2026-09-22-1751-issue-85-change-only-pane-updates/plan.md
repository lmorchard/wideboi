# Change-only pane updates Implementation Plan

**Goal:** An idle session sends ~0 `MsgPaneUpdate`s. The server renders and sends a pane only for clients
that haven't accepted its current state.

**Approach:**
- `term.Grid` gains a `Generation()` counter, bumped on every path that changes what a pane update carries.
- The server records, per transport, the last generation each pane was delivered at, and sends only on a
  mismatch.
- `broadcastLayout` forces a full resend, because snapshots can prune or blank client mirrors.
- Pane broadcasts are serialized, so delivery order and recorded generations cannot disagree.

**Tech stack:** Go, `charmbracelet/x/vt` (behind `term.Grid`), gob-over-unix-socket transport.

Conventions for every phase:
- `go test` runs use `-count=1`.
- Every new test is proven red for the right reason: break the guarded line, see it fail, restore.
- Timing-sensitive tests are run 4×.

---

## Phase 1: Grid generation counter

The grid gains a counter that advances whenever a pane's rendered state may have changed. Nothing reads it yet.

**Files:**
- Modify: `internal/server/term/grid.go`:
  - `Generation()` on the `Grid` interface
  - a `generation atomic.Uint64` field on `vtGrid`
  - bumps in `Write`, `Resize` and `SetScrollOffset`
- Modify: `internal/server/pane.go` — `func (p *Pane) Generation() uint64 { return p.grid.Generation() }`
- Modify: `internal/server/status_test.go` — `statusGrid` gets `gen atomic.Uint64`, `Generation()` and `bump()`
- Modify: `internal/server/pane_wedge_test.go` — `blockingGrid.Generation()` returns 0
- Test: `internal/server/term/grid_test.go` (package `term_test`)

**Key changes:**

```go
// in the Grid interface, after SetScrollOffset:

// Generation advances whenever anything a pane update carries may have
// changed: cells, cursor position or visibility, mouse modes, size, or
// scroll offset. Only inequality is meaningful. The server reads it
// *before* rendering, so a change racing the render errs toward one
// update too many, never one too few.
//
// Every new path that mutates what Draw, CursorPosition, CursorVisible
// or MouseTracking report must bump it. One that doesn't leaves every
// attached client stale until something unrelated changes the pane.
Generation() uint64
```

```go
func (g *vtGrid) Write(p []byte) (int, error) {
	g.writeResizeMu.Lock()
	defer g.writeResizeMu.Unlock()
	// ... existing lastWriteTime / status lines unchanged ...
	n, err := g.em.Write(p)
	g.generation.Add(1)
	return n, err
}

func (g *vtGrid) Resize(cols, rows int) {
	g.writeResizeMu.Lock()
	defer g.writeResizeMu.Unlock()

	oldCols, oldRows := g.em.Width(), g.em.Height()
	if cols == oldCols && rows == oldRows {
		return
	}
	// Both the alt-screen and reflow paths below change the grid.
	defer g.generation.Add(1)
	// ... unchanged ...
}

func (g *vtGrid) SetScrollOffset(offset int) {
	// ... existing clamp unchanged ...
	if g.scrollOffset.Swap(int32(offset)) != int32(offset) {
		g.generation.Add(1)
	}
}

func (g *vtGrid) Generation() uint64 { return g.generation.Load() }
```

Tests (`grid_test.go`):

```go
func TestGenerationAdvancesOnWrite(t *testing.T) {
	g := term.NewVT(10, 3)
	defer g.Close()
	before := g.Generation()
	if _, err := g.Write([]byte("hi")); err != nil {
		t.Fatal(err)
	}
	if g.Generation() == before {
		t.Error("Write did not advance the generation")
	}
}

func TestGenerationAdvancesOnResizeOnlyWhenTheSizeChanges(t *testing.T) {
	g := term.NewVT(10, 3)
	defer g.Close()
	before := g.Generation()
	g.Resize(10, 3)
	if g.Generation() != before {
		t.Error("a same-size Resize advanced the generation")
	}
	g.Resize(8, 3)
	if g.Generation() == before {
		t.Error("Resize did not advance the generation")
	}
}

func TestGenerationAdvancesOnScrollOnlyWhenTheOffsetMoves(t *testing.T) {
	g := term.NewVT(10, 3)
	defer g.Close()
	for i := 0; i < 10; i++ {
		fmt.Fprintf(g, "%d\r\n", i)
	}
	if g.ScrollbackLen() < 2 {
		t.Fatalf("fixture produced %d scrollback lines, need >= 2", g.ScrollbackLen())
	}
	steps := []struct {
		offset int
		moves  bool
	}{
		{0, false},  // already at the bottom
		{2, true},
		{2, false},  // unchanged
		{-5, true},  // clamps to 0, which is a move from 2
		{-1, false}, // clamps to 0 again
	}
	for _, st := range steps {
		before := g.Generation()
		g.SetScrollOffset(st.offset)
		if moved := g.Generation() != before; moved != st.moves {
			t.Errorf("SetScrollOffset(%d): generation moved=%v, want %v", st.offset, moved, st.moves)
		}
	}
}
```

`statusGrid` additions (`status_test.go`):

```go
	gen    atomic.Uint64 // new field on statusGrid

func (g *statusGrid) bump()              { g.gen.Add(1) }
func (g *statusGrid) Generation() uint64 { return g.gen.Load() }
```

**Verification — automated:**
- [x] New tests fail before implementation (compile failure on `Generation`), then pass — **build failed: `g.Generation undefined`; then 3/3 PASS**
- [x] Red for the right reason: remove the bump in each of `Write`, `Resize` and `SetScrollOffset` in turn, and
      replace the `Swap` comparison with an unconditional bump. The matching test fails each time. Restore.
      — **each sabotage failed its matching test (Write / Resize / SetScrollOffset(2,-5) / unconditional (0,2,-1)); restored, diff clean**
- [x] `go test -count=1 ./internal/server/term/ -run Generation -v` — **3 PASS**
- [x] `make quick` passes — **all packages ok**

**Verification — manual:**
- [x] None. There is no behaviour change yet.

---

## Phase 2: Change-only broadcast with per-client delivery tracking

The server skips panes a client already has, retries drops per client, gives new clients everything, and
forces a resend on every layout broadcast. The wire test proves the idle behaviour end to end.

**Files:**
- Modify: `internal/server/server.go`:
  - `paneGens` and `paneSendMu` fields on `Server`
  - `broadcastPaneUpdates(ctx, force bool)`
  - forget a transport's records in `removeTransportLocked` and `Close`
  - the Run loop passes `false`; `broadcastLayout` passes `true`
  - update the self-heal comment in `broadcastLayoutIfStatusChanged` (server.go:629-637), which says pane
    updates "self-heal" by repetition
- Create: `internal/server/paneupdate_test.go` (package `server`, white-box, reuses `serverWithStatuses`)
- Modify: `cmd/wideboi/main_test.go` — `TestIdleSessionStopsSendingPaneUpdates`

**Key changes:**

```go
	// paneGens records, per client, the grid generation each pane was
	// at in the last update that client accepted. The frame tick sends a
	// pane only to clients whose record is missing or behind, so a new
	// client gets everything and a dropped update is retried for the
	// client that missed it. See broadcastPaneUpdates.
	paneGens map[transport.Transport]map[int]uint64

	// paneSendMu serializes broadcastPaneUpdates. The Run loop, every
	// client's message loop (via broadcastLayout) and onPaneExit all
	// broadcast, and two overlapping rounds could deliver an older
	// render last while recording the newer generation, leaving that
	// client stale with nothing left to trigger a resend. Taken before
	// s.mu, never while holding it.
	paneSendMu sync.Mutex
```

```go
// broadcastPaneUpdates sends each pane to every client that has not
// accepted its current generation. force sends every pane to every
// client: broadcastLayout needs that, because a snapshot can prune a
// client's mirror or replace it with a blank one (client.go, the
// MsgLayoutSnapshot case), so an unchanged pane must still be resent
// after one.
func (s *Server) broadcastPaneUpdates(ctx context.Context, force bool) {
	s.paneSendMu.Lock()
	defer s.paneSendMu.Unlock()

	type outgoing struct {
		update protocol.MsgPaneUpdate
		gen    uint64
		to     []transport.Transport
	}
	s.mu.Lock()
	tps := append([]transport.Transport{}, s.transports...)
	var out []outgoing
	for id, p := range s.panes {
		// Read before rendering. A write landing in between leaves
		// the recorded generation behind the content, so the next
		// tick resends: one update too many, never one too few.
		gen := p.Generation()
		var to []transport.Transport
		for _, tp := range tps {
			last, ok := s.paneGens[tp][id]
			if force || !ok || last != gen {
				to = append(to, tp)
			}
		}
		if len(to) > 0 {
			out = append(out, outgoing{update: p.UpdateMessage(), gen: gen, to: to})
		}
	}
	s.mu.Unlock()

	type delivery struct {
		tp  transport.Transport
		id  int
		gen uint64
	}
	var delivered []delivery
	for _, o := range out {
		for _, tp := range o.to {
			if tp.SendServer(ctx, o.update) {
				delivered = append(delivered, delivery{tp, o.update.PaneID, o.gen})
			}
		}
	}
	if len(delivered) == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	present := make(map[transport.Transport]bool, len(s.transports))
	for _, tp := range s.transports {
		present[tp] = true
	}
	if s.paneGens == nil {
		s.paneGens = make(map[transport.Transport]map[int]uint64)
	}
	for _, d := range delivered {
		// A client dropped mid-send must not be re-added, and a pane
		// that exited mid-send has nothing left to track.
		if !present[d.tp] {
			continue
		}
		if _, ok := s.panes[d.id]; !ok {
			continue
		}
		m := s.paneGens[d.tp]
		if m == nil {
			m = make(map[int]uint64)
			s.paneGens[d.tp] = m
		}
		m[d.id] = d.gen
	}
	for _, m := range s.paneGens {
		for id := range m {
			if _, ok := s.panes[id]; !ok {
				delete(m, id)
			}
		}
	}
}
```

- `removeTransportLocked`: add `delete(s.paneGens, tp)`.
- `Close`: next to `s.transports = nil`, add `s.paneGens = nil`.
- Run loop: `s.broadcastPaneUpdates(ctx, false)`.
- `broadcastLayout` tail: `s.broadcastPaneUpdates(ctx, true)`.
- Replace the self-heal paragraph in `broadcastLayoutIfStatusChanged` (server.go:629-637) with this, keeping the
  retry reasoning:

  > It is also what marks the glyph set delivered, deliberately not done here. A status broadcast is
  > edge-triggered. If this snapshot is dropped (SendServer returns false on a full buffer) and we had already
  > recorded the set as sent, the client would stay stale until some later, unrelated status change. Leaving
  > lastStatuses untouched on a failed send makes the next tick retry. Pane updates have the same problem and
  > solve it per client instead; see paneGens.

Unit tests (`paneupdate_test.go`):

```go
package server

// White-box, same justification as status_test.go: drives
// broadcastPaneUpdates directly against fake grids.

import (
	"context"
	"slices"
	"sort"
	"testing"

	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/server/term"
	"github.com/lmorchard/wideboi/internal/transport"
)

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

	s.dropClient(tp)
	if _, ok := s.paneGens[tp]; ok {
		t.Error("records for a dropped client survived dropClient")
	}
}
```

Wire test (`cmd/wideboi/main_test.go`). The unit tests use InProc; this one runs over a real socket, the
transport every session uses since #25:

```go
// An idle session must go quiet on the wire. Before #85 the server sent
// every pane to every client each 33ms frame whether or not it had
// changed, so no quiet second ever came. Change-only sends must still
// deliver a change, which the second half checks.
func TestIdleSessionStopsSendingPaneUpdates(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "s.sock")
	sl, err := transport.NewSocketListener(sockPath)
	if err != nil {
		t.Fatalf("NewSocketListener failed: %v", err)
	}
	defer sl.Close()

	srv := server.NewServer(nil, "/bin/sh", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.ListenSocket(ctx, sl)
	go func() { _ = srv.Run(ctx) }()
	defer srv.Close()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("net.Dial failed: %v", err)
	}
	cc := transport.NewClientSocketConn(conn, 256)
	cc.RunPumps(ctx)
	defer cc.Close()
	cli := client.NewClient(cc, 80, 24, "C-b")
	cli.Attach(ctx)

	// waitQuiet reports whether a stretch of `quiet` with no
	// MsgPaneUpdate arrives before `ceiling`. Every message still goes
	// through the client, so the focus pane is known for SendInput.
	waitQuiet := func(quiet, ceiling time.Duration) bool {
		deadline := time.After(ceiling)
		timer := time.NewTimer(quiet)
		defer timer.Stop()
		for {
			select {
			case msg := <-cc.ServerSendChan():
				cli.HandleServerMsg(msg)
				if _, ok := msg.(protocol.MsgPaneUpdate); ok {
					timer.Reset(quiet)
				}
			case <-timer.C:
				return true
			case <-deadline:
				return false
			}
		}
	}
	// The ceiling covers shell startup and the Working->Idle status
	// decay at ~3s, whose snapshot forces one more round of updates.
	if !waitQuiet(time.Second, 8*time.Second) {
		t.Fatal("an idle session never went a full second without a MsgPaneUpdate")
	}

	cli.SendInput(ctx, []byte("x"))
	deadline := time.After(3 * time.Second)
	for {
		select {
		case msg := <-cc.ServerSendChan():
			cli.HandleServerMsg(msg)
			if _, ok := msg.(protocol.MsgPaneUpdate); ok {
				return
			}
		case <-deadline:
			t.Fatal("typing into an idle pane produced no MsgPaneUpdate")
		}
	}
}
```

**Verification — automated:**
- [x] Before implementation, `TestIdleSessionStopsSendingPaneUpdates` fails with "never went a full second".
      Record the actual output. The unit tests fail to compile until `broadcastPaneUpdates` takes `force`.
      — **`main_test.go:250: an idle session never went a full second without a MsgPaneUpdate` (10.06s); unit
      tests: `too many arguments in call to s.broadcastPaneUpdates`.** Adaptation: `t.TempDir()` overflowed
      darwin's 104-byte socket path for this test name; switched to `os.MkdirTemp("", "wb")` per
      `lifecycle_test.go:190`.
- [x] Red for the right reason, once each against the implementation, restoring after each:
  - `force` ignored → `TestLayoutBroadcastResendsEveryPane` fails — **`followed by [], want [1 2]`**
  - `SendServer` result ignored (always record) → `TestDroppedPaneUpdateIsRetriedForThatClientOnly` fails — **`wedged client got [] on retry`**
  - `delete(s.paneGens, tp)` removed → `TestDeliveryRecordsAreForgotten` fails — **`records for a dropped client survived`**
  - the exited-pane prune loop removed → `TestDeliveryRecordsAreForgotten` fails — **`record for exited pane 2 survived`** (first attempt didn't compile; redone removing only `delete(m, id)`)
  - [!] the typing half: make `vtGrid.Write` skip its bump → the second half of the wire test fails —
        **DID NOT HOLD as planned: the test passed.** Typing into an idle pane flips status Idle→Working, and that
        snapshot forces a resend of every pane, so the keystroke arrived without any bump. Test reworked: the first
        key wakes the pane, wait for a 300ms quiet stretch (inside the 3s decay), then a second key must arrive with
        no snapshot before it. Re-sabotaged: **`typing "y" produced no MsgPaneUpdate`**. See notes.md.
- [x] `go test -count=1 ./internal/server/ -run 'Pane|Resent|Retried|Forgotten' -v` — **all PASS incl. 5 new**
- [x] `go test -count=1 -race ./internal/server/ ./cmd/wideboi/` — **ok (server/..., cmd/wideboi)**
- [x] Wire test ×4: `go test -count=4 ./cmd/wideboi/ -run TestIdleSessionStopsSendingPaneUpdates -v` — **4/4 PASS, 3.42s each (reworked version)**
- [x] `make quick` passes — **all ok**

**Verification — manual:**
- [x] None yet; Phase 3 runs the binary.

---

## Phase 3: Docs, lessons, and the full gate

Update comments and docs that describe the old behaviour. Record the new hazard in LESSONS, then run the
binary and the full gate.

**Files:**
- Modify: `internal/client/client.go:208-209` — the trace comment no longer says the message fires "whether or
  not anything changed":
  ```go
  // Trace, not Debug: busy panes still send one of these per
  // server frame.
  ```
- Modify: `docs/LESSONS.md` — new section, placed after "The harness's environment pin…":

  > ## Change-only sends trade self-healing for bookkeeping
  >
  > Until #85 the server resent every pane to every client on each 33ms frame. That was wasteful, and it also
  > repaired every dropped or out-of-order update a frame later without anyone having to think about it. Sending
  > only on change removed that. Three things now carry the guarantee instead, and each one fails silently as a
  > stale pane:
  >
  > - **Every mutation path bumps `Grid.Generation`.** A path that changes cells, cursor, mouse mode, size or
  >   scroll offset without bumping never reaches a client until something unrelated changes that pane.
  > - **Every layout snapshot forces a full resend.** The client prunes mirrors of panes that aren't placed, and
  >   replaces a mirror that a placement outgrew with a blank one. An "unchanged" pane can therefore be missing
  >   on the client.
  > - **Pane broadcasts are serialized (`paneSendMu`).** Several goroutines broadcast. Two overlapping rounds
  >   could deliver an older render last while recording the newer generation.
  >
  > Delivery is tracked per client (`paneGens`), recorded only when `SendServer` accepts. There is deliberately
  > no periodic full resend: it would hide a missing bump.

**Verification — automated:**
- [x] `make check` passes — **smoke, attach-check (17 passed), race, verify-exit all green**
- [x] `make check` ×4 in total. Per memory, parallel check has a ~1-in-6 load flake on main. Record which cases
      fail, if any, and compare them against main before blaming the branch. — **4/4 PASS, no failures**

**Verification — manual:**
- [x] `make build`, then `WIDEBOI_LOG_LEVEL=trace ./bin/wideboi`. Leave it idle 10s, then
      `grep -c 'received MsgPaneUpdate'` over the idle stretch of `$TMPDIR/wideboi-$(id -u)/client.log`. Expect a
      burst at startup and around the ~3s status decay, then ~0.
- [x] Type in a pane; output appears promptly.
- [x] Scroll back and forward in a pane; the view follows (generation bump on offset).
- [x] Open columns until the strip scrolls, move focus so a pane goes off-screen and back; it renders with its
      content, not blank (forced resend after snapshot).
- [x] Resize the host terminal; panes redraw correctly.
- [x] Attach a second client with `wideboi attach` to the running session; it shows every pane immediately. — **confirmed by Les, 2026-09-23 (items 1–6)**
