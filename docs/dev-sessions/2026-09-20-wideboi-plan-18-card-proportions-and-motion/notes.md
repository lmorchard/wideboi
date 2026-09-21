# Plan 18 — notes

Les used what Plan 17 shipped and reported two things: slivers should share
the remaining width, and *"I don't understand what the animation is doing, it
seems kind of random."* Both were right, and the second was worse than it
sounded.

## What landed

| Phase | What |
| --- | --- |
| 1 | Cards divide the window; full-render cap; `MinSliverWidth` floor; no card dividers |
| 2 | Placement interpolation replaces the wipe; `wipe.go` deleted |
| 3 | `BEYOND-V1` §1 and §2 |

## The animation was never motion

`WipeTransition` blitted the new layout left of a moving seam and the old
layout right of it. **Nothing moved.** Content teleported in vertical bands.

That was semi-defensible in the scrolling strip, where a horizontal seam
loosely mimics horizontal scrolling. Cards made it incoherent: a focus change
resizes and repositions *every* pane, so the two halves of the screen showed
completely different geometries at once. "Kind of random" is the accurate
description.

`BEYOND-V1` §1 predicted this in so many words — *"Cards need genuine motion…
a wipe is for focus switches"* — and Plan 17 shipped cards while keeping the
wipe anyway. **Worth noticing as a pattern: the roadmap said the thing, and we
still had to hit it to act on it.**

## Two measurements that overturned recorded assumptions

**Motion costs ~3.8x a snap, not ~20x.** 4,664 bytes for a card-mode focus
change, against a 1,214-byte snap and the wipe's 3,712. §1's ~20x estimate
assumed an N-frame animation costs N full repaints; the renderer diffs, and a
sliding pane changes far fewer cells per frame than a whole screen.

This matters beyond this plan: **§1's entire "wipes are the cheap first cut"
argument rests on a 10x gap that is actually about 1.2x.** The analysis is
kept — the byte table is still useful — but now opens by saying its premise
did not hold. A future plan reaching for it to justify avoiding motion would
otherwise inherit the error.

**An animation that moves nothing should not run.** Motion is now armed on a
placement *change*, not a focus change. Two panes that both fit keep identical
placements when focus moves between them, so scroll mode spends 694 bytes
where the wipe spent thousands. The wipe fired regardless, which is part of
why it read as arbitrary.

## Fixture findings worth keeping

Seven Plan 17 client tests needed new fixtures — none were regressions, all
were setups that no longer produce the conditions they test. Three 30-cell
panes in a 100-column viewport used to yield fixed 10-cell slivers; under
proportional shares they get 35 each, exceed their own width, and correctly
render full.

**One had silently stopped discriminating.**
`TestSliverTitleIsTruncatedByWidthNotRunes` watched the composited screen and
passed against deliberately broken truncation. The reason is structural and
worth understanding: cards are now packed contiguously across the full
viewport, so a sliver's overflow either lands in the next card's region and is
painted over when that card draws, or runs off the right edge and is clipped.
**The composed screen can no longer show this defect at all.** Rewritten to
assert the invariant directly against `drawSliverLocked` with the sliver
parked mid-surface.

This is the *second* time that test needed re-aiming — Plan 17's notes record
the first, where it watched the leftmost sliver and the focused card painted
over the spill. The general shape: **a test that asserts "X does not escape
its bounds" is only valid while something outside those bounds would show it.**
Layout changes quietly invalidate that.

The obvious motion fixture also animates nothing, correctly: two 25-cell panes
in a 60-cell viewport are both fully visible, so a focus change moves no
placement. `newMotionClient` uses card mode with panes wider than their share.

## What the Copilot review caught

Three comments. Two were real bugs I introduced, and both were in the new
motion code.

1. **A status snapshot restarted the animation.** Arming compared *what is on
   screen* against the new target — but an interpolated layout never equals
   its target, so every frame of a running animation satisfied it. And
   `broadcastLayoutIfStatusChanged` sends a snapshot whenever a busy pane's
   glyph changes, so the step counter reset repeatedly and the motion never
   settled. Fixed by comparing the new target against the *previous target*
   while still starting from what is on screen — two different "previous"
   values, and the distinction is the whole bug. Caught with `step went 2 -> 0
   across status-only snapshots`.
2. **Interpolated rects overlap, and nothing sorted by `Z`.**
   `composeFrameLocked` paints in slice order and the interpolated set
   preserved the target's left-to-right order, so a `Z=0` sliver could paint
   over the `Z=1` pane the user is looking at — and a collapsing pane, being
   appended last, painted over everything. Only reachable mid-transition,
   which is exactly the state that did not exist before this PR. Now sorted
   back-to-front, stably so equal-`Z` panes keep left-to-right order for the
   divider logic.
3. **The roadmap still pointed at deleted code.** I rewrote §1's opening and
   flagged its cost premise, but left the analysis below still describing
   `WipeTransition.Draw` as live and saying wipes and springs coexist.

Worth noting where these landed: **both real bugs were in transient states —
mid-animation overlap, and a mid-animation snapshot.** The same shape as the
last two reviews, which clustered in multi-client and teardown paths. Steady
state gets tested; the in-between does not.

## Deliberately not done

- **Real springs.** Eased interpolation only; velocity and momentum-carrying
  retarget stay in §1. Retargeting works by restarting from the current
  interpolated rects, which is bounded and never jumps, but does not carry
  momentum.
- **Scrolling the sliver row.** Overflow still gets `+N`.
- **Genuine z-overlap.** Cards still do not slip under each other; the z-fan
  is still decoration.
- **The scroll-mode overflow marker.** `ScrollStrategy` drops off-screen
  columns silently too — recorded in §2 since Plan 17, still unaddressed,
  because surfacing it changes the default layout's chrome for every user.

## Where to pick up

- **Les's eye on the motion.** The one plan checkbox left unchecked. Frame
  count (8) and easing (ease-out cubic) are guesses that wanted looking at,
  and the cost measurement says there is headroom to spend if it feels too
  fast.
- **`OSC 9;4`** — still the highest-value agent-status work, and still the
  reason the card fan is worth having. See §8.
- **Why Claude Code sets no title inside a pane.** Directly undercuts sliver
  chrome; ruled out alt screen, `TERM` and shell-child shape.
- **Springs**, now that the cost objection to motion is gone.
