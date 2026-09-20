# Plan 16 — Wipe frames and OSC 133 Implementation Plan

**Goal:** Make the focus-change wipe animate real frames and make OSC 133 drive
pane status, and build the `Client.Draw` test seam whose absence let both ship
green and broken.

**Approach:** Extract the pane-compositing half of `Draw` into
`composeFrameLocked`, which is both the wipe fix (compose A from retained
layout state, B from current) and the test seam. Fix the OSC 133 handler to
split its payload the way the vt library's own handlers do, remap `A`/`B` to
`NeedsInput`, and rank smart-jump targets instead of taking the first match.

**Tech stack:** Go, `charmbracelet/ultraviolet` (cell surfaces),
`charmbracelet/x/vt` (emulator), Python for the pty-level smoke suite.

**Commit per phase:** `Phase N: <name>`.

---

## Phase 1: A screen interface `Draw` can be tested against

Pure infrastructure, no behaviour change. `Draw` takes `*uv.TerminalScreen`,
which a unit test cannot cheaply construct — that is the mechanical reason no
test has ever called it. Narrow the parameter to an interface that both the
real terminal screen and a test double satisfy.

**TDD opt-out:** this phase is a signature change with no behaviour. The test
it adds asserts the harness works, and passes on arrival. Phase 2 supplies the
failing test.

**Files:**
- Modify: `internal/client/client.go` — add `HostScreen`, change `Draw`'s first parameter.
- Create: `internal/client/screen_test.go` — `fakeHostScreen` + a smoke-level "Draw renders panes" test.

**Key changes:**

`uv.Screen` is `{Bounds, CellAt, SetCell, WidthMethod}` (`ultraviolet/uv.go:41-57`).
`*uv.TerminalScreen` adds the three cursor methods `Draw` uses
(`terminal_screen.go:375,386,405`); `uv.ScreenBuffer` has none of them but does
satisfy `uv.Screen` by value (`buffer.go:609`, `_ Screen = ScreenBuffer{}`).

```go
// HostScreen is everything Draw needs from the host terminal: a cell
// surface, plus cursor control.
//
// Draw used to take *uv.TerminalScreen concretely. That is a type a unit
// test cannot cheaply build, which is why nothing ever called Draw from
// a test -- and why a wipe that interpolated two blank frames shipped
// green. uv.ScreenBuffer satisfies uv.Screen but has no cursor methods,
// so the three Draw actually uses are named here and a test double
// supplies them.
type HostScreen interface {
	uv.Screen
	HideCursor()
	ShowCursor()
	SetCursorPosition(x, y int)
}

func (c *Client) Draw(scr HostScreen, drawPane func(id int, dst uv.Screen, area image.Rectangle), cursorInfo func(id int) (image.Point, bool))
```

`cmd/wideboi/main.go:204` and `:343` pass a `*uv.TerminalScreen` and need no
change — it satisfies the interface.

Test double:

```go
// fakeHostScreen is a HostScreen backed by an off-screen surface, so a
// test can call Draw and then read the cells back.
type fakeHostScreen struct {
	compose.Surface
	cursorShown bool
	cursorX     int
	cursorY     int
}

func newFakeHostScreen(cols, rows int) *fakeHostScreen {
	return &fakeHostScreen{Surface: compose.NewSurface(cols, rows)}
}

func (f *fakeHostScreen) HideCursor()                 { f.cursorShown = false }
func (f *fakeHostScreen) ShowCursor()                 { f.cursorShown = true }
func (f *fakeHostScreen) SetCursorPosition(x, y int)  { f.cursorX, f.cursorY = x, y }
```

The test builds a client over `transport.NewInProcChannel(16)`, feeds one
`MsgLayoutSnapshot` with two `ColumnData` plus a `MsgPaneUpdate` carrying cells
for each pane, calls `Draw(scr, nil, nil)` (the mirror path — `drawPane` nil),
and asserts the pane text appears via `compose.Text`.

**Verification — automated:**
- [x] `go build ./...` succeeds (proves `*uv.TerminalScreen` satisfies `HostScreen`) — **exit 0, no call-site changes needed**
- [x] `go test ./internal/client/ -run TestDrawRendersPaneContent -v` passes — **PASS, finds PANE-ONE / PANE-TWO / status bar on the composited screen**
- [x] `make lint` passes — **`go vet ./...` clean**
- [x] `make test` passes — **all 11 packages ok**

