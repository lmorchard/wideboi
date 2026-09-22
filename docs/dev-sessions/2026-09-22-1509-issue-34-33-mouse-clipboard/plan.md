# Mouse support and OSC 52 clipboard Implementation Plan

**Goal:** Click-to-focus, wheel scroll, drag-to-copy over OSC 52, and mouse
forwarding to children that request it (#34, #33).

**Approach:** The client owns hit-testing and selection (placements are
client-side; the composed screen is the only "what you see"). The server learns
only focus changes (`MsgFocusPane`), scrolls (`MsgScroll`) and forwarded events
(`MsgMouse`). OSC 52 is written out-of-band to the `TerminalScreen` by `main`,
never through a cell.

**Tech stack:** Go, charmbracelet/ultraviolet (mouse events, `SetMouseMode`),
x/vt (`EnableMode` callbacks, `SendMouse`), x/ansi (`SetSystemClipboard`),
Python pty harness (`scripts/smoke.py`).

Shared context for every phase:

- Screen rows: row 0 = headers, panes at `Dst` (Min.Y = 1), last row = status bar.
- Paint order is ascending Z, stable (`composeFrameLocked`). Hit-test walks that
  same sorted order **in reverse**.
- On-screen placements: `c.currentPlacementsLocked()`.
- Client methods that send hold `c.mu` only to read state, then send after
  unlocking (see `SendKey`).
- `make quick` = edit loop; `make check` = gate. Go tests ad hoc need `-count=1`.
- Break every new test's guarded behaviour once and watch it go red before
  trusting it (LESSONS).

---

## Phase 1: Mouse capture and click-to-focus

End to end: host terminal reports mouse events, a left press on any pane focuses
it via a new `MsgFocusPane`, and `mouse = false` in config disables capture.

**Files:**
- Modify: `internal/protocol/messages.go` — add `MsgFocusPane`.
- Modify: `internal/protocol/wire_test.go` — add `MsgFocusPane{}` to `wireTypes`.
- Modify: `internal/transport/socket.go` — `gob.Register(protocol.MsgFocusPane{})`.
- Modify: `internal/transport/wire_test.go` — add `protocol.MsgFocusPane{PaneID: 3}` to `TestEveryMessageTypeRoundtrips`.
- Modify: `internal/server/server.go` — `handleClientMsg` case.
- Create: `internal/client/mouse.go` — `HandleMouse`, hit-testing.
- Create: `internal/client/mouse_test.go`.
- Modify: `internal/server/server_test.go` (or a new `focus_test.go`) — server focus test.
- Modify: `internal/config/config.go` — `Mouse *bool` + `MouseEnabled bool`.
- Modify: `internal/config/*_test.go` — default/override test.
- Modify: `cmd/wideboi/main.go` — enable mouse mode in `run` and `runAttach`; dispatch `uv.MouseEvent`.
- Modify: `scripts/smoke.py` — `case_click_focuses_pane`.

**Key changes:**

```go
// messages.go
// MsgFocusPane asks the server to focus a specific pane -- a mouse click
// names its target, unlike the relative focus verbs.
type MsgFocusPane struct {
	PaneID int
}
```

```go
// server.go handleClientMsg
case protocol.MsgFocusPane:
	if _, ok := s.panes[m.PaneID]; ok {
		s.strip.FocusPaneID(m.PaneID)
		needBroadcast = true
	}
```

```go
// internal/client/mouse.go
package client

// hitTestLocked returns the topmost placement under pt, or nil.
// Row 0 (the header) belongs to the placement whose Dst spans pt.X.
// c.mu must be held.
func (c *Client) hitTestLocked(pt image.Point) *protocol.PlacementData {
	ps := c.currentPlacementsLocked()
	sorted := make([]protocol.PlacementData, len(ps))
	copy(sorted, ps)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Z < sorted[j].Z })
	for i := len(sorted) - 1; i >= 0; i-- {
		p := sorted[i]
		if pt.X >= p.Dst.Min.X && pt.X < p.Dst.Max.X && pt.Y >= 0 && pt.Y < p.Dst.Max.Y {
			return &p
		}
	}
	return nil
}

// HandleMouse routes one host mouse event. It returns text to place on
// the clipboard, or "" (the copy path arrives in Phase 3).
func (c *Client) HandleMouse(ctx context.Context, ev uv.MouseEvent) string {
	var out []transport.ClientMessage
	c.mu.Lock()
	if c.helpVisible {
		c.mu.Unlock()
		return ""
	}
	m := ev.Mouse()
	pt := image.Pt(m.X, m.Y)
	if _, ok := ev.(uv.MouseClickEvent); ok && m.Button == uv.MouseLeft {
		if p := c.hitTestLocked(pt); p != nil && p.PaneID != c.focusPaneID {
			out = append(out, protocol.MsgFocusPane{PaneID: p.PaneID})
		}
	}
	c.mu.Unlock()
	for _, msg := range out {
		c.transport.SendClient(ctx, msg)
	}
	return ""
}
```
(Confirm `transport.ClientMessage` is the type `SendClient` accepts; use whatever `SendKey` passes.)

```go
// config.go
Mouse        *bool `toml:"mouse"`   // nil = default (on)
MouseEnabled bool  `toml:"-"`
// in Load, file merge: if fileCfg.Mouse != nil { cfg.Mouse = fileCfg.Mouse }
// validation: cfg.MouseEnabled = cfg.Mouse == nil || *cfg.Mouse
```

```go
// main.go, in both run and runAttach, right after EnterAltScreen:
if cfg.MouseEnabled {
	scr.SetMouseMode(uv.MouseModeDrag)
	scr.SetMouseEncoding(uv.MouseEncodingSGR)
}
// event switch, new case in both loops:
case uv.MouseEvent:
	if text := cli.HandleMouse(ctx, ev); text != "" {
		screenLock.Lock()
		_, _ = scr.WriteString(ansi.SetSystemClipboard(text))
		_ = scr.Flush()
		screenLock.Unlock()
	}
```
(`run` must also check `!stopped.Load()` before writing, like its resize case.
Case order: `uv.MouseEvent` is an interface, so place it after the concrete
`uv.KeyPressEvent` case.)

**Tests (write first):**
- `TestClickFocusesPaneUnderPointer` — scroll layout, two panes, focus 1; `HandleMouse(MouseClickEvent{X: inside pane 2, Y: 3, Button: MouseLeft})`; drain `ch.ClientSend`, want `MsgFocusPane{PaneID: 2}`.
- `TestClickOnHeaderRowFocuses` — same, Y = 0.
- `TestClickOnFocusedPaneSendsNothing`.
- `TestClickHitsTopmostCard` — card mode, `threeColumns()`, focus 2; click a cell inside both pane 2's and a sliver's Dst (the overlap; compute from `placementFor`) → `MsgFocusPane` not sent (2 is top and focused). Click a cell only inside pane 1's sliver → `MsgFocusPane{1}`.
- `TestMouseIgnoredWhileHelpVisible`.
- Server: send `MsgFocusPane{PaneID: second}` via `handleClientMsg`, assert `strip.FocusedPaneID()` (or broadcast snapshot `FocusPaneID`) equals it; unknown ID leaves focus unchanged.
- Config: default `MouseEnabled == true`; TOML `mouse = false` → false.
- Smoke `case_click_focuses_pane`: `Session()` (cards, two panes, focus is pane 1 after startup — verify with `focus_pane_id`). Find pane 2's column: after `\x02l`… simpler: write SGR click at column 95 (1-based), row 5: `s.type("\x1b[<0;95;5M\x1b[<0;95;5m")`; assert `focus_pane_id(s.output(), s.rows)` changed from its prior value. Prove red: comment out the `MsgFocusPane` send and see it fail.
  Also assert the enable sequence `\x1b[?1002h` appears in startup output.

**Verification — automated:**
- [x] New Go tests fail before implementation — **client: focus requests = [] (stub HandleMouse); server: focused pane = 1, want 3; config: MouseEnabled forced true → red; smoke: broken send → "clicking column 95 left focus on pane 1"**
- [x] `go test -count=1 ./internal/client ./internal/server ./internal/config ./internal/protocol ./internal/transport` — **all ok**
- [x] `make quick` — **ok**
- [x] `make smoke` including `case_click_focuses_pane`, run 4 times — **29/29 ×5** (an earlier 4× "28 passed" was a stale binary left over from the red check, not a flake)
- [x] `make check` — **29 passed smoke, golden OK, all targets green**

**Verification — manual:**
- [ ] Clicking a sliver or unfocused pane focuses it (animation plays); clicking a header works
- [ ] On quit, the host terminal no longer reports mouse (clicking in the shell prints no garbage)
- [ ] `mouse = false` in config: native terminal selection works as before

---

## Phase 2: Wheel scrolls the pane under the pointer

**Files:**
- Modify: `internal/client/mouse.go` — wheel branch.
- Modify: `internal/client/mouse_test.go`.

**Key changes:**

```go
// wheelStep matches common terminals' three rows per notch. Positive
// scrolls into history, the same sign as the "k" scroll-up binding.
const wheelStep = 3

// in HandleMouse, before the click branch:
if w, ok := ev.(uv.MouseWheelEvent); ok {
	p := c.hitTestLocked(pt)
	if p != nil && p.Kind == protocol.PlacementFull && pt.In(p.Dst) {
		switch w.Button {
		case uv.MouseWheelUp:
			out = append(out, protocol.MsgScroll{PaneID: p.PaneID, Delta: wheelStep})
		case uv.MouseWheelDown:
			out = append(out, protocol.MsgScroll{PaneID: p.PaneID, Delta: -wheelStep})
		}
	}
}
```
(Phase 4 adds the forward-to-child branch ahead of this.)

**Tests (write first):**
- `TestWheelScrollsPaneUnderPointer` — scroll layout, focus 1, wheel-up over pane 2 content → `MsgScroll{2, +3}`, and no `MsgFocusPane`.
- `TestWheelDownScrollsForward` → `Delta: -3`.
- `TestWheelOverSliverOrHeaderDoesNothing`.

**Verification — automated:**
- [x] Tests red first, then green — **scroll tests: `scrolls = []`; sliver/header guard passes vacuously pre-impl, so each half removed in turn → red ("wheel on sliver sent [{2 3}]", "wheel on header sent [{2 3}]")**
- [x] `make quick` — **ok**

**Verification — manual:**
- [ ] Wheel over an unfocused pane scrolls its history without moving focus

---

## Phase 3: Drag-select, highlight, copy over OSC 52

**Files:**
- Modify: `internal/client/mouse.go` — selection state machine, text extraction.
- Modify: `internal/client/client.go` — `selection` field; draw highlight in `drawToScreenLocked`; clear on placement change in `HandleServerMsg`.
- Modify: `internal/client/mouse_test.go`.
- Modify: `cmd/wideboi/main.go` — `cli.ClearSelection()` on every `uv.KeyPressEvent` (both loops).
- Modify: `scripts/smoke.py` — `case_drag_copies_over_osc52`.

**Key changes:**

```go
// client.go, Client fields
sel *selection

// mouse.go
// selection is a drag in progress or a finished one still highlighted.
// Coordinates are screen cells, clamped to bounds -- the pane's content
// rect when the drag began -- so a selection never crosses into a
// neighbour.
type selection struct {
	paneID         int
	bounds         image.Rectangle
	anchor, cursor image.Point
	dragging       bool
}

func clampPt(pt image.Point, r image.Rectangle) image.Point {
	return image.Pt(min(max(pt.X, r.Min.X), r.Max.X-1), min(max(pt.Y, r.Min.Y), r.Max.Y-1))
}

// ordered returns anchor and cursor in reading order.
func (s *selection) ordered() (start, end image.Point) {
	a, b := s.anchor, s.cursor
	if b.Y < a.Y || (b.Y == a.Y && b.X < a.X) {
		a, b = b, a
	}
	return a, b
}

// rowSpan is the [x0, x1] cell range selected on row y (inclusive),
// or ok=false if y is outside the selection. Stream selection: the
// first row starts at start.X, the last ends at end.X, rows between
// span the full bounds.
func (s *selection) rowSpan(y int) (x0, x1 int, ok bool) {
	start, end := s.ordered()
	if y < start.Y || y > end.Y {
		return 0, 0, false
	}
	x0, x1 = s.bounds.Min.X, s.bounds.Max.X-1
	if y == start.Y {
		x0 = start.X
	}
	if y == end.Y {
		x1 = end.X
	}
	return x0, x1, true
}

// selectionText reads the selected cells from scr: one line per row,
// trailing spaces trimmed, joined with "\n". Advances by each cell's
// Width so a wide glyph contributes its content once and its
// continuation cell nothing (compose.Text is not faithful here).
func (s *selection) text(scr uv.Screen) string {
	start, end := s.ordered()
	var lines []string
	for y := start.Y; y <= end.Y; y++ {
		x0, x1, _ := s.rowSpan(y)
		var b strings.Builder
		for x := x0; x <= x1; {
			c := scr.CellAt(x, y)
			if c == nil || c.Content == "" {
				b.WriteByte(' ')
				x++
				continue
			}
			b.WriteString(c.Content)
			x += max(c.Width, 1)
		}
		lines = append(lines, strings.TrimRight(b.String(), " "))
	}
	return strings.Join(lines, "\n")
}
```

HandleMouse additions (full event handling after Phase 3, before Phase 4):

```go
switch e := ev.(type) {
case uv.MouseClickEvent:
	c.sel = nil
	if e.Button != uv.MouseLeft { break }
	p := c.hitTestLocked(pt)
	if p == nil { break }
	if p.PaneID != c.focusPaneID {
		out = append(out, protocol.MsgFocusPane{PaneID: p.PaneID})
	}
	if p.Kind == protocol.PlacementFull && pt.In(p.Dst) {
		c.sel = &selection{paneID: p.PaneID, bounds: p.Dst, anchor: pt, cursor: pt, dragging: true}
	}
case uv.MouseMotionEvent:
	if c.sel != nil && c.sel.dragging {
		c.sel.cursor = clampPt(pt, c.sel.bounds)
	}
case uv.MouseReleaseEvent:
	if c.sel != nil && c.sel.dragging {
		c.sel.cursor = clampPt(pt, c.sel.bounds)
		c.sel.dragging = false
		if c.sel.anchor == c.sel.cursor {
			c.sel = nil
		} else if c.lastRenderedScreen != nil {
			copyText = c.sel.text(c.lastRenderedScreen)
		}
	}
case uv.MouseWheelEvent:
	// Phase 2 branch
}
```
(`copyText` is returned after unlocking and sending.)

Highlight, in `drawToScreenLocked` right after `composeFrameLocked` and before `drawStatusBarLocked`:

```go
// drawSelectionLocked inverts selected cells over the composed frame.
// A copy of each cell is modified, never the pointer CellAt returns
// (LESSONS: "CellAt returns a live pointer"). c.mu must be held.
func (c *Client) drawSelectionLocked(scr uv.Screen) {
	if c.sel == nil { return }
	start, end := c.sel.ordered()
	for y := start.Y; y <= end.Y; y++ {
		x0, x1, _ := c.sel.rowSpan(y)
		for x := x0; x <= x1; x++ {
			cp := scr.CellAt(x, y)
			if cp == nil { continue }
			cc := *cp
			cc.Style.Attrs ^= uv.AttrReverse
			scr.SetCell(x, y, &cc)
		}
	}
}
```
(Verify `uv.Style.Attrs` field name/type and whether a wide glyph's continuation cell must be skipped to avoid the partial-overwrite blanking noted in LESSONS; if so, advance by `cc.Width`.)

Clearing:
```go
// ClearSelection drops any highlighted selection. main calls it on
// every key press: typing means the user has moved on.
func (c *Client) ClearSelection() { c.mu.Lock(); c.sel = nil; c.mu.Unlock() }
```
In `HandleServerMsg`'s `MsgLayoutSnapshot` case, after `c.placements` is set:
```go
if c.sel != nil {
	var still bool
	for _, p := range c.placements {
		if p.PaneID == c.sel.paneID && p.Dst == c.sel.bounds { still = true }
	}
	if !still { c.sel = nil }
}
```

**Tests (write first):**
- `TestDragSelectsAndReturnsText` — scroll layout, pane update with known multi-row content ("HELLO WORLD" row 0, "SECOND" row 1) for pane 1; `Draw`; press at (Dst.Min.X+6, Dst.Min.Y), motion, release at (Dst.Min.X+2, Dst.Min.Y+1) → returns `"WORLD\nSEC"`.
- `TestReleaseWithoutDragCopiesNothing` → returns "" and `sel == nil`.
- `TestDragIsClampedToStartingPane` — release far right in pane 2 → text contains no pane-2 content.
- `TestSelectionIsHighlighted` — after drag, `Draw` into `fakeHostScreen`; selected cells have `AttrReverse`, a cell just outside does not.
- `TestSelectionTextHandlesWideGlyphs` — content "世X" → selection over both gives `"世X"`.
- `TestClearSelectionOnKey` and `TestSelectionClearsWhenPaneMoves` (new snapshot with focus change in card mode).

Smoke `case_drag_copies_over_osc52`:
- `s = Session(args=["--layout", "scroll"])` (or the env var) so pane 1 sits at column 0.
- `s.type("printf 'AB%sCD\\n' XY\r")` — output `ABXYCD` is distinguishable from the typed command.
- Drag from (1-based) col 1, row 2 to col 40, row = last cursor row: `s.type("\x1b[<0;1;2M\x1b[<32;20;5M\x1b[<0;40;{r}m")`, where `r` comes from `s.cursor_positions()[-1][0]`.
- Assert a `\x1b]52;c;([A-Za-z0-9+/=]+)` match; base64-decode; `b"ABXYCD" in decoded`.
- Prove red: return "" from the release branch and watch it fail.

**Verification — automated:**
- [x] Tests red first (record reasons), then green — **copied "" / not highlighted / unchanged snapshot cleared selection. Guards proved individually: wide-glyph skip removed → "highlighting blanked the wide glyph"; highlight call removed → "cell 6 … not highlighted"; clamp removed → first version of the clamp test stayed GREEN (too weak: drag ended at "MORE N", test looked for "NEIGHBOUR"), tightened to "MORE" → red. Smoke: release copy removed → "no OSC 52 clipboard write after a drag". Also fixed an escaping slip in the smoke regex's ST branch (matched ESC + two backslashes; passed via BEL only).**
- [x] `go test -count=1 ./internal/client` — **ok**
- [x] `make quick` — **ok**
- [x] `make smoke` ×4 — **30/30 ×4**
- [!] `make check` — **1 failure in 5 runs: `unmodified verb exits control mode` (untouched case, no mouse input; typed `C-b l echo …` output missing under parallel load). Other 4 runs fully green. Baseline on origin/main being measured; see notes.md.**

**Verification — manual:**
- [ ] Drag in a shell pane highlights; release puts the text on the Mac clipboard (paste elsewhere)
- [ ] Same via `wideboi server` + `wideboi attach`, and over `ssh` into a host running wideboi
- [ ] Highlight clears on keypress and on next click

---

## Phase 4: Forward mouse events to children that request them

**Files:**
- Modify: `internal/server/term/grid.go` — `Grid` gains `SendMouse(uv.MouseEvent)` and `MouseTracking() bool`; vtGrid tracks DEC 9/1000/1002/1003 via `EnableMode`/`DisableMode`.
- Modify: `internal/server/status_test.go`, `internal/server/pane_wedge_test.go` — fakes gain the two no-op methods.
- Modify: `internal/server/term/*_test.go` — grid test.
- Modify: `internal/server/pane.go` — `mice` queue drained by the existing key-writer goroutine; `Pane.SendMouse`; `UpdateMessage` sets `MouseTracking`.
- Modify: `internal/protocol/messages.go` — `MsgPaneUpdate.MouseTracking bool`; `MsgMouse`.
- Modify: `internal/protocol/wire.go` — `MouseData` encode/decode.
- Modify: `internal/protocol/wire_test.go` (add `MsgMouse{}`), `internal/transport/socket.go` (register), `internal/transport/wire_test.go` (roundtrip).
- Modify: `internal/server/server.go` — `MsgMouse` case.
- Modify: `internal/client/client.go` — `mouseTracking map[int]bool` from `MsgPaneUpdate`, pruned with mirrors; `grab` field.
- Modify: `internal/client/mouse.go` — forwarding branches.
- Modify: `scripts/smoke.py` — `case_click_reaches_mouse_tracking_child`.

**Key changes:**

```go
// grid.go, in NewVTWithIdleTimeout's vt.Callbacks:
EnableMode:  func(m ansi.Mode) { g.trackMouseMode(m, true) },
DisableMode: func(m ansi.Mode) { g.trackMouseMode(m, false) },

// mouseModes is a bitmask of the child's DEC tracking modes that are set.
// More than one can be set at once, and clearing one leaves the others.
mouseModes atomic.Uint32

var mouseModeBits = map[ansi.DECMode]uint32{
	ansi.ModeMouseX10: 1, ansi.ModeMouseNormal: 2,
	ansi.ModeMouseButtonEvent: 4, ansi.ModeMouseAnyEvent: 8,
}

func (g *vtGrid) trackMouseMode(m ansi.Mode, on bool) {
	dm, ok := m.(ansi.DECMode)
	if !ok { return }
	bit, ok := mouseModeBits[dm]
	if !ok { return }
	for {
		old := g.mouseModes.Load()
		nw := old &^ bit
		if on { nw = old | bit }
		if g.mouseModes.CompareAndSwap(old, nw) { return }
	}
}

func (g *vtGrid) MouseTracking() bool          { return g.mouseModes.Load() != 0 }
func (g *vtGrid) SendMouse(m uv.MouseEvent)    { g.em.SendMouse(m) }
```

```go
// pane.go -- SendMouse writes to vt's io.Pipe and blocks until read,
// exactly like SendKey (LESSONS), so it rides the same writer goroutine.
mice chan uv.MouseEvent   // make(chan uv.MouseEvent, keyQueueDepth)
// key-writer goroutine select gains:
case m := <-p.mice:
	p.grid.SendMouse(m)

func (p *Pane) SendMouse(m uv.MouseEvent) {
	select {
	case p.mice <- m:
	default:
		p.dropped.Add(1)
	}
}
// UpdateMessage: MouseTracking: p.grid.MouseTracking(),
```

```go
// messages.go
type MouseKind int
const (
	MousePress MouseKind = iota
	MouseRelease
	MouseMotion
	MouseWheel
)

// MsgMouse forwards a mouse event to a pane whose child enabled mouse
// tracking. X and Y are pane-local cells. Concrete fields, not
// uv.MouseEvent: that is an interface, and interfaces cannot cross the
// wire (TestWireTypesCarryNoInterfaces).
type MsgMouse struct {
	PaneID int
	Kind   MouseKind
	X, Y   int
	Button int
	Mod    int
}

// wire.go
func EncodeMouse(paneID int, ev uv.MouseEvent, local image.Point) MsgMouse
func (m MsgMouse) Decode() uv.MouseEvent // switch Kind -> MouseClickEvent/…; uv.Mouse{X,Y,Button: uv.MouseButton(m.Button), Mod: uv.KeyMod(m.Mod)}
```

```go
// server.go
case protocol.MsgMouse:
	if p, ok := s.panes[m.PaneID]; ok {
		p.SendMouse(m.Decode())
	}
```

Client:
```go
// client.go fields
mouseTracking map[int]bool
grab          *mouseGrab

// mouse.go
// mouseGrab is a forwarded drag: once a press reaches a child, motion
// and release follow it there even if the pointer leaves the pane.
type mouseGrab struct {
	paneID int
	dst    image.Rectangle
	src    image.Point // p.Src.Min
}

func (g *mouseGrab) local(pt image.Point) image.Point {
	pt = clampPt(pt, g.dst)
	return image.Pt(pt.X-g.dst.Min.X+g.src.X, pt.Y-g.dst.Min.Y+g.src.Y)
}
```
In `HandleServerMsg` `MsgPaneUpdate`: `c.mouseTracking[m.PaneID] = m.MouseTracking` (init map lazily); delete in the prune loop.

HandleMouse, final routing order:
1. `grab != nil`: motion/release → `EncodeMouse(grab.paneID, ev, grab.local(pt))`; release also clears grab. Return.
2. Wheel: hit pane content; if `mouseTracking[p.PaneID]` → forward with local coords, else the Phase 2 `MsgScroll`.
3. Press: `c.sel = nil`; hit; if unfocused → `MsgFocusPane` only (not forwarded, even for tracking panes), then selection only if not tracking. If focused and tracking and in content → forward press, set grab. Otherwise selection as Phase 3 (left button only).
4. Motion/release with selection → Phase 3.

**Tests (write first):**
- Grid: write `"\x1b[?1002h"` → `MouseTracking()` true; `"\x1b[?1000h"` then `"\x1b[?1002l"` → still true; `"\x1b[?1000l"` → false. `SendMouse(MouseClickEvent{X:3,Y:2})` after `?1002h?1006h` → `Read` yields `"\x1b[<0;4;3M"` (read concurrently, per SendKey's pipe caveat).
- Protocol: `MsgMouse` roundtrip via `EncodeMouse`/`Decode` for all four kinds.
- Client: `TestPressInTrackingFocusedPaneIsForwarded` (local coords account for `Src.Min` — use a clipped pane or assert the formula with `Dst.Min.X != 0`), `TestPressOnUnfocusedTrackingPaneOnlyFocuses`, `TestForwardedDragFollowsGrabOutsidePane`, `TestNoSelectionInTrackingPane`, `TestWheelOverTrackingPaneIsForwarded`.
- Server: `MsgMouse` for a pane reaches `SendMouse` (a fake grid recording calls, or a real pane running `cat -v` if a fake is awkward).
- Smoke `case_click_reaches_mouse_tracking_child`: in pane 1, type
  `stty raw -echo; printf '\033[?1000h\033[?1006h'; dd bs=1 count=6 2>/dev/null | od -An -tx1; printf '\033[?1000l'; stty sane\r`,
  wait for settle, then click inside pane 1: `s.type("\x1b[<0;5;5M\x1b[<0;5;5m")`. Assert `b"1b 5b 3c"` in output after the click (od's hex — never present in the typed command). Prove red by skipping the forward branch.

**Verification — automated:**
- [x] Tests red first, then green — **grid: stubbed MouseTracking/SendMouse → wrong tracking + forwarded "" ; protocol: release decoded as press → "decoded as uv.MouseClickEvent"; server: "nothing queued for pane 2"; client: forward/grab/wheel tests red on "forwarded []"; the two vacuous-at-first tests proved by breaking their guards (focus check removed → forwarded to unfocused pane; tracking never cleared → "copied \"\""). Smoke: first version asserted `1b 5b 3c` literally but the screen shows `1b  5b  3c`; fixed to a whitespace regex, then forwarding disabled → red, restored → green.**
- [x] `go test -count=1 ./internal/server/... ./internal/protocol ./internal/transport ./internal/client` — **all ok**
- [x] `make quick`; `make smoke` ×4; `make check` ×4 — **smoke 31/31 ×4; make check fully green ×4 (race included)**

**Verification — manual:**
- [ ] `vim` with `:set mouse=a`: click positions the cursor, wheel scrolls, drag selects in vim's visual mode
- [ ] `htop` click selects a process
- [ ] Clicking an unfocused vim pane focuses it without moving vim's cursor
- [ ] Attached mode (`wideboi server` + `attach`) behaves the same

---

## Phase 5: Docs

Doc-only; TDD opt-out.

**Files:**
- Modify: `README.md` — a Mouse section: click to focus, wheel, drag-to-copy (OSC 52, works over SSH), forwarding to mouse-aware programs, Shift/Option to bypass, `mouse = false`, tmux needs `set -g set-clipboard on`, some terminals need OSC 52 clipboard access enabled (iTerm2: "Applications in terminal may access clipboard").
- Modify: `docs/LESSONS.md` — only if execution teaches something non-obvious.

**Verification — automated:**
- [x] `make check` — **8/8 attach, 31/31 smoke, all targets green**

**Verification — manual:**
- [ ] README section reads correctly
