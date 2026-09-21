# Plan 18 — Proportional cards + motion Implementation Plan

**Goal:** Slivers take an even share of whatever the focused pane leaves, and a
focus change moves panes instead of splicing two layouts together.

**Approach:** Rewrite `CardStrategy`'s geometry around division rather than a
constant, then delete the wipe and interpolate placements instead.

**Tech stack:** Go, `charmbracelet/ultraviolet`, Python for the pty smoke suite.

**Commit per phase:** `Phase N: <name>`.

---

## Phase 1: Cards divide the window instead of taking a fixed slice

Today a 120-column viewport with three 30-wide panes uses 50 columns and leaves
70 dead, because `DefaultSliverWidth` is a constant. Replace it with a share of
the remainder, cap each card at its own logical width, and let cards that
cannot get a usable share overflow into the existing `+N`.

**Test first.**

**Files:**
- Modify: `internal/layout/card.go` — rewrite `ComputePlacements`; `DefaultSliverWidth` → `MinSliverWidth`.
- Modify: `internal/client/client.go` — suppress dividers for card placements.
- Test: `internal/layout/card_test.go`, `internal/layout/kind_test.go`, `internal/client/cards_test.go`.

**Key changes:**

```go
// MinSliverWidth is the narrowest a card can be and still say
// anything: one column of spine, a status glyph, and a space.
//
// This replaces DefaultSliverWidth, which governed the layout and
// left the rest of the window empty. A floor is a different kind of
// number -- it only decides how many cards fit, not how wide they are.
const MinSliverWidth = 4
```

The geometry, replacing everything after the `numCols == 1` early return:

```go
focusedIdx := s.focusIndex
focusedW := min(s.columns[focusedIdx].Width, viewportWidth)

// Slivers divide whatever the focused pane leaves. No divider
// columns are reserved: each sliver draws a spine at its own left
// edge, which is the separator.
sliverCount := numCols - 1
remaining := max(viewportWidth-focusedW, 0)

// With enough columns the even share rounds below what chrome needs.
// Show as many as clear the floor, dropping from the outside in so
// the cards nearest the focused pane survive -- those are the ones
// whose neighbours you are most likely to want.
shown := sliverCount
if shown > 0 && remaining/shown < MinSliverWidth {
    shown = remaining / MinSliverWidth
}
```

Widths are then the even share with the remainder spread one cell at a time,
so the fan's right edge lands exactly on the viewport edge:

```go
// shareAt returns the width of the i-th of n slivers dividing total
// cells, giving the first (total % n) of them one extra cell so the
// sum is exactly total.
func shareAt(total, n, i int) int {
    if n <= 0 {
        return 0
    }
    w := total / n
    if i < total%n {
        w++
    }
    return w
}
```

Cards are then laid out left to right by *actual* width, so a card capped at
its own logical width does not leave a hole:

```go
// A card gets its share, but never more than the pane is wide -- a
// 45-cell "sliver" showing a spine and a title wastes what it was
// given. When the share covers the whole pane there is nothing
// occluded, so it renders as content rather than chrome.
w := min(share, col.Width)
kind := protocol.PlacementSliver
if w >= col.Width {
    kind = protocol.PlacementFull
}
```

Cards nearest the focused pane are kept when `shown < sliverCount`: the left
side keeps the `shown_left` columns immediately left of focus, the right side
the `shown_right` immediately right, split proportionally to how many exist on
each side.

Divider suppression in `composeFrameLocked`:

```go
// Cards draw no dividers. They are laid out contiguously, so a
// divider at one card's right edge is the next card's first column
// and gets painted over the moment that card draws -- which is why
// every divider but the last was invisible. The sliver spine already
// separates them.
if c.layoutMode != protocol.LayoutCards && p.Dst.Max.X < c.cols {
```

**Tests:**

- `TestCardsFillTheViewport` — Les's worked example: 90 columns, four panes,
  focused 60 wide → three slivers of exactly 10, and the placements' union
  covers `[0, 90)` with no gap.
- `TestCardShareRemainderIsDistributed` — a remainder that does not divide
  evenly (e.g. 91 − 60 = 31 over 3) produces widths summing to exactly 31.
- `TestWideShareRendersFullNotSliver` — 120 columns, three 30-wide panes: every
  placement is `PlacementFull` and none is chrome.
- `TestNarrowSharesOverflowRatherThanStarve` — many columns in a small
  viewport: every emitted sliver is at least `MinSliverWidth`, and the ones
  dropped are the outermost.