**Verification — manual:**
- [x] `make run`, confirm the UI is visually unchanged from before this phase. — **substituted an objective check: `python3 scripts/golden.py` reports "wire output matches the golden snapshot", i.e. the startup byte stream is identical to the committed baseline.**

---

## Phase 2: The wipe animates real frames

The defect. `client.go:97-98` builds `fA`/`fB` with `compose.NewSurface` and
never writes into them, so every focus change blanks the screen for 8 frames at
16 ms. Measured on a real pty: one erase, then 18→48 bytes over 96 ms, then 973
bytes at t+128 ms.

**Test first.** Add the failing test before any production change and record
that it fails.

**Files:**
- Modify: `internal/client/client.go` — `frameState`, `pendingWipe`, `composeFrameLocked`, rework `Draw`, rework the focus-change branch of `HandleServerMsg`.
- Create: `internal/client/draw_wipe_test.go`.

**Key changes:**

```go
// frameState is the layout-dependent input to one composed frame.
// Everything else Draw needs (control mode, prefix label, detachable)
// is chrome that does not participate in a transition.
type frameState struct {
	placements   []protocol.PlacementData
	focusPaneID  int
	paneStatuses map[int]string
}

func (c *Client) frameStateLocked() frameState {
	return frameState{
		placements:   c.placements,
		focusPaneID:  c.focusPaneID,
		paneStatuses: c.paneStatuses,
	}
}

// pendingWipe is a focus change that has arrived but has not yet been
// turned into a WipeTransition.
//
// The trigger and the frames live on different paths: HandleServerMsg
// learns that focus moved, but pane content is only reachable inside
// Draw -- through the drawPane callback in-process, or c.mirrors when
// attached. So the message path records what to animate away from and
// the next Draw composes both frames.
type pendingWipe struct {
	dir  Direction
	from frameState
	cols int
	rows int
}

const wipeSteps = 8
```

In `HandleServerMsg`, capture the outgoing state *before* it is overwritten
(today only `oldFocus` is captured, at `client.go:82`):

```go
oldFocus := c.focusPaneID
oldState := c.frameStateLocked()
...
if oldFocus != 0 && c.focusPaneID != oldFocus {
	dir := WipeLeftToRight
	if c.focusPaneID < oldFocus {
		dir = WipeRightToLeft
	}
	// A newer focus change supersedes one still in flight. Restarting
	// from the state just before this change is not true retargeting
	// -- see BEYOND-V1 section 1 -- but it is bounded and never blank.
	c.activeWipe = nil
	c.pendingWipe = &pendingWipe{dir: dir, from: oldState, cols: c.cols, rows: c.rows}
}
```

`composeFrameLocked` is the existing placement loop, verbatim except that it
reads from `st` instead of `c` and writes to a `uv.Screen`. It covers rows
`0 .. rows-2`: headers at row 0, panes and dividers from `Dst.Min.Y = 1` to
`Dst.Max.Y = rows-1` exclusive (`layout.AvailHeight` is `rows-2`,
`internal/layout/layout.go:156-158`). The status bar at row `rows-1` is **not**
part of it.

```go
// composeFrameLocked draws pane headers, pane content and column
// dividers for st into dst, and returns the focused placement (nil if
// st has none).
//
// It deliberately stops short of the status bar and the cursor. The bar
// is chrome that should not dissolve mid-transition, and the cursor is
// hidden for a wipe's duration anyway, so a composed frame covers rows
// 0..rows-2 only. c.mu must be held.
func (c *Client) composeFrameLocked(dst uv.Screen, st frameState, drawPane func(id int, dst uv.Screen, area image.Rectangle)) *protocol.PlacementData
```

Inside, the only substitutions against today's loop body
(`client.go:221-269`) are `c.placements` → `st.placements`, `c.focusPaneID` →
`st.focusPaneID`, `c.paneStatuses` → `st.paneStatuses`, and `scr` → `dst`. The
mirror-vs-callback branch at `client.go:252-256` is preserved exactly:

```go
if drawPane != nil {
	drawPane(p.PaneID, dst, p.Dst)
} else if mirror, ok := c.mirrors[p.PaneID]; ok {
	compose.Blit(dst, mirror.Surface, p.Dst)
}
```

`Draw` gains a realization step before the layer switch, and its two remaining
branches each draw the status bar themselves:

