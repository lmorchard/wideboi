# Plan 17 — Card layout + sliver chrome Implementation Plan

**Goal:** Make `CardStrategy` selectable by env var and verb, and render an
occluded card as chrome — status glyph, live terminal title, activity spine —
instead of a four-column peek at pane content.

**Approach:** Mark sliver placements on the wire so the client can tell them
from a viewport-clipped pane; carry the layout mode as shared session state
the way focus already is; plumb the terminal title end-to-end the way
`PaneStatuses` already goes; then render chrome with width-aware writes.

**Tech stack:** Go, `charmbracelet/ultraviolet`, `charmbracelet/x/vt`, Python
for the pty-level smoke suite.

**Commit per phase:** `Phase N: <name>`.

---

## Phase 1: Mark sliver placements on the wire

A card sliver and a viewport-clipped pane are identical today — both a narrow
`Dst` with a cropped `Src`, both `Z=0`. The client must be able to tell them
apart before it can render chrome over one and not the other.

**Test first.**

**Files:**
- Modify: `internal/protocol/messages.go` — `PlacementKind`, field on `PlacementData`.
- Modify: `internal/layout/layout.go` — field on `Placement`, copy in `ToProtocol`, explicit `PlacementFull` in `ScrollStrategy`.
- Modify: `internal/layout/card.go` — `PlacementSliver` on slivers, `PlacementFull` on the focused card.
- Test: `internal/layout/card_test.go`, `internal/layout/layout_test.go`.

**Key changes:**

`layout` already imports `protocol` (`ToProtocol`, `SyncColumns`), so the type
belongs in `protocol` as the wire owner and is referenced from `layout`.

```go
// protocol

// PlacementKind says what a placement represents, which the renderer
// cannot infer from its geometry: a card sliver and a pane clipped by
// the viewport edge are both a narrow Dst over a cropped Src, and Z
// does not separate them either -- ScrollStrategy emits Z=0 for
// everything.
type PlacementKind int

const (
	// PlacementFull is a pane rendering its own content, whether or
	// not the viewport clips it.
	PlacementFull PlacementKind = iota
	// PlacementSliver is an occluded card, drawn as chrome.
	PlacementSliver
)

type PlacementData struct {
	PaneID int
	Src    image.Rectangle
	Dst    image.Rectangle
	Z      int
	Kind   PlacementKind
}
```

`layout.Placement` gains `Kind protocol.PlacementKind`, and `ToProtocol`
(`layout.go:235-246`) copies it. `ScrollStrategy` sets `Kind: protocol.PlacementFull`
explicitly on every placement it builds — the zero value already is that, but
writing it makes the invariant visible at the site that must never change it.
`CardStrategy` sets `PlacementSliver` in its two sliver loops
(`card.go:69-87`, `card.go:99-119`) and `PlacementFull` on the focused card
(`card.go:88-97`) and on the single-column early return (`card.go:28-38`).

**Tests:**

- `TestScrollStrategyNeverEmitsSlivers` — the existing property generator,
  asserting every placement is `PlacementFull` including at viewport widths
  narrow enough to clip. This is the one guarding
  `case_partly_clipped_pane_keeps_full_width`.
- `TestCardStrategyMarksSlivers` — in a multi-column fan, the focused pane's
  placement is `PlacementFull` and every other is `PlacementSliver`.
- `TestCardStrategySingleColumnIsFull` — one column is not a sliver.

**Verification — automated:**
- [x] `go test ./internal/layout/ -run 'TestScrollStrategyNeverEmitsSlivers|TestCardStrategyMarksSlivers|TestCardStrategySingleColumnIsFull' -v` **fails** before the production change — **build failure: `p.Kind undefined`, `undefined: protocol.PlacementFull`**
- [x] same command passes after — **4/4 PASS, including `TestToProtocolCarriesKind` which the plan did not name but the conversion needed**
- [x] `go test ./internal/protocol/ -run TestWireTypesCarryNoInterfaces -v` passes — **ok; `PlacementKind` is a named int**
- [x] `make test` passes — **all 11 packages ok**
- [x] `make check` passes — **24 smoke / golden ok / 7 attach-check**

**Verification — manual:**
- [x] No UI change is expected in this phase; confirm `python3 scripts/golden.py` still matches. — **"wire output matches the golden snapshot"**

---

## Phase 2: Layout mode is shared session state

Placements are computed client-side (Plan 12), but `SetStrategy` lives on
`layout.Strip` which both halves own a copy of. The mode has to reach the
client's strip, and two clients attached to one session must agree — the same
argument that makes focus shared state.

**Test first.**

**Files:**
- Modify: `internal/protocol/messages.go` — `LayoutMode`, field on `MsgLayoutSnapshot`.
- Modify: `internal/server/server.go` — `Server.layout`, apply to `s.strip`, include in `broadcastLayout`.
- Modify: `internal/client/client.go` — apply the mode to `c.strip` before computing placements.
- Modify: `cmd/wideboi/main.go` — read `WIDEBOI_LAYOUT`, pass to `NewServer`.
- Test: `internal/client/cards_test.go` (new), `internal/server/status_test.go`.

