# Plan 17 — Card layout, reachable, with sliver chrome

**Goal:** Make `CardStrategy` selectable, and give an occluded card a sliver
worth the space it takes — status glyph, live title, activity colour — so you
can watch a fan of agent panes at once.

**Source:** user request, 2026-09-20, following Plan 16.

## Current state

`CardStrategy` (`internal/layout/card.go`) is complete, unit-tested and
`rapid` property-tested, and **unreachable**: `Strip.SetStrategy` has exactly
one caller in the repo and it is `card_test.go`. Every running wideboi is a
`ScrollStrategy`.

Two things stand between it and being useful, both recorded in
`BEYOND-V1.md` §2 and confirmed in `research.md`:

- A sliver currently shows `Src = Rect(0,0,4,h)` — **the leftmost 4 columns of
  real pane content**, which §2's own analysis calls visual noise.
- Cards that fall outside the viewport are skipped with `continue`
  (`card.go:73,105`) — they vanish with no indication the panes exist.

Everything chrome needs already exists but is unplumbed: `vt.Callbacks.Title`
is parsed by `x/vt` and never registered by `NewVT` (`grid.go:176-178`);
`vtGrid.lastWriteTime` (`grid.go:138`) already tracks per-pane write recency;
`PaneStatus.Glyph()` works as of Plan 16.

**Why the title matters more than it looks.** Measured 2026-09-20, Claude Code
emits a live title carrying a spinner and a turn summary — `◐ Claude Code`,
`✳ Pong reply` — and **no OSC 133 at all**. For the workload this project was
started for, the title is the status signal we actually have. See
`BEYOND-V1.md` §8.

## Desired end state

- `WIDEBOI_LAYOUT=cards` starts in card mode; `$mod c` toggles it at runtime,
  and the binding appears in the status bar and help overlay like every other.
- An occluded card renders as chrome, not content: status glyph and truncated
  title on one row, an activity-coloured spine below.
- A pane's terminal title reaches the client and updates live.
- Cards that do not fit collapse into a `+N` marker instead of vanishing.
- Switching modes never changes a pane's logical width — the no-shrink
  premise holds under both strategies, asserted at the layout layer.

## Design decisions

- **Decision: `Placement` and `PlacementData` gain a `Kind PlacementKind`
  field** (`PlacementFull`, `PlacementSliver`).
  - **Why:** a card sliver and a viewport-clipped pane are otherwise
    identical on the wire — both a narrow `Dst` with a cropped `Src` — and
    `Z` doesn't discriminate either, since `ScrollStrategy` emits `Z=0` for
    everything. Without a mark the client would render chrome over a
    legitimately clipped pane, which is exactly what
    `case_partly_clipped_pane_keeps_full_width` exists to protect.
  - **Rejected:** a `Sliver bool` — enough for today's two cases, but this is
    a wire type, so a third presentation later means a bool beside a bool.
  - **Rejected:** inferring from `Dst.Dx()` — fragile in precisely the
    protected case; a clipped pane can legitimately be 4 cells wide.

- **Decision: sliver width becomes ~10 cells, and slivers show chrome.**
  - **Why:** §2 assumed 4 cells *because* it assumed slivers show content.
    Once a sliver shows chrome the width is a free parameter, and 10 cells
    fits a horizontal truncated title, which is far more readable than
    running text vertically one character per row.
  - **Accepted cost:** ~10-12 cards at 200 columns instead of ~25. That is
    the trade this buys readability with, and it makes the overflow decision
    below load-bearing rather than theoretical.

- **Decision: cards that do not fit collapse into a `+N` marker.**
  - **Why:** `CardStrategy` currently drops them silently. A pane that exists
    and is invisible with no indicator erodes trust in the layout, and with
    10-cell slivers overflow is reachable rather than hypothetical.
  - **Rejected:** scrolling the row of slivers — what §2 predicts is
    eventually needed, but it is a second scrolling model layered on the
    first and much larger than this session.