```go
func (c *Client) Draw(scr HostScreen, drawPane func(...), cursorInfo func(...)) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.realizePendingWipeLocked(drawPane)

	switch c.layerLocked() {
	case layerHelp:
		drawHelpOverlay(scr, c.cols, c.rows, c.prefixLabel, c.detachable)
		scr.HideCursor()
		return
	case layerWipe:
		c.activeWipe.Draw(scr)
		c.drawStatusBarLocked(scr)
		scr.HideCursor()
		if c.activeWipe.Step() {
			c.activeWipe = nil
		}
		return
	}

	focusedPlacement := c.composeFrameLocked(scr, c.frameStateLocked(), drawPane)
	c.drawStatusBarLocked(scr)
	// ... existing cursor block, unchanged, using focusedPlacement
}

// realizePendingWipeLocked turns a recorded focus change into a live
// transition, composing both frames. c.mu must be held.
func (c *Client) realizePendingWipeLocked(drawPane func(id int, dst uv.Screen, area image.Rectangle)) {
	pw := c.pendingWipe
	if pw == nil {
		return
	}
	c.pendingWipe = nil
	// A resize between the focus change and this frame invalidates the
	// retained geometry: compositing the old placements at the new size
	// would animate against a layout that never existed. Snap instead.
	if pw.cols != c.cols || pw.rows != c.rows || c.cols <= 0 || c.rows <= 1 {
		return
	}
	h := c.rows - 1
	fA := compose.NewSurface(c.cols, h)
	fB := compose.NewSurface(c.cols, h)
	c.composeFrameLocked(fA, pw.from, drawPane)
	c.composeFrameLocked(fB, c.frameStateLocked(), drawPane)
	c.activeWipe = NewWipeTransition(fA, fB, c.cols, h, pw.dir, wipeSteps)
}

// drawStatusBarLocked paints the bottom row. c.mu must be held.
//
// Leave the final column untouched: ultraviolet's terminal renderer
// writes the last cell of a row with autowrap toggled off and back on
// around it, which splits whatever glyph lands there across a mode
// escape sequence on the wire. Budgeting one cell short of c.cols keeps
// the whole line contiguous in the raw output.
func (c *Client) drawStatusBarLocked(scr uv.Screen) {
	statusText, statusStyle := c.statusLineLocked(c.cols - 1)
	compose.WriteStyled(scr, 0, c.rows-1, statusText, statusStyle)
}
```

`WipeTransition` is constructed with height `c.rows-1` so its blits never touch
the status row. No change to `wipe.go` itself.

**The test** (`draw_wipe_test.go`), which must fail before the change:

```go
// A focus change used to blank the screen for eight frames: Draw built
// its two wipe frames with compose.NewSurface and never drew into
// either. wipe_test.go missed it because it writes its own frames; the
// only production call site did not. Assert against Draw itself.
func TestFocusChangeWipeNeverShowsABlankFrame(t *testing.T) {
	const cols, rows = 60, 12
	cli := newTestClientWithTwoPanes(t, cols, rows) // helper below

	// Move focus 1 -> 2. This is what arms the wipe.
	cli.HandleServerMsg(protocol.MsgLayoutSnapshot{
		Columns: twoColumns(), FocusPaneID: 2,
	})

	for step := 0; step <= wipeSteps; step++ {
		scr := newFakeHostScreen(cols, rows)
		cli.Draw(scr, nil, nil)
		if blankAbove(scr, rows-1) {
			t.Fatalf("frame %d of the transition is blank above the status bar", step)
		}
	}
}

// blankAbove reports whether every cell in rows 0..limit-1 is empty.
// The status row is excluded: it is drawn outside the composed frame.
func blankAbove(scr *fakeHostScreen, limit int) bool {
	for _, line := range compose.Text(scr, image.Rect(0, 0, scr.Bounds().Dx(), limit)) {
		if strings.TrimSpace(line) != "" {
			return false
		}
	}
	return true
}
```

`newTestClientWithTwoPanes` feeds a `MsgLayoutSnapshot` with two
`ColumnData` and `FocusPaneID: 1`, then one `MsgPaneUpdate` per pane whose
`Lines` carry distinctive text (`"PANE-ONE"`, `"PANE-TWO"`), so the mirror
path has something to composite. It is shared by all three tests in this
file and by Phase 1's test.

Two more assertions in the same file:

- `TestFocusChangeWipeEndsOnTheSteadyStateFrame` — after `wipeSteps` draws, the
  composed rows equal what a client with no pending wipe renders for the same
  state.
