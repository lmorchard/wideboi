# Notes

## Decisions (Les, 2026-09-22)

- #48: `+N` markers in both modes, same chrome. The README lists cards
  as the default layout now, so the original worry ("changes the default
  layout's chrome") mostly no longer applies anyway.
- #20: a sticky window, not re-centring. See spec.md.

## What landed

- `drawHiddenMarkersLocked` no longer returns early outside card mode.
  `TestScrollModeNeverShowsAMarker` became `TestScrollModeMarksOffScreenPanes`
  plus a no-overflow counterpart. New smoke case
  `scroll mode marks off-screen panes`, proven red by reverting the gate.
- `Strip.cardFirst` plus a windowed `CardStrategy.visibleSides`, which now
  takes the Strip. The window is `budget+1` cards with a 1-card margin when it
  holds ≥3, clamped to the strip, and it mutates `s.cardFirst` from inside
  `ComputePlacements` exactly as `ScrollStrategy` mutates `s.scrollX`.
- Tests: two example tests (sticky inside, slides at the margin), both
  red against the old code, plus a rapid property (contiguous run, focus
  placed, neighbours placed when size ≥3). The property was proven to fail
  by zeroing the margin.

## Verification

- `make check` 8/8 green (4 before and 4 after the smoke case was added).

## Not done / worth eyeballing

- Card-mode *feel* when the window slides has not been looked at by
  eye. When it slides, one card enters and one leaves in the same frame. The
  motion code handled that before too, since re-centring did it on every move.
- `cardFirst` is not reset on mode switch or column add/kill. It is clamped
  every compute, so it is always valid, just possibly not where you'd pick.
  Same as `scrollX`.
- Board transitions skipped: the `gh` token lacks `read:project`
  (`gh auth refresh -s read:project,project`).

## PR #73

- Before pushing I removed a stray rapid `.fail` file that my margin-breaking
  probe had left in `internal/layout/testdata/rapid/`. Check the full diffstat
  after any "prove it fails" run with rapid.
- Copilot: one finding. The right `+N` was written into the terminal's last
  column (LESSONS: never write the last column). This predates the PR in card
  mode, but the PR spread it to scroll mode. Fixed by shifting it one cell left,
  and the test now pins the position. Pushed as a normal commit instead of
  force-pushing, per LESSONS on force-push racing the merge.
