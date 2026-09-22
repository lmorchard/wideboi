# Notes: mouse (#34) and OSC 52 clipboard (#33)

## State at end of execute

All five phases committed on `issue-34-33-mouse-clipboard`. Automated gates green:
`make check` fully green 4× after Phase 4, and once more after Phase 5. **Manual checks
are all still pending.** Les asked to batch them at the end; see the "Verification —
manual" boxes in `plan.md`.

**Before `pr`:** `origin/main` moved after this branch was cut: #73 (off-screen
markers in scroll mode, sticky card window) and #76 (OSC 9;4 progress). #73 touches
`internal/client`, so expect conflicts in `client.go` and possibly the smoke suite.
Rebase, then rerun `make check` 4×.

## Decisions made during execute (not in the spec)

- **Non-left buttons in a focused tracking pane are forwarded** (right-click in vim,
  middle-click). The spec said "press, release, drag"; it didn't restrict buttons.
  In non-tracking panes, non-left presses only clear the selection.
- **The wheel goes to any tracking child under the pointer, focused or not.** Spec
  item 3 read that way; item 5 (forwarding is focused-only) covers presses.
- **`mouse = false` has no env var or CLI flag.** Config file only, which keeps the
  change small.

## Findings

- **`PlacementSliver` is no longer emitted by any strategy.** Since #65 made cards
  genuinely overlap, `layout` never sets `Kind` to sliver, so the renderer's
  `drawSliverLocked` path and the mouse's sliver guard only run on hand-built
  placements. The wheel test injects one. Recorded in project memory, not fixed.
- **The issue text for #34 was out of date:** `composeFrameLocked` already sorted by
  Z. Hit-testing reuses that sort, walked in reverse.
- **`uv.Line.Set` blanks a wide glyph if you write its placeholder cell.** The
  selection highlight steps by `Width` for that reason (LESSONS already covered it;
  this was the first time it applied to highlighting).
- **vt's `SendMouse` blocks on the same pipe `SendKey` does**, so `Pane.SendMouse`
  queues onto the key-writer goroutine.

## Test-honesty log

- Three tests passed vacuously before their implementation. Each was proved by
  breaking its guard: the sliver/header wheel guard, the unfocused-tracking-pane
  guard, and tracking-turned-off.
- **The first clamp test couldn't fail.** The drag ended at `MORE N` and the test
  looked for `NEIGHBOUR`. Tightened to `MORE`.
- **The smoke OSC 52 regex's ST branch was mis-escaped** (it matched ESC plus two
  backslashes). It passed through the BEL branch only. Fixed.
- **The forwarding smoke case first asserted `1b 5b 3c` literally.** The screen shows
  `1b  5b  3c`. The failure was real at the assertion level; forwarding itself
  worked. Switched to a whitespace regex, then re-proved red and green.
- **Stale binary false alarm.** After a smoke red check I restored the source but
  didn't rebuild, and the next four smoke runs failed against the broken binary.
  Added to LESSONS.

## PR self-review fixes (after rebase onto #73/#76)

- **Card-mode selection leaked into the card above.** The selection clamped to the
  lower card's full `Dst`, which runs underneath the higher card, so a drag from
  pane 1's visible strip copied pane 2's text (`┃OP-CARD`). Now clamped to the
  visible strip (`visibleRectLocked`). The selection stores the pane's `Dst`
  separately, so an unchanged status snapshot doesn't clear a card selection.
  The first version of the test passed against the bug: the top card's divider
  overwrites its "T", so asserting on `TOP` saw nothing.
- **A stale grab swallowed the next press.** A forwarded drag whose release was
  lost (for example, while the help overlay was up) left the grab set, and the
  next press was dropped. A press now always clears the grab first.

## Flakes observed (not caused by this branch, as far as I can tell)

- This branch, Phase 3: `unmodified verb exits control mode` failed 1 of 5
  `make check` runs. It's a case with no mouse input.
- Baseline, `origin/main` at f7de897 in a separate worktree:
  `attached client emits no bytes while idle` failed 1 of 6 `make check` runs.
  Some of those runs overlapped with my own test runs, so there was extra load.
- After the rebase, this branch had 2 failures in 8 `make check` runs: `focus switch
  moves the cursor` and `attached client emits no bytes while idle`, the latter the
  same case that flaked on main. Neither involves mouse input.
- Read: the parallel gate has an existing roughly 1-in-6 load-sensitive flake
  rate. Not investigated.

## Housekeeping

- The board transitions (In progress, In review) were skipped because `gh` lacks
  the `project` scope. Fix with `gh auth refresh -s project`.
- This repo's `.git/config` sets `commit.gpgsign=false`, overriding the global
  `true`, so this branch's commits are unsigned. Not changed.