- `TestResizeDuringPendingWipeSnapsInsteadOfAnimating` — feed the focus-change
  snapshot, then `SendResize` to a different size, then `Draw`; assert
  `layerLocked()` is `layerPanes`, i.e. no wipe was built against stale
  geometry.

**Verification — automated:**
- [x] `go test ./internal/client/ -run TestFocusChangeWipeNeverShowsABlankFrame -v` **fails** before the production change — **`draw_wipe_test.go:40: frame 0 of the transition is blank above the status bar`**
- [x] `go test ./internal/client/ -run 'TestFocusChange|TestResizeDuringPendingWipe' -v` passes after — **3/3 PASS**
- [x] `make test` passes — **all 11 packages ok**
- [x] `make race` passes — **all 11 packages ok under -race -count=1**
- [x] `make check` passes — **23 smoke / golden ok / 7 attach-check, plus lint, seam-check, verify-exit**

Note: `TestFocusChangeWipeEndsOnTheSteadyStateFrame` passed before the change
as well. That is expected and not a plan error — it guards the transition's
endpoint, which was already correct because a completed wipe falls through to
the normal draw path. The plan only claimed the blank-frame test would fail.

**Verification — manual:**
- [x] `make run`, press `C-b l` and `C-b h` a few times: the transition shows content sliding, never an empty screen. — **substituted an objective pty probe rather than an eyeball; see the next box.**
- [x] Re-run the pty probe and confirm bytes are emitted across the transition rather than a ~128 ms silence. — **before: 18/30/48 bytes then 973 at t+128ms. After: 36/629/2500/3010, pane text back by t+64ms. Total 3,712 bytes vs 1,214 for a snap, i.e. ~3.06x.**
- [x] Press `C-b l` twice in rapid succession: bounded, never blank. — **retarget probe: deltas 502/903/502/878 then settles; no silent gap.**

**Unplanned finding.** The measured cost is ~3.06x a snap, where `BEYOND-V1`
section 1's table predicts ~1.9x for a directional column wipe. Not a
regression and not a wrong fix: that table models a *diffed change set*, and
the shipped `WipeTransition` blits whole clipped rects on either side of the
split instead. Same order of magnitude, different constant. Recorded in
`notes.md`; section 1 already flags the divergence between the design and what
Plan 9 built.

---

## Phase 3: OSC 133 parses its payload

`grid.go:180-196` matches `strings.HasPrefix(s, "A")` against a payload that is
actually `"133;A"`. `ansi.Parser.parseStringCmd` reads the leading digits into
`p.cmd` without removing them from `p.data`, which is why all four of vt's own
OSC handlers start with `bytes.Split(data, ';')` and read `parts[1]`
(`x/vt/osc.go:21-25`).

**Test first.** `internal/server/term` has no OSC test at all; this adds the
package's first.

**Files:**
- Create: `internal/server/term/osc_test.go`.
- Modify: `internal/server/term/grid.go` — replace the handler body.

**Key changes:**

```go
g.em.RegisterOscHandler(133, func(data []byte) bool {
	// x/vt hands OSC handlers the whole payload, command number
	// included -- "133;A", not "A". ansi.Parser.parseStringCmd reads
	// the leading digits into p.cmd without removing them from p.data,
	// which is why every one of vt's own OSC handlers starts by
	// splitting on ';' and reading parts[1]. Matching HasPrefix
	// against the raw payload is how this handler stayed dead from the
	// day it was written: no glyph ever rendered and VerbSmartJump
	// never had a target.
	parts := strings.Split(string(data), ";")
	if len(parts) < 2 {
		return false
	}

	var st PaneStatus
	switch parts[1] {
	case "A", "B":
		// A is prompt-start, B is prompt-end, and a shell emits both
		// back to back on every prompt. Mapping B to Working would
		// clobber A microseconds later and leave an idle shell
		// reading as busy, so both mean "waiting on you".
		st = StatusNeedsInput
	case "C":
		st = StatusWorking
	case "D":
		// Bare "D" and "D;0" are success; any other exit-code field
		// is a failure. Split rather than match a ";0" suffix: the
		// payload may carry trailing key=value fields, so
		// "133;D;0;aid=1" is still a success.
		st = StatusDone
		if len(parts) > 2 && parts[2] != "" && parts[2] != "0" {
			st = StatusFailed
		}
	default:
		// Unrecognised. Let vt log it as unhandled, and do not latch
		// sawOSC133 -- the latch also disables the activity fallback
		// in Write and the idle timeout in Status, and one malformed
		// sequence should not switch those off permanently.
		return false
	}

	g.status.Store(int32(st))
	g.sawOSC133.Store(true)
	return true
})
```

