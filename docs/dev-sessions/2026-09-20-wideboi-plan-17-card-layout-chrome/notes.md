# Plan 17 — notes

`CardStrategy` had been complete, property-tested and unreachable since Plan 8.
This makes it selectable and gives its slivers something worth showing.

## What landed

| Phase | What |
| --- | --- |
| 1 | `PlacementKind` — a sliver is distinguishable from a clipped pane |
| 2 | Layout mode as shared session state; `WIDEBOI_LAYOUT=cards` |
| 3 | `$mod c` toggle |
| 4 | Terminal title captured and carried to the client |
| 5 | `compose.TruncateWidth`; a stale comment corrected |
| 6 | Sliver chrome: glyph, title, activity spine |
| 7 | `+N` marker for cards that do not fit |
| 8 | `BEYOND-V1` §2/§6 and `LESSONS` made true |

Plus a ninth commit restoring content the Plan 16 merge silently dropped — see
below.

## The load-bearing design decision

**A card sliver and a viewport-clipped pane are identical on the wire.** Both
are a narrow `Dst` over a cropped `Src`, and `Z` doesn't separate them either
because `ScrollStrategy` emits `Z=0` for everything. Without
`protocol.PlacementKind` the client would paint chrome over the visible edge of
a legitimately clipped pane — the exact thing
`case_partly_clipped_pane_keeps_full_width` exists to prevent.
`TestClippedPaneIsNotDrawnAsChrome` is the guard.

## Four things the plan got wrong

Worth reading before the next plan, because three of four were *the plan
trusting prose over code*.

1. **The bar-budget fix.** The plan worked out correctly that the attached
   status bar sits at exactly 77 of 79 cells, then concluded that placing
   `c cards` last would make it the entry that drops. Two Go tests said
   otherwise: one asserts *every* `BarGroup` is present at 80 columns, the
   other asserted every binding *must have* a `BarGroup`. Together, no new
   binding could satisfy both at any label length. Resolved with Les by making
   `BarGroup` optional and `Long` mandatory — which is what the first test's
   own comment already prescribed.

2. **`WriteStyledWidth` was never needed.** The plan budgeted a width-aware
   writer because `WriteString`'s doc comment said width was ignored. It
   wasn't — `WriteStyled` has advanced by `cell.Width` all along. The comment
   was fiction, and `BEYOND-V1` §6 carried a defect row sourced from it. Only
   `TruncateWidth` was real. Written up in `LESSONS.md`.

3. **`ScrollStrategy` drops columns too.** The plan's
   `TestScrollModeNeverShowsAMarker` was justified by "scroll clips rather than
   drops, so every column has a placement." False — it skips any column whose
   `Dst` is empty. The default layout has the same silent-invisible-pane
   problem cards had. The marker is gated to card mode rather than changing
   the default chrome for everyone; recorded in §2.

4. **`ctrl+c` had to stay unbound.** Binding `c` gave it a repeat form and took
   away the unknown-key-exits-control-mode escape hatch. Added `NoRepeat`;
   repeat is meaningless for a toggle anyway.

## A test that passed against the bug

`TestSliverTitleIsTruncatedByWidthNotRunes` originally watched the **leftmost**
sliver and passed even with deliberately broken rune-count truncation.
`composeFrameLocked` draws left slivers → focused card → right slivers, so the
focused card painted over the spill before the test could see it. Retargeted at
the rightmost sliver, which nothing is drawn after, it fails correctly.

Generalised into `LESSONS.md` as a compositing-specific corollary to Plan 16's
red-step rule: **when asserting something does not escape its bounds, assert it
where nothing else can tidy up.**

## The motivating case does not work yet — say so

Sliver chrome renders correctly; a title set by hand shows up in a sliver on a
real pty. **But Claude Code sets no title inside a wideboi pane**, so its
sliver shows only a spine. That undercuts the argument I used to justify the
whole title-plumbing phase, and it should not be soft-pedalled.

Ruled out: the alternate screen, `TERM`, and being a shell's child (Claude Code
under `/bin/sh` in a plain pty still emits one title). Remaining suspect,
unverified: a capability probe the emulator does not answer. Recorded in §8.

**`OSC 9;4` is probably the better signal anyway** — Claude Code emits
`ESC]9;4;3;` on turn start and `ESC]9;4;0;` on turn end, reliably, and it maps
onto `PaneStatus` almost directly. That is the next thing to build if agent
panes are the point.

## The Plan 16 merge dropped a commit

Plan 16's final docs-only commit was force-pushed after review; the merge
landed the **pre-force-push** head. Pushed tip `013218e` had the §8 content,
merged `main` did not, every signal was green, and it surfaced a session later
only because a grep came back empty. Restored here.

`--force-with-lease` does not protect against this: it guards against
overwriting commits you have not fetched, not against a reviewer merging a head
you have since replaced. Written up in `LESSONS.md`. **Habit to adopt: once a
PR is handed over, stop force-pushing — and after a merge, verify the merged
tree, not just that the branch merged.**

## What the Copilot review caught

Three comments, all real, none skipped. Two were multi-client or
edge-of-lifecycle cases the plan never considered.

1. **A stale strip on the empty snapshot.** When the last pane closes the
   server broadcasts a snapshot with no columns, and the client's mode
   application and strip sync both lived inside the `len(m.Columns) > 0`
   branch. So the strip kept its old columns, `hiddenCountsLocked` counted
   panes that no longer existed, and card mode drew `+2` for them. A mode
   change arriving while the session was empty was dropped too. Both hoisted
   out of the branch.
2. **`TruncateWidth` and `WriteStyled` disagreed about zero-width runes.**
   Truncation charged a combining mark 0 cells; the writer advances at least
   one column per rune. A title with combining marks passed the budget check
   and still overflowed. They share one contract now — truncation charges
   exactly what the writer advances.
3. **`delivered` meant "any client", not "every client".** With two clients
   attached and one buffer full, the glyph and title sets were marked clean
   and the client that missed the edge-triggered broadcast would never see
   that change again. Now every attached client has to accept before the set
   goes clean, so a wedged client causes retries rather than silent
   divergence.

All three verified to fail first. The messages are worth keeping: *"marker
survived an empty snapshot, counting panes that no longer exist"*, *"truncated
to \"ééabcd\" for a 6-cell budget, but it wrote into column 6"*, and *"marked
delivered while one attached client never received it"*.

Pattern worth noticing across both this plan and Plan 16: **the reviewer's
findings clustered in multi-client and teardown paths** — exactly the states
my own tests build fixtures for least often.

## Where to pick up

- **`OSC 9;4` support**, feeding the same `PaneStatus`. Needs a precedence rule
  against OSC 133; note `sawOSC133`'s latch already disables the write
  heuristic, so a second authoritative source needs the same treatment rather
  than a second latch.
- **Why Claude Code sets no title in a pane.** One focused pass; it is the
  difference between the fan being useful and being decorative.
- **Scrolling the row of slivers.** Still unbuilt; `+N` only marks the problem.
- **Genuine z-overlap.** `CardStrategy` still emits non-overlapping rects, so
  "panes that slip under each other" remains the name rather than the
  behaviour.