- `TestCardColumnWidthsAreUntouched` — extend the existing `rapid` property
  check: no viewport size changes any `ColumnWidth`. This is the no-shrink
  premise and the thing a geometry rewrite is most likely to break.
- `TestCardsDrawNoDividers` — client-level: in card mode no `│`/`┃` appears
  between cards; in scroll mode they still do.

**Verification — automated:**
- [x] `go test ./internal/layout/ -run 'TestCardsFill|TestCardShare|TestWideShare|TestNarrowShares' -v` **fails** before the rewrite — **build failure: `undefined: layout.MinSliverWidth`**
- [x] same passes after — **all 7 new layout tests PASS**
- [x] `go test ./internal/layout/ -v` — the existing card and kind tests still pass — **20/20, none of Plan 17's layout tests needed changing**
- [x] `go test ./internal/client/ -run TestCardsDrawNoDividers -v` passes — **and its sibling `TestScrollModeStillDrawsDividers`, so suppression is card-mode only**
- [x] `make check` passes — **25 smoke / golden ok / 7 attach-check**

**Verification — manual:**
- [x] `WIDEBOI_LAYOUT=cards` on a pty with four panes: confirm the fan reaches the viewport edge. — **rightmost written column is 99 of 100. (Not 100: the renderer never writes a row's last cell — see `LESSONS.md`, "Never write the terminal's last column".) 416 spine glyphs, 0 dividers.**

### Seven Plan 17 client tests needed new fixtures

Not regressions — their setups no longer produce the conditions they test.
Three panes of 30 cells in a 100-column viewport used to yield fixed 10-cell
slivers; under proportional shares they get 35 each, exceed their own width,
and correctly render *full*. Likewise 6 columns in 60 cells no longer overflow,
because a 6-cell share clears the 4-cell floor.

Fixtures updated with the reason recorded in each: `threeColumns` panes are
60 wide so a share is narrower than the pane, and the overflow fixtures use 14
columns so the division rounds under the floor.

**One of them had silently stopped discriminating.**
`TestSliverTitleIsTruncatedByWidthNotRunes` watched the composited screen, and
after the geometry change it passed against deliberately broken rune-count
truncation. The reason is structural: cards are now packed contiguously across
the full viewport, so a sliver's overflow either lands in the next card's
region and is painted over when that card draws, or runs off the right edge
and is clipped. **The composed screen can no longer show this defect at all.**

Rewritten to assert the invariant directly — `drawSliverLocked` into a blank
surface, with the sliver parked mid-screen, asserting nothing is written
outside its rect. Now fails correctly: *"sliver wrote 本 at (36,1), outside its
rect (20,1)-(35,11)"*. This is the second time this exact test has needed
re-aiming; Plan 17's notes record the first.

---

## Phase 2: Panes move, instead of two layouts being spliced

`WipeTransition` blits the new layout left of a moving seam and the old layout
right of it. Nothing moves. In card mode, where a focus change repositions
every pane, the two halves show different geometries at once — which is what
reads as random.

**Test first.**

**Files:**
- Delete: `internal/client/wipe.go`, `internal/client/wipe_test.go` (`TestWipeTransitionSteppingAndDirection`, `TestWipeRightToLeftStepping` test only the deleted type).
- Modify: `internal/client/client.go` — `motion` replaces `activeWipe`/`pendingWipe`; drop `realizePendingWipeLocked`, `layerWipe`, `Direction`, `wipeSteps`.
- Rename + rewrite: `internal/client/draw_wipe_test.go` → `motion_test.go`. Its four tests all name wipe internals; three carry over with new names and one splits. See "Tests" below.

**Key changes:**

```go
// motionFrames is how many frames a layout change animates over. At
// main.go's 16ms render tick that is about 128ms.
const motionFrames = 8

// motion animates placements from one layout to the next.
//
// It replaces a wipe that blitted the new layout on one side of a
// moving seam and the old layout on the other. Nothing moved: content
// teleported in vertical bands, and in card mode -- where a focus
// change resizes and repositions every pane -- the screen showed two
// different geometries at once. Interpolating the rects instead means
// panes slide and resize, which is what makes a re-dealing fan
// legible, and it is less machinery than the wipe was: no frame
// snapshots, no viewport-fit check.
type motion struct {
    from  []protocol.PlacementData
    to    []protocol.PlacementData
    step  int
    total int
}
```

`HandleServerMsg` arms it wherever placements change, not only on focus change:

```go
// Restart from where the animation currently is, not from the
// pre-animation layout: a second focus change mid-flight should
// continue from what is on screen.
prev := c.currentPlacementsLocked()
...
if !placementsEqual(prev, c.placements) {
    c.motion = &motion{from: prev, to: c.placements, total: motionFrames}
}
```

Interpolation, with ease-out so the motion settles rather than stopping dead:

Supporting helpers, so nothing above references something undefined:

```go
// currentPlacementsLocked is what is on screen right now: the
// interpolated rects if an animation is running, otherwise the
// settled ones. Arming a new animation from here rather than from
// c.placements is what makes a second focus change mid-flight
// continue from what the user is looking at.
func (c *Client) currentPlacementsLocked() []protocol.PlacementData {
    if c.motion != nil {
        return c.motion.at()
    }
    return c.placements
}

// at returns the placement set for the animation's current step.
func (m *motion) at() []protocol.PlacementData {
    if m.total <= 0 {
        return m.to
    }
    return interpolate(m.from, m.to, easeOutCubic(float64(m.step)/float64(m.total)))
}

// placementsEqual reports whether two placement sets would render
// identically, so a snapshot that changed only a status glyph does
// not animate anything.
func placementsEqual(a, b []protocol.PlacementData) bool
```

```go
// easeOutCubic maps linear progress to a decelerating curve.
func easeOutCubic(t float64) float64 {
    u := 1 - t
    return 1 - u*u*u
}

// interpolate returns the placement set part-way from a to b.
//
// Panes in only one side animate from a zero-width rect at their own
// edge, so an opened or killed column grows or collapses rather than
// appearing at full size mid-flight. Src and Kind come from the
// destination: a card that is becoming a sliver should start drawing
// chrome immediately rather than switching at the end.
func interpolate(from, to []protocol.PlacementData, t float64) []protocol.PlacementData
```

`Draw` loses the `layerWipe` branch entirely. Instead, after the help check:

```go
st := c.frameStateLocked()
if c.motion != nil {
    st.placements = c.motion.at()
    if c.motion.step++; c.motion.step >= c.motion.total {
        c.motion = nil
    }
}
focusedPlacement := c.composeFrameLocked(scr, st, drawPane)
```

The cursor is hidden while `c.motion != nil` — its position is meaningless
while the pane it belongs to is sliding, which is the one thing the wipe got
right.

**Tests:**

- `TestMotionInterpolatesTowardTheTarget` — at `t=0` the rects equal `from`, at
  `t=1` they equal `to`, and at a midpoint each pane's `Dst` lies strictly
  between. Must fail before the change (no `interpolate`).
- `TestMotionSettlesOnTheTargetLayout` — after `motionFrames` draws, the
  composited screen equals a client with no motion in the same state. This is
  the Plan 16 endpoint guard, carried over.
- `TestMotionNeverShowsABlankFrame` — carried over from Plan 16, which is the
  regression it exists to prevent; it must still pass against the new model.
- `TestMotionGrowsAnAddedPaneFromZero` — a pane present only in `to` starts at
  zero width and ends at full width.
- `TestNoMotionWhenPlacementsAreUnchanged` — a redundant snapshot does not
  animate, or every status broadcast would jitter the screen.
- `TestCursorHiddenDuringMotion`.

Carried over from `draw_wipe_test.go`, renamed:

- `TestFocusChangeWipeNeverShowsABlankFrame` → `TestMotionNeverShowsABlankFrame`.
  This is the Plan 16 regression and the most important one to keep: the
  defect it caught was an animation drawing nothing at all.
- `TestFocusChangeWipeEndsOnTheSteadyStateFrame` → `TestMotionSettlesOnTheTargetLayout`.
- `TestResizeDuringPendingWipeSnapsInsteadOfAnimating` and
  `TestResizeDuringActiveWipeDropsIt` collapse into one,
  **`TestResizeCancelsMotion`**. The old pair existed because the wipe had
  two distinct states — recorded but not yet composed, and composed against a
  fixed-size surface — and a resize broke each differently. Interpolation has
  one state, and its rects are in old-viewport coordinates either way, so
  `SendResize` cancels `c.motion` outright and the next frame snaps. One
  state, one test.

**Verification — automated:**
- [x] `go test ./internal/client/ -run TestMotion -v` **fails** before the change — **build failure: `undefined: interpolate`**
- [x] same passes after — **all 8 motion tests PASS**
- [x] `grep -rn "WipeTransition\|activeWipe\|pendingWipe" --include='*.go' .` returns nothing — **clean; `wipe.go` and `wipe_test.go` deleted**
- [x] `make test` passes — **all 11 packages ok**
- [x] `make race` passes — **11/11 under `-race -count=1`**
- [x] `make check` passes — **25 smoke / golden ok / 7 attach-check, `case_card_layout_toggles` still round-trips**

**Verification — manual:**
- [x] pty probe: bytes across the transition, measured against the known baselines. — **card mode 4,664 bytes, growing every slice (1523 / 2773 / 3968 / 4513 / …). Against Plan 16's baselines — snap ≈ 1,214, wipe ≈ 3,712 — that is ~3.8x a snap and ~1.26x the wipe.**
- [ ] `WIDEBOI_LAYOUT=cards make run`, four panes, `C-b l` / `C-b h`: panes visibly slide and resize; the fan re-deals. **Les's eye — this is the part a test cannot settle.**

### Motion is far cheaper than the roadmap assumed

`BEYOND-V1` §1 estimates real motion at ~20x a snap and treats that as the
reason to prefer a wipe. Measured, it is **~3.8x** — about the same as the
wipe it replaces.

The estimate assumed an N-frame animation costs N full repaints. It does not:
the renderer diffs, and a sliding pane changes far fewer cells per frame than
a whole screen. That removes the main argument recorded against motion, and
`motionFrames` did not need cutting.

Scroll mode measured **694 bytes** for the same keypress — because two panes
that both fit keep identical placements when focus moves between them, so
nothing animates at all. The wipe fired on every focus change regardless.
`TestNoMotionWhenFocusMovesButGeometryDoesNot` pins that.

### Three test-fixture findings

- **The obvious motion fixture animates nothing**, and correctly so. Two
  25-cell panes in a 60-cell viewport are both fully visible, so a focus
  change moves no placement. `newMotionClient` uses card mode with panes
  wider than their share instead.
- **`TestLayerLockedPrecedence` lost its subject.** `drawLayer` collapses from
  three states to two: the wipe needed its own because it took over the screen
  and drew from snapshots, while motion feeds interpolated rects through the
  ordinary pane path. The precedence that still matters — help outranks a
  running animation — moved to `TestHelpOutranksMotion`, asserted through
  `Draw`, and additionally checks the animation does not advance unseen
  behind the overlay.
- **Two resize tests collapsed into one**, as planned: the wipe had two
  states a resize could break, interpolation has one.

---

## Phase 3: Docs

Docs only.

**Files:**
- Modify: `docs/BEYOND-V1.md` — §1 and §2.

**Key changes:**

1. §1 stops describing the directional column wipe as the shipped animation.
   Record that it was replaced because splicing two layouts is not motion, and
   that eased placement interpolation is now what runs — the first cut of the
   spring design, minus springs and retargeting, both still parked.
2. Update §1's cost table framing with the measured interpolation cost beside
   the existing snap and wipe numbers.
3. §2 replaces the fixed-sliver description with the share model, records the
   full-render cap and `MinSliverWidth`, and corrects the fan-size estimate —
   it is no longer a fixed count, it is however many clear the floor.
4. Record what stayed out: springs, retargeting, sliver-row scrolling,
   z-overlap.

**Verification — automated:**
- [x] `make check` passes — **25 smoke / golden ok / 7 attach-check**
- [x] `grep -n "DefaultSliverWidth\|10-cell sliver" docs/BEYOND-V1.md` returns nothing stale — **one hit remains and is deliberate: the new §2 text explains what `DefaultSliverWidth` used to decide, to contrast it with a floor**

**Verification — manual:**
- [x] Read §1 and §2 end to end; both describe what the code now does. — **§1 retitled "Animation: motion, as of Plan 18"; §2 "shipped in Plan 17, proportioned in Plan 18".**

**Beyond the plan:** §1's "why the wipe was the cheaper first cut" section now
opens with a warning to read its own numbers against Plan 18's measurement.
Its whole argument is that motion costs ~20x a snap and a wipe ~2x, which made
the wipe look like most of the benefit for a tenth of the cost. Measured,
motion is ~3.8x — so the real gap is about 1.2x, and the wipe's advantage was
very nearly all of its justification. Left the analysis in place rather than
deleting it, since the byte table is still useful, but a reader reaching for
it to justify a future decision needs to know its premise did not hold.

---

## Spec coverage check

| Spec requirement | Phase |
| --- | --- |
| Fan spans the full viewport width | 1 |
| Even share of the remainder, exact total | 1 |
| Share ≥ pane width renders full | 1 |
| Minimum sliver width, overflow to `+N` | 1 |
| No dividers between cards | 1 |
| Focus change animates panes sliding | 2 |
| `wipe.go` is gone | 2 |
| Added/removed panes grow and collapse | 2 |
| Restart-from-current on re-trigger | 2 |
| Docs match the code | 3 |