Note the reordering: `sawOSC133` now latches *after* a successful match, where
today it latches first (`grid.go:181`) regardless of whether anything matched.

Table test, driving real bytes through `Grid.Write`:

| payload written | want |
| --- | --- |
| `\x1b]133;A\x07` | `StatusNeedsInput` |
| `\x1b]133;B\x07` | `StatusNeedsInput` |
| `\x1b]133;C\x07` | `StatusWorking` |
| `\x1b]133;D\x07` | `StatusDone` |
| `\x1b]133;D;0\x07` | `StatusDone` |
| `\x1b]133;D;1\x07` | `StatusFailed` |
| `\x1b]133;D;130\x07` | `StatusFailed` |
| `\x1b]133;D;0;aid=1\x07` | `StatusDone` |
| `\x1b]133;A;cl=m\x07` | `StatusNeedsInput` |

Plus `TestMalformedOSC133DoesNotLatch`: write `\x1b]133;Z\x07`, then assert the
`Write` activity fallback still works — i.e. a subsequent plain `Write` yields
`StatusWorking`, which it cannot do once `sawOSC133` is set.

**Verification — automated:**
- [x] `go test ./internal/server/term/ -run TestOSC133 -v` **fails** before the handler change — **8 of 9 rows fail** (`A`, `B`, `D`, `D;0`, `D;1`, `D;130`, `D;0;aid=1`, `A;cl=m` all report `Status() = 1`)
- [x] `go test ./internal/server/term/ -run TestOSC133 -v` passes after — **all 9 rows PASS**
- [!] `go test ./internal/server/term/ -run TestMalformedOSC133DoesNotLatch -v` passes — **REPLACED, the planned test was worthless.** It passed *before* the fix too, so it discriminated nothing: right after any `Write` the status is `Working` either way. Split into two that do discriminate: `TestMalformedOSC133StillAllowsALaterValidSequence` (fast) and `TestMalformedOSC133LeavesTheIdleFallbackArmed` (3.1s, asserts through the idle timeout, which is the only fast-reachable observable gated on the latch). Both **fail before, pass after**.
- [x] `make test` passes — **all 11 packages ok**
- [x] `make check` passes — **23 smoke / golden ok / 7 attach-check**

**Verification — manual:**
- [x] `make run`, then in a pane: `printf '\033]133;D;1\007'` — the pane header shows `✗`. — **verified on a real pty by probe, not by eye: `✗` present on the wire.**
- [x] `printf '\033]133;D;0\007'` — the header shows `✓`. — **`✓` present; `A` also confirmed to produce `!`.**

### Unplanned: the status never reached the client

Fixing the handler was **not sufficient**, and the plan missed why. With all
9 unit rows green, no glyph reached the wire.

`PaneStatuses` rides on `MsgLayoutSnapshot`, and `broadcastLayout` only runs
for verbs, spawns and kills (`server.go:155/162/191/226/268`). The 33 ms
`frameTicker` in `Run` only calls `broadcastPaneUpdates`, which carries cells
and not statuses. So an OSC 133 status change sat invisible until the user
happened to press a verb key.

This is squarely inside the spec's stated end state ("a child emitting OSC 133
drives the pane's status glyph"), so it was adapted rather than deferred:

- `Server.lastStatuses` records the glyph set as of the last layout broadcast.
- `statusGlyphsLocked` renders the current set; `broadcastLayout` now uses it
  and keeps `lastStatuses` in sync so an explicit broadcast doesn't cause a
  redundant one on the next tick.
- `broadcastLayoutIfStatusChanged` runs on the frame ticker and sends only
  when the set actually differs, returning whether it did — the tick skips its
  own `broadcastPaneUpdates` when it fired, since `broadcastLayout` ends with
  one.

Covered by `TestStatusChangeTriggersALayoutBroadcast` and
`TestUnchangedStatusesDoNotBroadcast` (an idle session must not emit 30 layout
messages a second). Without these, the new propagation would be exactly the
untested seam this whole plan exists to close.

---

## Phase 4: Smart jump ranks its targets

Once `A`/`B` mean `NeedsInput`, a shell at a prompt is `NeedsInput` nearly all
the time, so `server.go:183-189`'s first-match loop would land arbitrarily —
and `s.panes` is a map, so "first" is not stable between runs.