**Key changes:**

```go
// protocol

// LayoutMode is which Strategy the session is using. It is shared
// session state, like focus: two clients of different sizes compute
// their own placements, but they agree on the mode.
type LayoutMode int

const (
	LayoutScroll LayoutMode = iota
	LayoutCards
)

type MsgLayoutSnapshot struct {
	Columns      []ColumnData
	Placements   []PlacementData
	FocusPaneID  int
	PaneStatuses map[int]string
	Layout       LayoutMode
}
```

Server holds `layout protocol.LayoutMode`, set by `NewServer` from a new
parameter. A helper applies it to a strip, used by both halves so the mapping
lives in one place:

```go
// layout

// ApplyMode installs the strategy for mode. Both the server's strip and
// each client's strip go through here, so the two cannot disagree about
// what a mode means.
func ApplyMode(s *Strip, mode protocol.LayoutMode) {
	switch mode {
	case protocol.LayoutCards:
		s.SetStrategy(CardStrategy{})
	default:
		s.SetStrategy(ScrollStrategy{})
	}
}
```

`broadcastLayout` adds `Layout: s.layout` to the snapshot. In
`HandleServerMsg`'s `MsgLayoutSnapshot` branch, **before** the
`ComputePlacements` call at `client.go:85`:

```go
layout.ApplyMode(c.strip, m.Layout)
```

`cmd/wideboi/main.go` — both `run()` and the server path read the env var
beside the existing `WIDEBOI_PREFIX` block (`main.go:108`, `main.go:225`):

```go
// parseLayout maps WIDEBOI_LAYOUT to a mode. Unknown values are an
// error rather than a silent fallback: a typo that quietly starts the
// wrong layout is the same class of defect as the pgdn binding that
// shipped dead because an unmatchable key name looks exactly like a key
// nobody pressed.
func parseLayout(name string) (protocol.LayoutMode, error) {
	switch name {
	case "", "scroll":
		return protocol.LayoutScroll, nil
	case "cards":
		return protocol.LayoutCards, nil
	default:
		return 0, fmt.Errorf("WIDEBOI_LAYOUT=%q: want \"scroll\" or \"cards\"", name)
	}
}
```

**Tests:**

- `TestClientAppliesCardLayoutFromSnapshot` — feed a `MsgLayoutSnapshot` with
  `Layout: LayoutCards` and three columns; assert the client's computed
  placements include `PlacementSliver` entries. Fails today because the
  client's strip is always `ScrollStrategy`.
- `TestClientRevertsToScrollLayout` — a later snapshot with `LayoutScroll`
  produces no slivers.
- `TestParseLayoutRejectsUnknown` — in `cmd/wideboi`, `parseLayout("card")`
  (a plausible typo) is an error, not a silent scroll.

**Verification — automated:**
- [x] `go test ./internal/client/ -run TestClientAppliesCardLayoutFromSnapshot -v` **fails** before the change — **build failure: `unknown field Layout`, `undefined: protocol.LayoutCards`**
- [x] `go test ./internal/client/ ./cmd/wideboi/ -run 'TestClient.*Layout|TestParseLayout' -v` passes after — **5/5 PASS**
- [x] `WIDEBOI_LAYOUT=cards ./bin/wideboi` starts without error; `WIDEBOI_LAYOUT=nonsense ./bin/wideboi` exits with the message above — **prints `wideboi: WIDEBOI_LAYOUT="nonsense": want "scroll" or "cards"` and exits 1**
- [x] `make test` passes — **all 11 packages ok**
- [x] `make check` passes — **24 smoke / golden ok / 7 attach-check**

**Verification — manual:**
- [x] `WIDEBOI_LAYOUT=cards make run` with three or four panes: the focused pane is wide, the others are narrow strips of content. — **verified objectively on a pty rather than by eye: with three panes, divider columns are `[13, 50, 51, 62, 75, 76, 87]` under scroll and `[26, 38, 50, 51, 58, 76]` under cards. Cards are reachable for the first time.**

**Adaptation from the plan.** The plan said `NewServer` would take the mode as
a new parameter. It has eight call sites, six of them in tests that care
nothing about layout, so this is a `Server.SetLayout` method instead — the
zero value is already `LayoutScroll`, so only a caller that wants cards has to
say anything. Same effect, no churn in unrelated tests.

`parseLayout` is also read only by the two halves that own a server
(`runServer`, `run`), not by `runAttach`: an attaching client takes the mode
from the server's snapshot, because layout is shared session state. Putting it
in `runAttach` too would have been a second source of truth — and would have
needed a `conn.Close()` on the error path, which is the tell that it did not
belong there.

---

## Phase 3: `$mod c` toggles card layout

**Test first.**

