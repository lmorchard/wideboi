# Spec: hidden-pane markers in scroll mode (#48) and a sticky card window (#20)

## #48: `+N` in scroll mode

`ScrollStrategy` drops columns that are scrolled fully out of view, and
`hiddenCountsLocked` already counts them. `drawHiddenMarkersLocked` only
drew the marker in card mode. **Decision (Les, 2026-09-22):** show it in both
modes, using the same chrome (a bold `+N` at the left/right end of the header row).

## #20: scroll the row of slivers

Before this change, `CardStrategy.visibleSides` re-centred on focus on every
move, showing the nearest cards alternately outward. So every focus move changed
*which* cards were visible, on both sides.

**Decision (Les, 2026-09-22):** a sticky window, the card-mode counterpart
of `ScrollStrategy`'s `scrollX`.

- The Strip remembers the index of the leftmost visible card (`cardFirst`).
- The window holds `budget + 1` cards (the focused card plus however
  many slivers clear `MinSliverWidth`, or the pinned `SliverWidth`).
- Moving focus inside the window leaves the set alone. The window slides only
  when focus would get closer than a 1-card margin to either edge.
- The margin is 1 when the window holds at least 3 cards, otherwise 0. This
  keeps the old guarantee that both neighbours of the focused card are visible
  whenever that is possible.
- The window is clamped to the strip, so it never shows empty space when
  there are cards to fill it.

## Non-goals

- Panning the sliver row without moving focus.
- Changing the geometry of the cards that *are* visible (widths, overlap, Z).
- Deduplicating server/client placement computation (#47).