- **Decision: both an env var and a verb.** `WIDEBOI_LAYOUT=cards` for the
  startup default, `$mod c` to toggle.
  - **Why:** the env var matches the only existing runtime config
    (`WIDEBOI_PREFIX`) and the verb is what lets you flip back and forth to
    judge whether the fan earns its space. `c` is free and mnemonic.
  - **Note:** `c`'s repeat form is `C-c`. `keys.Reserved` only forbids
    letters whose control byte decodes as a *different named key* (Tab,
    Enter, Escape), so `c` is legal — and repeat is meaningless for a toggle
    anyway.
  - **Constraint:** placements are computed **client-side** since Plan 12, so
    the toggle must reach the client's `strip`, not only the server's.

- **Decision: a width-aware write/truncate pair in `compose`, used only by
  sliver chrome.**
  - **Why:** `WriteString`/`WriteStyled` ignore `Cell.Width` — one rune per
    cell (`surface.go:30-44`). Titles are arbitrary text and Claude Code's
    already contain `◐` and `✳`, which are ambiguous-width. A scoped helper
    that advances by `cell.Width` fixes titles without changing layout maths
    under the pane header, dividers, status bar and help overlay.
  - **Rejected:** fixing `WriteString` globally — more correct, and it closes
    an open `BEYOND-V1` §6 row, but it moves every piece of chrome at once
    and would churn the golden snapshot. Its own task.
  - **Rejected:** sanitizing titles to single-width — safe but it drops the
    spinner glyphs that make the title informative.

- **Decision: titles cross the wire as `PaneTitles map[int]string` on
  `MsgLayoutSnapshot`.**
  - **Why:** `PaneStatuses` is the exact precedent, including the
    change-detection broadcast Plan 16 added. Titles should ride the same
    trigger rather than inventing a second one.

## Patterns to follow

- Status glyph end-to-end, the template for titles: `grid.go:116-129` →
  `pane.go:211` → `server.go` (`statusGlyphsLocked`, `broadcastLayoutIfStatusChanged`)
  → `messages.go:99` → `client.go:230,373`.
- Callback registration: `NewVT`'s existing `CursorVisibility` callback,
  `grid.go:176-178`.
- The chrome-vs-content branch belongs in `composeFrameLocked`
  (`internal/client/client.go`), at the existing `drawPane`/`mirror` fork.
- Client-side render tests: `fakeHostScreen` and `newTestClientWithTwoPanes`
  from Plan 16, `internal/client/screen_test.go`.
- Keybinding: the single table at `internal/keys/keys.go:81-95`, read by the
  router, status bar and help overlay.
- Layout property tests to extend: `card_test.go:45`.

## What we're NOT doing

- **Scrolling the sliver row.** Overflow gets a `+N` marker; §2's eventual
  scrolling model stays parked.
- **Fixing `compose.WriteString` globally.** Scoped helper only; the §6 row
  stays open.
- **OSC 9;4 progress support.** `BEYOND-V1` §8 now records it as the real
  agent-status protocol, and it would make these slivers considerably more
  useful — but it is a separate change with its own precedence questions
  against OSC 133.
- **Animating card transitions.** §2's note that springs are needed for
  re-dealing slivers stays parked; the Plan 16 wipe fires on focus change and
  is not being extended.
- **Mouse hit-testing on cards**, even though `[]Placement` in descending `Z`
  is the right shape for it.
- **Card `Z`-fan overlap.** `CardStrategy` emits non-overlapping rects today;
  making cards genuinely slip under each other is not in scope.
- **A config file.** Env var plus verb only.

## Open questions

- **Should the focused card's own sliver appear in the fan?** Default: no —
  the focused pane renders full-width and is not also represented as a
  sliver, which is what `CardStrategy` already does.
- **What does an empty title render as?** Default: fall back to the pane's
  command name if available, otherwise just the status glyph and the spine.
  Panes with no title must not render a blank row that looks like a bug.
- **How does the activity colour "pulse"?** Default: a two-step
  bright/normal distinction driven by `lastWriteTime` within the last ~1s,
  not a continuous animation — the render loop already ticks at 16 ms and
  nothing else in the client animates per-frame outside a wipe.