**Files:**
- Modify: `internal/protocol/messages.go` — `VerbToggleCards`.
- Modify: `internal/keys/keys.go` — one row in `Bindings`.
- Modify: `internal/server/server.go` — handle the verb.
- Test: `internal/keys/keys_test.go`, `internal/server/status_test.go`, `scripts/smoke.py`.

**Key changes:**

`VerbToggleCards` appended to the `VerbType` block (`messages.go:12-17`) —
appended, not inserted, since the value crosses the wire.

```go
// keys.Bindings -- placed LAST among the droppable rows, after
// "d detach" and before the Essential "q"/"esc". See the budget note.
{Key: "c", Action: ActionVerb, Verb: protocol.VerbToggleCards,
	BarGroup: "c cards", Long: "toggle the card layout"},
```

**Bar-budget note — this ordering is load-bearing, not cosmetic.** The status
bar has a hard 79-cell budget at 80 columns and `controlHelp` drops entries
**from the end of the droppable list**. For an attached (detachable) client
the bar currently totals exactly 77:

```
essential tail  "q quit" + 2 + "esc exit"            = 16
+ hjkl move (9)                                      = 27
+ n new (5)                                          = 34
+ w width (7)                                        = 43
+ x kill (6)                                         = 51
+ a attn (6)                                         = 59
+ ? help (6)                                         = 67
+ d detach (8)                                       = 77   <= 79
```

Adding `c cards` (7) anywhere earlier pushes the total to 86, so the last
droppable is dropped — and that is `d detach`, which
`scripts/attachcheck.py:231` asserts is present at its 80x24 size
(`attachcheck.py:46`). Placing the new row last instead means `c cards` is
the entry that drops at 80 columns, which is the documented design: the bar
degrades from the end and the help overlay is the complete reference.
`BEYOND-V1` §8 already records that "the help overlay becomes load-bearing
rather than a convenience" for exactly this reason. So do **not** add
`c cards` to `case_control_mode_names_every_entry_at_80_columns`'s list.

`c` is free (bound today: `h l j k n w x a ? d q esc`) and is not in
`keys.Reserved`, which forbids only `i`, `m` and `[` — letters whose control
byte decodes as a *different named key* (Tab, Enter, Escape). `c`'s repeat
form is `ctrl+c`, which decodes as itself; repeat is meaningless for a toggle
but harmless, and nothing in `cmd/wideboi` or `internal/keys` handles
`ctrl+c` specially (verified by grep).

Server verb handling, beside the other verbs (`server.go:204`):

```go
case protocol.VerbToggleCards:
	if s.layout == protocol.LayoutCards {
		s.layout = protocol.LayoutScroll
	} else {
		s.layout = protocol.LayoutCards
	}
	layout.ApplyMode(s.strip, s.layout)
```

No `resizePanesLocked` call: switching modes must not change any pane's
logical width, which is the whole no-shrink premise.

**Tests:**

- `keys_test.go` already walks the table for structural invariants; add
  `TestToggleCardsIsOnC` mirroring the existing `TestSmartJumpIsOnA`
  (`keys_test.go:225`).
- `TestToggleCardsFlipsLayoutMode` — drive `handleClientMsg` with
  `MsgVerb{VerbToggleCards}` twice, assert `s.layout` goes cards then scroll.
- `TestToggleCardsLeavesColumnWidthsAlone` — record every `ColumnWidth`
  before and after the toggle and assert they are unchanged. This is the
  no-shrink invariant at the layer where a mode switch could break it.
- `scripts/smoke.py` — `case_card_layout_toggles`: press `\x02c` and assert
  the divider columns move, using the existing `divider_columns` helper
  (`smoke.py:63-65`) the way `case_cycle_width` (`smoke.py:185-201`) does.
  Press again and assert they move back.

**Verification — automated:**
- [x] `go test ./internal/keys/ ./internal/server/ -run 'TestToggleCards' -v` **fails** before the change — **build failure: `undefined: protocol.VerbToggleCards`**
- [x] same passes after — **3/3 PASS**
- [x] `python3 scripts/smoke.py --only card` passes — **`OK card layout toggles`, and shown to fail with the verb unhandled: "card layout left the focused pane at column 59; the toggle never reached the placement maths"**
- [!] `python3 scripts/smoke.py --only "control mode names every entry"` still passes — the bar has a hard 79-cell budget and this adds an entry — **PREMISE WRONG, and the plan's whole bar-ordering analysis with it. See below.**
- [x] `make check` passes — **25 smoke / golden ok / 7 attach-check, `d detach` intact**

**Verification — manual:**
- [x] `make run`, `C-b n` twice, then `C-b c` — layout flips; again — flips back. — **verified on a pty by cursor column: 59 (scroll) -> 13 (cards) -> 59 (back).**
- [x] `C-b ?` shows `c` in the help overlay. — **covered by the pre-existing `TestHelpLinesNameEveryBinding`, which asserts the overlay names every row in the table.**

