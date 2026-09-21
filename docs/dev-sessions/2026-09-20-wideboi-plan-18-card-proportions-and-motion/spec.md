# Plan 18 — Proportional cards, and motion instead of a wipe

**Goal:** Make the card fan fill its window by giving slivers an even share of
whatever the focused pane leaves, and replace the focus-change wipe with panes
that actually slide.

**Source:** Les, 2026-09-20, after using what Plan 17 shipped — "the rest take
up an even share of the remaining width", and "I don't understand what the
animation is doing, it seems kind of random".

## Current state

Three problems, all confirmed by probe against `aabc009`.

**Slivers are a fixed 10 cells, so the fan leaves the window half empty.** A
120-column viewport with three 30-wide panes focused on the middle places
`(0,10)`, `(10,40)`, `(40,50)` — **70 of 120 columns unused**. At realistic
spawn widths (59 at 120 columns) it is still ~41 dead columns. `sliverWidth`
is a constant that knows nothing about the viewport.

**Dividers between cards are drawn and immediately overwritten.**
`ScrollStrategy` reserves a divider column (`currX += c.Width + 1`);
`CardStrategy` lays cards out contiguously. `composeFrameLocked` draws a
divider at each placement's right edge, which is exactly where the next card
begins, so the next card's blit covers it. Probed: at column 40 the cell holds
pane 3's spine, not a divider. Only the fan's rightmost edge keeps one.

**The wipe shows two layouts at once.** `WipeTransition.Draw` blits frame B
left of a moving seam and frame A right of it. Nothing moves; content
teleports in vertical bands. In scroll mode that loosely mimics horizontal
scrolling. In card mode a focus change resizes and repositions *every* pane,
so the two halves of the screen show different geometries simultaneously —
which is what reads as random. `BEYOND-V1` §1 predicted exactly this: "Cards
need genuine motion — slivers sliding and re-dealing — so the spring design
stays the answer there."

## Desired end state

- The card fan spans the full viewport width, with no dead zone.
- Unfocused panes each get `(viewport − focused) / n` cells, remainder spread
  so the total is exact.
- A pane whose share is at least its own logical width renders **full**, not
  as chrome — card mode degrades gracefully toward scroll-like when
  everything fits.
- A focus change animates panes sliding and resizing between layouts. No
  spliced frames.
- `wipe.go` is gone.

## Design decisions

- **Decision: sliver width is `(viewportWidth − focusedWidth) / sliverCount`,
  with the remainder distributed one cell at a time from the left.**
  - **Why:** it fills the window by construction and removes
    `DefaultSliverWidth` as a tuning constant. Distributing the remainder
    rather than truncating means the fan's right edge lands exactly on the
    viewport edge instead of leaving a 1–3 column gap.
  - **The focused pane keeps its own width**, and slivers take what is left.
    Not the reverse: the focused pane is the one being read, and its logical
    width is load-bearing for the no-shrink premise. With no divider columns
    reserved, Les's worked example holds exactly: a 90-cell viewport, a
    60-cell focused pane and three slivers gives `(90 − 60) / 3 = 10` each.

- **Decision: a pane whose share ≥ its logical width renders `PlacementFull`.**
  - **Why:** a 45-cell "sliver" showing a spine and a short title wastes the
    space it was given. If there is room to show the pane, show the pane.
    Card mode then becomes a consequence of available space rather than a
    separate world, and the sliver/full split stays a property of the
    placement rather than of the mode.
  - **Rejected:** always rendering unfocused panes as chrome — visually
    consistent but wasteful. **Rejected:** capping slivers at a maximum, which
    re-introduces a tuning constant and puts the slack back in a dead zone.

- **Decision: a minimum sliver width. Below it, panes overflow and are counted
  by the existing `+N` marker.**
  - **Why:** chrome needs a few cells to say anything — one for the spine,
    a couple for a glyph. With enough panes the even share rounds to zero, and
    something has to give. The existing overflow path already handles this;
    it just stops being reachable by fixed-width arithmetic and starts being
    reachable by division.
  - **Default: 4 cells** — spine plus a glyph plus a space. Tunable in one
    place, and unlike `DefaultSliverWidth` it is a floor rather than the
    layout's governing number.

