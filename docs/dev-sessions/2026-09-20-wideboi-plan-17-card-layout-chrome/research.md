# Research — Plan 17 (card layout + sliver chrome)

Verified against `0febbe4` (Plan 16 merged) on 2026-09-20. Facts and file
references; decisions belong in `spec.md`.

## What already exists

`internal/layout/card.go` — `CardStrategy` implements `Strategy`, with unit
tests (`card_test.go:11`) and `rapid` property tests (`card_test.go:45`). It
honours the invariant that matters: `ColumnWidth` is untouched, so an occluded
pane's child never learns it is partly covered.

**It is unreachable.** `Strip.SetStrategy` (`layout.go:43`) has exactly one
caller in the repo and it is `card_test.go:13,48`. `NewStrip` installs
`ScrollStrategy{}` (`layout.go:38`) and nothing ever replaces it. No verb, no
key, no config.

What it emits (`card.go:15-121`):

- Single column: one placement, full width, `Z=1`.
- Otherwise: left slivers at `dstX = i*sliverWidth` with `Z=0`, the focused
  card at a computed `focusedX` with `Z=1`, right slivers after the focused
  card's right edge with `Z=0`.
- Sliver `Src = Rect(0, 0, dst.Dx(), availHeight)` — the **leftmost 4 columns
  of real pane content**. `DefaultSliverWidth = 4` (`card.go:7`).
- Cards that would fall outside the viewport are skipped with `continue`
  (`card.go:73`, `card.go:105`) — they silently vanish rather than scrolling.

## The two problems §2 records

1. **A 4-cell sliver of real terminal content is visual noise.** The sliver
   that earns its space is chrome: a vertical spine with the status glyph, a
   truncated title, and a colour that pulses on activity.
2. **Cards don't eliminate scrolling, they defer it.** Past ~20-25 cards the
   focused pane has no room and the strip must scroll a row of slivers.
   Currently they just disappear.

## Everything chrome needs already exists — except the plumbing

### Title: available, unregistered

`vt.Callbacks` has `Title func(string)` and `IconName func(string)`. `NewVT`
(`grid.go:176-178`) registers **only** `CursorVisibility`, so the title is
parsed and dropped. `x/vt` already handles OSC 0/1/2 into `Emulator.title`
(`handlers.go:305-316`, `osc.go:21-48`).

Adding it is: a callback in `NewVT`, a `Title() string` on the `Grid`
interface (`grid.go:50-75`), `Pane.Title()` mirroring `Pane.Status()`
(`pane.go:211`), a `PaneTitles map[int]string` on `MsgLayoutSnapshot`
alongside the existing `PaneStatuses` (`messages.go:99`), and a client field.
`PaneStatuses` is the exact precedent for all of it.

**This is worth more than it looks.** Measured 2026-09-20: Claude Code emits a
live title carrying a spinner and a turn summary — `◐ Claude Code`,
`◑ Pong reply`, `✳ Pong reply`. That is precisely the "truncated title" §2
asks a sliver to show, already self-updating, for free. See `BEYOND-V1.md` §8.

### Activity: available, unexported

`vtGrid.lastWriteTime atomic.Pointer[time.Time]` (`grid.go:138`, set at
`grid.go:237`) already tracks per-pane write recency — it is what drives the
3-second idle fallback in `Status()` (`grid.go:247`). "A colour that pulses on
activity" needs exactly this value, exposed.

### Status glyph: works as of Plan 16

`PaneStatus.Glyph()` (`grid.go:116-129`) → `»` / `!` / `✓` / `✗` / `" "`, and
`PaneStatuses` already crosses the wire and renders in the pane header
(`client.go:230`) and status bar (`client.go:373`).

## The hard problem: a sliver is not distinguishable from a clipped pane

`Placement` and `PlacementData` are both `{PaneID, Src, Dst, Z}`
(`layout.go`, `messages.go:53`). A card sliver is a narrow `Dst` with a
cropped `Src`. **So is a pane clipped by the viewport edge under
`ScrollStrategy`** — which is the behaviour
`scripts/smoke.py:case_partly_clipped_pane_keeps_full_width` exists to
protect.

`Z` does not discriminate either: `ScrollStrategy` emits `Z=0` for everything
(`layout.go:184+`), and `CardStrategy` emits `Z=0` for slivers, so a `Z=0`
placement could be either.

So the client cannot currently tell "draw chrome here" from "draw the left
edge of a clipped pane here". **Something has to mark it**, and that mark has
to cross the wire. This is the load-bearing design decision for this session.

Note the wire constraint: `protocol.TestWireTypesCarryNoInterfaces` enforces
flat scalars on wire types. A `bool` or a small named `int` is fine; anything
with an interface field is not.

## Rendering: where chrome would be drawn

Plan 16 extracted `composeFrameLocked(dst, st, drawPane)`
(`client.go`), which loops placements and for each one draws a header, then
either `drawPane(...)` or `compose.Blit(dst, mirror.Surface, p.Dst)`. A sliver
would branch here: draw a spine instead of pane content. The seam is in the
right place already.

**Hazard, recorded and now relevant.** `compose.WriteString`/`WriteStyled`
ignore `Cell.Width` (`surface.go:30-44`) — one rune per cell. Its own doc
comment says this "matters sooner than it looks: WriteString is what a reader
will reach for when the status glyphs land." A vertical spine drawing `»`,
`✓`, `✗` down a column is single-width and fine; a truncated *title* is
arbitrary user text and may contain CJK or emoji, which will misalign. This is
an open row in `BEYOND-V1.md` §6.

Also: `Client.Draw` is now testable via the `HostScreen` interface and the
`fakeHostScreen` / `newTestClientWithTwoPanes` fixtures added in Plan 16
(`screen_test.go`), so chrome rendering is unit-testable from the start.

## Selecting the strategy

No precedent for a runtime layout toggle. The nearest patterns:

- **Verbs**: `internal/keys/keys.go:81-95` is a single table (letter, verb,
  labels, whether it needs a detachable session) read by the router, the
  status bar and the help overlay. Adding a verb is populating that table.
  Constraint from Plan 15: `i`, `m` and `[` can never be bound (their control
  bytes are Tab, Enter, Escape), enforced by a test. Free letters need
  checking against the current table.
- **Env config**: `WIDEBOI_PREFIX` is the only existing runtime config
  (`BEYOND-V1.md` §8 records that a config file is wanted but undesigned).

`SetStrategy` lives on `layout.Strip`, which the **server** owns
(`server.go:25`) — but placements are computed **client-side** since Plan 12
(`client.go:85`), from `c.strip` synced via `SyncColumns`. So a strategy
toggle has to reach the client's strip, not just the server's. Check whether
`SyncColumns` carries it or whether it needs a new snapshot field.

## Verification surface

- `make check` = fmt-check, lint, seam-check, test, race, verify-exit, smoke
  (24 cases), golden, attach-check (7 cases). ~2 minutes.
- **`make golden` will need regenerating** if chrome changes startup output —
  `testdata/golden/startup.txt` is a committed byte snapshot.
- Card placements already have property tests to extend
  (`card_test.go:45`), including the focused-pane-visible and
  logical-width-unchanged invariants.