### The bar-ordering analysis was right about the budget and wrong about the fix

The plan worked out that the attached bar sits at exactly 77 of 79 cells and
concluded that placing `c cards` last would make *it* the entry that drops.
The arithmetic held; the conclusion did not, because two Go tests encode
invariants the plan never found:

- `TestControlHelpFitsEveryEntryAt80Columns` (`help_test.go:37`) asserts
  **every** `BarGroup` is present at 80 columns — so nothing is allowed to
  drop there at all.
- `TestTableIsWellFormed` (`keys_test.go:36`) asserted every binding **must
  have** a `BarGroup`.

Together: every binding must be in the bar, and the whole bar must fit in 79
cells, while the bar is already at 77. No new binding could satisfy both,
regardless of label length.

Resolved by relaxing `TestTableIsWellFormed` to make `BarGroup` optional while
keeping `Long` mandatory — which is exactly what the *other* test's comment
already prescribed: "the overlay is already there and the bar should shed the
entry rather than grow." The card toggle is overlay-only. Decided with Les
rather than unilaterally, since it relaxes a guard and changes discoverability.

Also unplanned: **`ctrl+c` had to stay unbound.** Binding `c` gave it a repeat
form, which broke `TestUnknownKeysExitControlModeWithAnyModifier` — that test
deliberately lists `ctrl+c` as an unknown key that leaves control mode, i.e.
the escape hatch users reach for by reflex. Added a `NoRepeat` flag honoured by
`CtrlForm`; repeat is meaningless for a toggle anyway, since pressing it twice
returns you to where you started.

---

## Phase 4: The terminal title, end to end

`x/vt` parses OSC 0/1/2 into a title and offers `Callbacks.Title func(string)`.
`NewVT` registers only `CursorVisibility` (`grid.go:176-178`), so the title is
parsed and dropped. Claude Code emits a live one carrying a spinner and a turn
summary, and no OSC 133 at all — for the agent workload it is the status
signal we actually have.

**Test first.**

**Files:**
- Modify: `internal/server/term/grid.go` — `Title()` on the `Grid` interface, callback in `NewVT`, atomic field.
- Modify: `internal/server/pane.go` — `Pane.Title()`.
- Modify: `internal/server/server.go` — `paneTitlesLocked`, include in the snapshot, extend the change detector.
- Modify: `internal/protocol/messages.go` — `PaneTitles map[int]string`.
- Modify: `internal/client/client.go` — store `paneTitles`.
- Modify: `internal/server/pane_wedge_test.go`, `internal/server/status_test.go` — both `Grid` fakes need the new method.
- Test: `internal/server/term/osc_test.go`, `internal/server/status_test.go`.

**Key changes:**

```go
// vtGrid
title atomic.Pointer[string]

// in NewVT, beside the existing CursorVisibility callback
g.em.SetCallbacks(vt.Callbacks{
	CursorVisibility: func(visible bool) { g.cursorVisible.Store(visible) },
	Title:            func(s string) { g.title.Store(&s) },
})

// Title reports the pane's current terminal title, or "" if the child
// has never set one. OSC 0/1/2; x/vt parses it either way, this is just
// the callback nobody had registered.
func (g *vtGrid) Title() string {
	if t := g.title.Load(); t != nil {
		return *t
	}
	return ""
}
```

`Grid` interface gains `Title() string`. `Pane.Title()` mirrors
`Pane.Status()` (`pane.go:211`). Server gains `paneTitlesLocked()` shaped
exactly like `statusGlyphsLocked`, and `MsgLayoutSnapshot` gains
`PaneTitles map[int]string`.

Plan 16's `broadcastLayoutIfStatusChanged` becomes
`broadcastLayoutIfChanged`, comparing **both** maps against
`lastStatuses`/`lastTitles`, and `broadcastLayout` records both on successful
delivery. The delivery-gating from Plan 16 is preserved: an edge-triggered
broadcast that is dropped must be retried, so the maps are only marked clean
once a send succeeded.

**Tests:**

- `TestGridTracksTerminalTitle` (in `osc_test.go`) — write
  `"\x1b]2;my title\x07"`, assert `Title() == "my title"`; a second write
  replaces it; a grid with no OSC 2 returns `""`.
- `TestTitleChangeTriggersALayoutBroadcast` — mirrors Plan 16's status test:
  change a fake grid's title, assert the change detector fires once and then
  goes quiet.
- Both existing `Grid` fakes gain `Title() string { return "" }`.

**Verification — automated:**
- [x] `go test ./internal/server/term/ -run TestGridTracksTerminalTitle -v` **fails** before the callback is registered — **build failure: `Grid has no field or method Title`**
- [x] `go test ./internal/server/term/ ./internal/server/ -run 'TestGridTracksTerminalTitle|TestTitleChange' -v` passes after — **4/4 PASS across both packages**
- [x] `make test` passes — **all 11 packages ok**
- [x] `make race` passes — **all 11 packages ok under `-race -count=1`; `title` is an `atomic.Pointer[string]` for the same reason `cursorVisible` is**
- [x] `make check` passes — **25 smoke / golden ok / 7 attach-check**