- **Decision: cards draw no dividers at all; the client suppresses them for
  card placements.**
  - **Why:** every sliver already draws a spine `▌` at its own left edge, so
    the separator exists — which is why nobody noticed the real dividers were
    being overwritten. Reserving a column instead would match
    `ScrollStrategy` but would eat one cell per card and contradict the
    arithmetic this change is built on: 90 wide with a 60-cell focused pane
    and three slivers gives 10 each only if nothing is reserved.
  - **Rejected:** reserving a divider column per card — visually equivalent
    to the spine, costs `n−1` cells, and makes the shares awkward.
  - **Accepted gap:** the boundary immediately left of the focused card has
    no marker, since the preceding sliver's spine sits at that sliver's left
    edge rather than its right. The focused card is already distinguished by
    carrying content and a reverse-video header. Revisit only if it reads
    badly.

- **Decision: replace the wipe with placement interpolation.** On a focus
  change, keep the previous `[]PlacementData`; for `N` frames, feed
  `composeFrameLocked` a set of rects lerped from old to new with easing.
  - **Why:** it is what the motion was always supposed to be, it is
    *simpler* than the wipe (no frame snapshots, no `Fits` check, no A/B
    surfaces), and it is the only option that makes a re-dealing fan legible.
  - **Delete `wipe.go`, `wipe_test.go`, `Direction`, `pendingWipe`,
    `realizePendingWipeLocked` and the `layerWipe` branch.** One animation
    model, not two.
  - **Content is live, not frozen.** `BEYOND-V1` §1 argued for freezing to
    avoid per-frame emulator work, but interpolation already re-composites
    every frame, so a snapshot would add work rather than save it. Revisit if
    the frame budget says otherwise.

- **Decision: panes present in only one of the two layouts fade by
  interpolating from a zero-width rect at their own edge.**
  - **Why:** opening or killing a column changes the pane set, and a pane that
    simply appears at full size mid-animation is the teleporting this change
    exists to remove.

## Patterns to follow

- `ScrollStrategy`'s divider reservation: `internal/layout/layout.go`,
  `currX += c.Width + 1`.
- The placement loop and chrome branch that interpolation feeds:
  `Client.composeFrameLocked`, `internal/client/client.go`.
- Card property tests to extend, including the logical-width invariant:
  `internal/layout/card_test.go`, `internal/layout/kind_test.go`.
- Client render tests with `fakeHostScreen` / `newCardClient`:
  `internal/client/cards_test.go`, `internal/client/screen_test.go`.
- The smoke case that already round-trips the toggle on cursor column:
  `scripts/smoke.py`, `case_card_layout_toggles`.

## What we're NOT doing

- **Spring physics.** Eased linear interpolation first; `(current, velocity,
  target)` springs and mid-flight retargeting stay in `BEYOND-V1` §1.
- **Scrolling the sliver row.** Overflow still gets `+N`.
- **Genuine z-overlap.** Cards still do not slip under each other.
- **Touching `ScrollStrategy`'s geometry.** Only card layout changes; the
  scroll path keeps its current placements exactly.
- **The scroll-mode overflow marker.** Still card-mode only.
- **`OSC 9;4`,** and the open question of why Claude Code sets no title in a
  pane. Both stay in `BEYOND-V1` §8.

## Open questions

- **How many frames, and what easing?** Default: keep 8 frames at the existing
  16 ms tick (~128 ms) and use ease-out, so the motion settles rather than
  stopping dead. Revisit by eye once it is on screen; this is the one part of
  the change that genuinely wants looking at rather than specifying.
- **Does interpolation cost too much per frame?** Default: ship it and measure
  on a pty the way Plans 16 and 17 measured the wipe (snap ≈ 1.2 KB, wipe
  ≈ 3.7 KB per transition). If it lands far above `BEYOND-V1` §1's ~20x
  estimate, reduce frame count before abandoning the approach.
- **Should a focus change during an animation retarget or restart?** Default:
  restart from the current interpolated rects, which is strictly better than
  Plan 16's restart-from-previous-state and avoids the teleport that made
  retargeting visible.