**Test first.**

**Files:**
- Modify: `internal/server/server.go` — replace the `VerbSmartJump` body, add `smartJumpTargetLocked`.
- Create or modify: `internal/server/smartjump_test.go`.

**Key changes:**

```go
case protocol.VerbSmartJump:
	if id := s.smartJumpTargetLocked(); id > 0 {
		s.strip.FocusPaneID(id)
	}
```

```go
// smartJumpTargetLocked picks the pane most worth jumping to, or 0 when
// nothing wants attention. s.mu must be held.
//
// Priority rather than first match: OSC 133's A and B both mean
// NeedsInput, and a shell sits at a prompt almost all the time, so
// "first pane with an interesting status" would land on whichever idle
// shell the map happened to yield first -- and map order is not stable
// between runs. A failed command outranks a finished one, which
// outranks a prompt. Working and Idle are never targets. Ties break on
// the lowest pane ID so repeated presses are deterministic.
func (s *Server) smartJumpTargetLocked() int {
	rank := func(st term.PaneStatus) int {
		switch st {
		case term.StatusFailed:
			return 3
		case term.StatusDone:
			return 2
		case term.StatusNeedsInput:
			return 1
		default:
			return 0
		}
	}

	bestID, bestRank := 0, 0
	for id, p := range s.panes {
		r := rank(p.Status())
		switch {
		case r == 0:
		case r > bestRank:
			bestID, bestRank = id, r
		case r == bestRank && id < bestID:
			bestID = id
		}
	}
	return bestID
}
```

Tests use the existing `Grid`-interface fake pattern from
`internal/server/pane_wedge_test.go:60`, which already stubs
`Status() term.PaneStatus`:

- `TestSmartJumpPrefersFailedOverDone`
- `TestSmartJumpPrefersDoneOverNeedsInput`
- `TestSmartJumpIgnoresWorkingAndIdle` — all panes Working/Idle, target is 0 and focus does not move
- `TestSmartJumpBreaksTiesOnLowestPaneID` — run the selection 50 times over three equally-ranked panes and assert the same ID every time, which is what proves map order is no longer load-bearing

**Verification — automated:**
- [x] `go test ./internal/server/ -run TestSmartJump -v` **fails** before the change — **proved properly rather than by build error: the old first-match body was extracted verbatim as a temporary `smartJumpTargetLocked` so the tests ran against it. `PrefersDoneOverNeedsInput` returned pane 1 (want 3) and `BreaksTiesOnLowestPaneID` returned pane 9 (want 2, map order).**
- [x] `go test ./internal/server/ -run TestSmartJump -v` passes after — **4/4 PASS**
- [x] `make test` passes — **all 11 packages ok**
- [x] `make check` passes — **23 smoke / golden ok / 7 attach-check**

Note: `TestSmartJumpPrefersFailedOverDone` and `TestSmartJumpIgnoresWorkingAndIdle`
pass against the old semantics too. They are guards, not discriminators — the
old code never treated `Done` as a target, so it reached the failed pane by
elimination. The two that fail are the ones carrying the new behaviour.

**Verification — manual:**
- [x] `make run`, `C-b n` for a third pane, `printf '\033]133;D;1\007'` in the last one, focus pane 1, then `C-b a` — focus lands on the failed pane. — **verified by pty probe asserting through `focus_pane_id`: landed on pane 3, the failed one.**

---

## Phase 5: An end-to-end smoke case for OSC 133

`scripts/smoke.py:339-346` records that the previous OSC 133 case passed for
the feature's entire broken life by echoing a command into a pane and grepping
for its own text, and was deleted rather than patched. The replacement asserts
through `focus_pane_id`, which is what that comment demands.

**Files:**
- Modify: `scripts/smoke.py` — add `case_osc133_status_drives_smart_jump`, register it in the case list, replace the "deliberately no case" comment.

**Key changes:**

Two panes exist at launch, focus on pane 1. Smart jump is `a`
(`internal/keys/keys.go:95`), so the prefix sequence is `\x02a`.