`TestTitleChangeTriggersALayoutBroadcast` was shown to discriminate by
narrowing the change detector back to statuses only: **"a title change did not
trigger a broadcast"**. `TestPaneTitlesReachTheSnapshot` additionally asserts
the title survives onto the wire, not merely that it triggers a send.

**Verification — manual:**
- [x] `make run`, then in a pane `printf '\033]2;hello\007'` — **superseded by `TestPaneTitlesReachTheSnapshot`, which drains the transport and asserts `PaneTitles[1] == "◑ Pong reply"` — a stronger check than reading a debug log, and it uses a real Claude Code title including its spinner glyph.**

**Extra beyond the plan:** `frameState` also carries `paneTitles`. The plan
only listed the `Client` field, but a composed frame has to carry the titles
it was composed from for exactly the reason it carries its statuses — a wipe
animating away from an old frame would otherwise draw current titles into it.
`sameStringMap` was factored out rather than writing the comparison twice.

---

## Phase 5: Width-aware writes for arbitrary text

`compose.WriteString`/`WriteStyled` advance one cell per rune
(`surface.go:30-66`). Titles are arbitrary text and Claude Code's already
carry `◐` and `✳`, which are ambiguous-width. Chrome needs a write that
advances by measured width; the existing helpers keep their semantics for
headers, dividers, the status bar and the help overlay.

**Test first.** Pure addition, no behaviour change to existing callers.

**Files:**
- Modify: `internal/client/compose/surface.go` — `TruncateWidth`, `WriteStyledWidth`.
- Test: `internal/client/compose/surface_test.go`.

**Key changes:**

```go
// TruncateWidth returns the longest prefix of text whose total display
// width fits budget, measured with s's width method.
//
// Distinct from truncateRunes, which counts runes: a double-width
// glyph occupies two cells, so a rune count overflows the budget and
// pushes everything after it one column left. Chrome that renders a
// child-supplied title cannot assume single-width input.
func TruncateWidth(s uv.Screen, text string, budget int) string

// WriteStyledWidth writes text at (x, y) advancing by each cell's
// measured width rather than one column per rune. Shares
// TruncateWidth's reasoning; see there.
func WriteStyledWidth(s uv.Screen, x, y int, text string, style uv.Style)
```

Both iterate grapheme-by-grapheme via `uv.NewCell(s.WidthMethod(), ...)` and
advance `currX += cell.Width`, treating a reported width `<= 0` as 1 so a
zero-width cluster cannot loop forever.

**Tests:**

- `TestTruncateWidthCountsCellsNotRunes` — a CJK string whose rune count fits
  the budget but whose cell width does not.
- `TestWriteStyledWidthKeepsFollowingTextAligned` — write a wide glyph then
  ASCII, and assert the ASCII lands at the column its measured width implies,
  not one short. Must fail against `WriteStyled`.
- `TestWriteStyledWidthHandlesClaudeCodeTitleGlyphs` — the real `◐` and `✳`
  observed on the wire, asserting no column drift.
- `TestTruncateWidthEmptyAndZeroBudget` — both return `""` rather than
  panicking.

**Verification — automated:**
- [x] `go test ./internal/client/compose/ -run 'TestTruncateWidth|TestWriteStyledWidth' -v` **fails** before the helpers exist — **build failure: `undefined: compose.TruncateWidth`**
- [x] same passes after — **5/5 PASS** (run as `TestTruncateWidth|TestWriteStyledAdvances`, see below)
- [x] `go test ./internal/client/compose/ -v` — all pre-existing tests still pass — **ok, `WriteString`/`WriteStyled` behaviour untouched**
- [x] `make check` passes — **25 smoke / golden ok / 7 attach-check**

**Verification — manual:**
- [x] None; this phase renders nothing.

### `WriteStyledWidth` was not needed: the plan's premise was half wrong

The plan said `WriteString`/`WriteStyled` "advance one cell per rune" and
budgeted a width-aware writing helper. **They already advance by
`cell.Width`** (`surface.go:66-71`), verified by probe: writing `"日X"` puts
the wide glyph at column 0, leaves column 1 as its continuation, and lands
`X` at column 2 — correct.

What misled the plan was `WriteString`'s own doc comment, which stated in
detail that width is ignored. It was stale, and **`BEYOND-V1` §6's
"`compose.Text`/`WriteString` ignore `Cell.Width`" row rests on that same
stale comment**. Half of that row is real: `Text` genuinely emits one rune
per cell when reading back. The `WriteString` half is not.

So this phase shipped:

- `TruncateWidth` — genuinely missing, since truncation counted runes.
- Corrected doc comments on `WriteString`/`WriteStyled`, naming the earlier
  claim as wrong so the next reader does not re-derive it.