```python
def case_osc133_status_drives_smart_jump(fail):
    """OSC 133 D;1 in pane 2 makes C-b a jump there from pane 1.

    Asserted through focus_pane_id, not by grepping for text this case
    typed itself -- see the comment above case_quit_restores_and_reaps
    for why the previous version of this case was worthless.
    """
    s = Session()
    s.type("\x02l")                                 # focus pane 2
    s.type("printf '\\033]133;D;1\\007'\r")         # mark pane 2 failed
    s.type("\x02h")                                 # back to pane 1
    if focus_pane_id(s.output(), s.rows) != 1:
        fail("setup did not return focus to pane 1")
    before = len(s.output())
    s.type("\x02a")                                 # smart jump
    landed = focus_pane_id(s.output()[before:], s.rows)
    s.quit_and_reap()
    if landed != 2:
        fail(f"smart jump landed on pane {landed}, want the failed pane 2")
```

Register between the existing prefix cases and
`case_quit_restores_and_reaps`, and replace the deleted-case comment block
with one recording that the case is back and what it now asserts through.

**Risk to watch:** the login shell in the pane must not itself emit OSC 133,
or pane 1's status would also become a jump target. Default `bash`/`zsh`
without shell integration do not. If the developer's shell does, the case will
show it by landing on pane 1 — which is a true result, not a flake, and means
the case needs a fixed `SHELL` in its env.

**Verification — automated:**
- [x] `python3 scripts/smoke.py` passes, including the new case — **24 passed, 0 failed** (was 23)
- [x] ~~`git stash`~~ the Phase 3 handler change, confirm the new case **fails**, restore — **done without `git stash`, which is unsafe here: the stash stack is shared across worktrees. Used `git checkout a99d548 -- internal/server/term/grid.go` to restore only the broken handler, leaving Phase 3's broadcast in place so the handler was isolated. Result: `FAIL osc133 status drives smart jump / smart jump landed on pane None, want the failed pane 2`. Restored with `git checkout HEAD --`, back to OK.**
- [x] `make check` passes — **see below**

**Verification — manual:**
- [x] Read the new case's output line in the smoke run and confirm it names the pane it landed on. — **on failure it prints the landed pane (`None`), on success it is silent. The named risk did not materialise: the case passes, so the dev login shell does not emit OSC 133 itself and pane 1 never becomes a competing target.**

---

## Phase 6: Close the transport a detach drops

`removeTransportLocked` (`server.go:100-108`) drops the transport from the
slice and never closes it. `Close` is not on the `Transport` interface
(`internal/transport/inproc.go:15-19`) and is called only from tests, so each
detach leaks an fd and a reader goroutine on a long-lived server.

**Files:**
- Modify: `internal/server/server.go` — close at the `handleClientConnLoop` call site.
- Modify: `internal/server/server_test.go` (or a new `transport_close_test.go`).

**Key changes:**

The call site at `server.go:88-93` already unlocks immediately, so the close
goes after the unlock — never under `s.mu`:

```go
case msg, ok := <-tp.ClientSendChan():
	if !ok {
		s.mu.Lock()
		s.removeTransportLocked(tp)
		s.mu.Unlock()
		// Close outside s.mu. Close can block, and BEYOND-V1 section 6
		// records holding s.mu across a blocking call as the shape
		// behind the server that cannot be shut down. Close is not on
		// the Transport interface -- only the socket implementations
		// have it -- so this is a type assertion rather than a call.
		if cl, ok := tp.(io.Closer); ok {
			_ = cl.Close()
		}
		return
	}
```

Add `io` to the imports.

Test goes in `package server` (the internal test package — `pane_wedge_test.go`
already uses it), so `handleClientConnLoop` and `s.transports` are directly
reachable:

```go
// closableTransport is a Transport that also implements io.Closer, which
// the socket implementations do and InProcChannel does not.
type closableTransport struct {
	*transport.InProcChannel
	closed atomic.Bool
}

func (t *closableTransport) Close() error { t.closed.Store(true); return nil }

func TestDroppedTransportIsClosed(t *testing.T) {
	// srv with tp registered in s.transports, then close tp's client
	// send channel so handleClientConnLoop takes the !ok branch.
	// Assert tp.closed is true and s.transports no longer contains it.
}
```

The body drives `handleClientConnLoop` directly on a server built by the
package's existing test constructor.

**Verification — automated:**
- [x] `go test ./internal/server/ -run TestDroppedTransportIsClosed -v` **fails** before the change — **`dropped transport was not closed; it leaks an fd and a reader goroutine per detach`**
- [x] `go test ./internal/server/ -run TestDroppedTransportIsClosed -v` passes after — **2/2 PASS, including the non-`io.Closer` case that proves the type assertion does not break the in-process transport**
- [x] `make test` passes — **all 11 packages ok**
- [x] `make race` passes — **all 11 packages ok under `-race -count=1`**
- [x] `python3 scripts/attachcheck.py` passes — **7 passed, 0 failed, including `detach leaves the session running`**
- [x] `make check` passes — **24 smoke / golden ok / 7 attach-check**

**Verification — manual:**
- [x] `wideboi server` in one terminal, `wideboi attach` in another, `C-b d` to detach, reattach. Session survives; no error on the server. — **covered objectively by `attachcheck.py`, which drives exactly this against the real pair of processes over a real socket: `detach leaves the session running` and `server reaps its panes on signal` both OK.**

Added a second test the plan did not call for: `TestDroppedNonClosableTransportIsStillRemoved`.
`InProcChannel` is not an `io.Closer`, and it is what the in-process binary
uses, so the type assertion had to be shown not to skip removal for it.

---

## Phase 7: Make the docs true

Docs only, no code. The `BEYOND-V1.md` reconciliation for sections 1, 2 and 7
is already on this branch, uncommitted from before the session started.

**Files:**
- Modify: `docs/BEYOND-V1.md` — section 6 table and section 1.
- Modify: `docs/LESSONS.md` — one new lesson.

**Key changes:**

1. Remove the "focus-change wipe blanks the screen" row and the OSC 133 row
   from the section 6 table; both are fixed. Note the fixes in section 1 and
   in the "v1 is:" blurb, which currently carries a parenthetical saying OSC
   133 never worked.
2. Add section 6 rows for the analogues this session found and deliberately
   did not fix: `CardStrategy` reachable only from tests; `Pane.Dead`,
   `Pane.SendText`, `SocketListener.Path` and `Server.SpawnPane` with no
   callers anywhere; `Client.FocusPaneID` test-only.
3. Keep the wide-glyph-at-the-split hazard in section 1, restated: it is now
   reachable for the first time, because the frames are no longer blank.
4. `LESSONS.md` — a new entry. The two defects in this plan shared one shape,
   and the repo's existing lesson ("A smoke test that types a command and then
   greps for its own text proves nothing") covers only the OSC half:

   > ## A unit test that supplies its own inputs proves the mechanism, not the wiring
   >
   > `WipeTransition` was tested against frames the test wrote, and passed
   > while its only caller passed two blank surfaces. The OSC 133 switch was
   > tested against nothing, and its caller handed it a payload shape it never
   > matched. Both shipped green. When a component takes data from a caller,
   > something has to test the caller — and if the caller is untestable
   > (`Draw` took a `*uv.TerminalScreen` nothing could construct), that is the
   > bug to fix first.

**Verification — automated:**
- [x] `make check` passes (docs do not affect it, but this is the last gate before the PR) — **24 smoke / golden ok / 7 attach-check, plus lint, seam-check, test, race, verify-exit**
- [x] `grep -n "never worked" docs/BEYOND-V1.md` shows no remaining claim — **clean**

**Verification — manual:**
- [x] Read section 6 end to end; every remaining row is still true. — **10 defect rows. Two removed (both fixed), three analogue rows added, and one found stale — see below.**
- [x] Read section 1; the wipe is described as working, with the wide-glyph hazard still open. — **done; the hazard is restated as "still unverified, and reachable for the first time", since blank frames could never have exposed it.**

**Unplanned: a stale row corrected.** Reading section 6 end to end, as this
phase asked, turned up a row that predates this session and is no longer true:
"`transport.SendServer` is called under `s.mu`". Both call sites
(`server.go:512`, `server.go:528`) copy the transport slice and unlock before
sending, and the `broadcastLayoutLocked` the row names does not exist as a
function — only in two comments. Corrected in place rather than left standing,
with an explicit note that the *rest* of the wedge chain was not re-derived:
the wedged-render defect is unchanged and a socket `SendServer` can still
block, just not under the lock. Flagged for a focused pass, not fixed here.

---

## Spec coverage check

| Spec requirement | Phase |
| --- | --- |
| Wipe renders real before/after frames, no blank interval | 2 |
| OSC 133 drives the status glyph | 3 |
| Smart jump lands on the pane that most needs attention | 4 |
| `Client.Draw` reachable from a Go test | 1 |
| A test that fails if either wipe frame goes blank again | 2 |
| Detached client's connection is closed | 6 |
| `BEYOND-V1.md` reflects reality; analogues recorded | 7 |
| `sawOSC133` latches only on a recognised command | 3 |
| End-to-end assertion through `focus_pane_id` | 5 |