- `TestWriteStyledAdvancesByMeasuredWidth`, pinning the behaviour the comments
  used to deny — untested until now, which is how the comment drifted.

Also measured: `◐` and `✳`, the glyphs in Claude Code's live title, are
**width 1** under `WcWidth`. So the real titles were never a misalignment
hazard; arbitrary child-supplied text still is, which is what `TruncateWidth`
is for. §6's row gets corrected in Phase 8.

---

## Phase 6: Sliver chrome

The payoff. An occluded card stops showing four columns of pane content and
starts showing what §2 asked for: status glyph, truncated title, activity
colour.

**Test first.**

**Files:**
- Modify: `internal/layout/card.go` — `DefaultSliverWidth` 4 → 10.
- Modify: `internal/client/client.go` — `frameState` carries titles; `composeFrameLocked` branches on `Kind`; new `drawSliverLocked`.
- Test: `internal/client/cards_test.go`.

**Key changes:**

`DefaultSliverWidth = 10` (`card.go:7`). §2 budgeted 4 because it assumed a
sliver showed content; chrome makes the width a free parameter, and 10 fits a
readable horizontal title. Accepted cost: ~10-12 cards at 200 columns rather
than ~25, which is what makes Phase 7 load-bearing.

`frameState` gains `paneTitles map[int]string`, set in `frameStateLocked`.

In `composeFrameLocked`, the existing content fork becomes:

```go
if p.Kind == protocol.PlacementSliver {
	c.drawSliverLocked(dst, p, st)
} else if drawPane != nil {
	drawPane(p.PaneID, dst, p.Dst)
} else if mirror, ok := c.mirrors[p.PaneID]; ok {
	compose.Blit(dst, mirror.Surface, p.Dst)
}
```

```go
// drawSliverLocked renders an occluded card as chrome rather than a
// peek at its content.
//
// Four columns of real terminal output is visual noise -- BEYOND-V1
// section 2 -- so a sliver shows what is actually worth knowing about a
// pane you are not looking at: whether it wants you, what it is doing,
// and whether it is doing anything at all. The title is the good part:
// an agent harness keeps it current (Claude Code writes a spinner and a
// summary of the turn), and it costs nothing to display.
//
// Row 0 is the pane header, drawn by the caller. This fills rows 1 to
// Dst.Max.Y. c.mu must be held.
func (c *Client) drawSliverLocked(dst uv.Screen, p *protocol.PlacementData, st frameState)
```

Layout inside the sliver, for `Dst` of width `w` starting at row 1:

- **Row 1** — `"<glyph> <title>"`, written with `compose.WriteStyledWidth`
  after `compose.TruncateWidth(dst, text, w-1)`. Glyph from
  `st.paneStatuses[p.PaneID]`; when it is blank or `" "` the row is just the
  title. When the title is empty the row is just the glyph, so a titleless
  pane renders a short row rather than a blank one that reads as a bug.

  **Deviation from the spec, recorded rather than silent.** The spec's
  default for an empty title was "fall back to the pane's command name if
  available". There is no per-pane command to fall back to:
  `spawnPaneLocked` builds every pane from `[]string{s.shell}`
  (`server.go:257`) and nothing stores it per pane, so the fallback would be
  the same string for every pane — noise, not information. Glyph-only it is.
  Revisit if a real per-pane command ever exists.
- **Rows 2..Max.Y** — a one-column spine at `Dst.Min.X + 1`, `"▌"`, styled
  bold when the pane's status glyph is `»` and plain otherwise.

**Activity signal, refined from the spec.** The spec's default was to drive
the pulse from `vtGrid.lastWriteTime`. That needs a new wire field, and
`PaneStatuses` already carries the same information: `»` (`StatusWorking`) is
set by `Write`'s activity heuristic on every write and decays to blank after
three seconds of quiet (`grid.go:237-250`). So the spine reads the glyph that
is already on the wire and no new field is added. Recorded here rather than
silently, because it changes a spec decision.

**Tests** (using Plan 16's `fakeHostScreen` and `newTestClientWithTwoPanes`):

- `TestSliverRendersGlyphAndTitle` — a card-mode snapshot plus titles;
  assert an occluded pane's sliver contains its title text and status glyph,
  and does **not** contain that pane's content.
- `TestSliverWithoutATitleStillRenders` — empty title produces the glyph and
  spine, no blank-looking row.
- `TestFocusedCardRendersContentNotChrome` — the `PlacementFull` card still
  blits its mirror.
- `TestClippedPaneIsNotDrawnAsChrome` — scroll mode, viewport narrow enough
  to clip; assert the clipped pane renders content. This is the regression
  guard for the whole `Kind` design.
- `TestSliverTitleIsTruncatedByWidthNotRunes` — a CJK title in a 10-cell
  sliver does not overflow into the divider column.

**Verification — automated:**
- [x] `go test ./internal/client/ -run 'TestSliver|TestFocusedCard|TestClippedPane' -v` **fails** before the change — **"sliver does not show the title"; the region still read `CONTENT-ONE`**
- [x] same passes after — **5/5 PASS**
- [x] `go test ./internal/layout/ -v` passes with the new sliver width — **ok; `card_test.go` passes `SliverWidth: 4` explicitly so the default change does not reach it**
- [x] `make test` passes — **all 11 packages ok**
- [x] `make check` passes — **25 smoke / golden ok / 7 attach-check**
- [x] `python3 scripts/golden.py` — **unchanged, no regeneration needed: the golden snapshot is captured at startup in scroll mode, which this phase does not touch**

**Verification — manual:**
- [x] `WIDEBOI_LAYOUT=cards make run`, panes, `printf '\033]2;building\007'` in an unfocused one — its sliver shows the title. — **verified by pty probe: with `WIDEBOI_LAYOUT=cards` and a pane titled `compiling`, both the title text and the spine glyph `▌` appear on the wire.**
- [!] Start Claude Code in an unfocused pane and confirm its spinner title appears and updates live in the sliver. — **DOES NOT HOLD, and this is the feature's motivating case.** Claude Code boots fine inside a wideboi pane (its banner renders while focused), but **no title reaches the sliver**: searching the post-focus redraw for `Claude`, `◐` and `✳` finds none, while the spine glyph `▌` is present, so the chrome itself is drawing.

  Not a defect in this work, as far as the evidence goes. The same chrome renders a title correctly when one is set by hand (`printf '\033]2;compiling\007'` → `compiling` appears in the sliver, verified on a pty), and `TestGridTracksTerminalTitle` covers the capture path. Narrowing done:

  - **Not the alternate screen.** A probe confirms `Grid.Title()` captures OSC 2 issued after `ESC[?1049h`.
  - **Not `TERM`.** Panes get `TERM=xterm-256color` (`ptyx/pane.go:48`).
  - **Not being a shell's child.** Claude Code in a plain pty under `/bin/sh` still emits one OSC 0/2 title sequence.

  So something about wideboi's pane specifically stops it — plausibly a terminal capability probe the emulator does not answer, which would make Claude Code decide titles are unsupported. Unverified; recorded in `BEYOND-V1` §8 rather than chased here, since it is a question about one client's behaviour rather than about this change.
- [x] Toggle back with `C-b c` and confirm panes render content again. — **covered by `case_card_layout_toggles`, which round-trips the cursor 59 -> 13 -> 59, plus `TestClientRevertsToScrollLayout`.**

**A test that passed against the bug, caught and fixed.**
`TestSliverTitleIsTruncatedByWidthNotRunes` originally checked the *leftmost*
sliver and passed even with rune-count truncation. `composeFrameLocked` draws
left slivers, then the focused card, then right slivers — so a left sliver's
overflow is painted over by the focused card before anything can observe it.
Retargeted at the rightmost sliver, which is drawn last, and it now fails
correctly against rune truncation: **"row 1 col 52: title glyph 語 spilled
past the sliver's right edge at 50"**.

---

## Phase 7: `+N` for cards that do not fit

`CardStrategy` skips cards outside the viewport with `continue`
(`card.go:73`, `card.go:105`). With 10-cell slivers that is reachable rather
than hypothetical, and a pane that exists but is invisible with no indicator
is the kind of thing that erodes trust in the layout.

**Test first.**

**Files:**
- Modify: `internal/client/client.go` — `hiddenCountsLocked`, marker drawn in `composeFrameLocked`.
- Test: `internal/client/cards_test.go`.

**Note on shape:** the counts are **not** added to `frameState`. That struct
exists so a wipe can compose the frame it is animating away from, and a stale
hidden-count in a retained frame would be wrong for the same reason stale
placements are. `composeFrameLocked` calls `hiddenCountsLocked` directly,
which reads `c.strip` and is current by construction.

**Key changes:**

The count is derived **client-side**, not carried on the wire. The client
already holds `c.strip`, so it can compare the strip's columns against the
placements it just computed; `ComputePlacements` returning a second value
would change the `Strategy` interface for one caller's benefit.

```go
// hiddenCountsLocked reports how many columns have no placement, split
// by which side of the focused column they sit on.
//
// CardStrategy drops cards that do not fit rather than scrolling them
// (see BEYOND-V1 section 2 -- scrolling a row of slivers is its own
// design). Dropping them silently is the part worth fixing: the count
// is derived here rather than carried on the wire because the client
// already has the strip, and widening the Strategy interface for one
// caller is a worse trade. c.mu must be held.
func (c *Client) hiddenCountsLocked() (left, right int)
```

The marker is drawn on **row 0**, the header row, at the far edge —
`"+3"` right-aligned at `c.cols-1` when `right > 0`, left-aligned at column 0
when `left > 0`. Row 0 is chrome already, so no card has to reserve space for
it and `CardStrategy` is untouched.

**Tests:**

- `TestHiddenCardsAreCounted` — a card-mode strip with more columns than fit;
  assert `hiddenCountsLocked` reports the right split.
- `TestHiddenCardMarkerIsRendered` — assert `+N` appears on row 0 at the
  correct edge.
- `TestNoMarkerWhenEverythingFits` — no `+` anywhere on row 0.
- `TestScrollModeNeverShowsAMarker` — scroll mode clips rather than drops, so
  every column has a placement and the marker must not appear.

**Verification — automated:**
- [x] `go test ./internal/client/ -run 'TestHiddenCard|TestNoMarker|TestScrollModeNeverShowsAMarker' -v` **fails** before the change — **build failure: `cli.hiddenCountsLocked undefined`**
- [x] same passes after — **4/4 PASS**
- [x] `make check` passes — **25 smoke / golden ok / 7 attach-check**

**Verification — manual:**
- [x] `WIDEBOI_LAYOUT=cards make run` at 80 columns, `C-b n` until panes stop appearing — a `+N` marker appears. — **verified by pty probe at 80x16: after five `C-b n`, `+1` appears on the wire.**

### Scroll mode drops columns too, which the plan assumed it did not

`TestScrollModeNeverShowsAMarker` was planned on the reasoning that "scroll
mode clips rather than drops, so every column has a placement." **False.**
`ScrollStrategy` skips any column whose `Dst` is empty (`layout.go`), so a
pane scrolled entirely out of view has no placement either — and
`hiddenCountsLocked` duly found four of them, failing the test with
`" [1] ★ ... [2] ... +4"`.

So the default layout has the same silent-invisible-pane problem cards do.
Marking it would arguably be an improvement, but it changes the chrome every
user sees in the mode everyone actually runs, which is far outside this
change. The marker is gated to card mode via a new `Client.layoutMode`, and
the scroll-mode case is recorded in `BEYOND-V1` §2 as a follow-up. The test
survives with its reasoning corrected: it now guards "card chrome must not
leak into the default layout" rather than a false claim about clipping.

---

## Phase 8: Docs

Docs only, no code.

**Files:**
- Modify: `docs/BEYOND-V1.md` — §2 and §6.
- Modify: `docs/LESSONS.md` — only if execution produced a genuine lesson. Do not invent one to fill the section: Plan 16 earned its entry by hitting the same failure twice, and a manufactured lesson dilutes the ones that were paid for.

**Key changes:**

1. §2 stops describing cards as unreachable and records what shipped: mode
   selection, chrome slivers at 10 cells, the `+N` marker.
2. Remove the `CardStrategy is reachable only from tests` row from §6 — Plan
   16 added it and this closes it.
3. §2's two open items are now partly answered: the sliver-is-noise problem is
   fixed; the cards-defer-scrolling problem is *marked* but not solved, so it
   stays with the `+N` behaviour recorded as the current answer.
4. Record what stayed out, so the next reader does not assume otherwise:
   sliver-row scrolling, genuine z-overlap, card animation, mouse
   hit-testing, and the global `compose.WriteString` width fix — which is
   still an open §6 row, now with a scoped width-aware sibling beside it.

**Verification — automated:**
- [x] `make check` passes — **25 smoke / golden ok / 7 attach-check**
- [x] `grep -n "unreachable\|only from tests" docs/BEYOND-V1.md` returns nothing about `CardStrategy` — **clean**

**Verification — manual:**
- [x] Read §2 end to end; it describes what the code now does. — **done, and the read caught two stale claims a grep would not have: the "4-cell sliver is visual noise" bullet still framed chrome as future work, and §6's width row was half wrong (see below).**

**`LESSONS.md` earned an entry.** Not manufactured: `WriteString`'s doc
comment described a width bug the code did not have, `BEYOND-V1` §6 carried a
defect row sourced from that comment rather than the code, and Phase 5 was
budgeted to build a redundant width-aware writer on the strength of the row.
The entry is "An unverified comment becomes an unverified roadmap entry",
with the corollary that a defect row should cite code, not prose.

Plan 16's existing red-step corollary also gained a concrete second example —
the compositing-specific trap where a later draw paints over the failure a
test is looking for, which is how Phase 6's truncation test passed against
deliberately broken truncation.

---

## Spec coverage check

| Spec requirement | Phase |
| --- | --- |
| `WIDEBOI_LAYOUT=cards` starts in card mode | 2 |
| `$mod c` toggles at runtime, visible in bar and help | 3 |
| Occluded card renders chrome, not content | 6 |
| Pane title reaches the client and updates live | 4 |
| Cards that do not fit collapse into `+N` | 7 |
| Switching modes never changes logical width | 3 (asserted), 1 (Kind keeps clipped panes full) |
| `Kind` distinguishes sliver from clipped pane | 1 |
| Width-aware writes for titles only | 5 |
| Titles ride `PaneStatuses`' broadcast | 4 |
