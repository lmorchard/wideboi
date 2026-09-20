# Research — Plan 16

Findings gathered before brainstorm, all verified against the code at
`b21eb5f` (Plan 15) on 2026-09-20. Facts and file references only; the
decisions they point at belong in `spec.md`.

## Defect 1 — the focus-change wipe interpolates two blank frames

`internal/client/client.go:92-99`, inside `HandleServerMsg`'s
`MsgLayoutSnapshot` branch:

```go
if oldFocus != 0 && c.focusPaneID != oldFocus {
        dir := WipeLeftToRight
        if c.focusPaneID < oldFocus {
                dir = WipeRightToLeft
        }
        fA := compose.NewSurface(c.cols, c.rows)
        fB := compose.NewSurface(c.cols, c.rows)
        c.activeWipe = NewWipeTransition(fA, fB, c.cols, c.rows, dir, 8)
}
```

`compose.NewSurface` (`internal/client/compose/surface.go:21`) returns
`uv.NewScreenBuffer(cols, rows)` — blank. Neither `fA` nor `fB` is ever
written to. Grepping `NewSurface` across non-test code gives four call
sites: these two, and three pane-mirror allocations at `client.go:118`,
`125` and `142` that *are* subsequently filled by pane updates.

Consequences, traced:

- `Client.Draw` (`client.go:205`) dispatches on `layerLocked()`. While
  `activeWipe.Active()` the `layerWipe` branch calls `activeWipe.Draw(scr)`,
  `scr.HideCursor()`, then `Step()`, and **returns** — so no pane, header,
  divider or status bar is drawn for the duration.
- `WipeTransition.Draw` blits `frameB` on one side of a moving split and
  `frameA` on the other. Both blank, so the whole screen is blank.
- Both render loops in `cmd/wideboi/main.go` (`:152` in-process, `:294`
  attach) tick a `time.NewTicker(16 * time.Millisecond)`, and the wipe is
  constructed with `totalSteps = 8`. 8 × 16 ms ≈ **128 ms of blank screen,
  cursor hidden, on every focus change.**

Why the tests miss it: `internal/client/wipe_test.go:11-14` and `:49-52`
build their own frames and `compose.WriteString` `"AAA…"` / `"BBB…"` into
them before constructing the transition. They test `WipeTransition` in
isolation and pass. Nothing tests the construction site.
`scripts/smoke.py` and `scripts/golden.py` do not cover it either.

Related, from `docs/LESSONS.md`: "A smoke test that types a command and
then greps for its own text proves nothing." Same class, one layer up.

### Open sub-question: where do real frames come from?

Pane content reaches `Client.Draw` by one of two routes, chosen at
`client.go:252-256`:

- **in-process:** the `drawPane func(id int, dst uv.Screen, area image.Rectangle)`
  parameter, passed as `srv.DrawPane` from `main.go:343`.
- **attach:** `drawPane` is `nil` (`main.go:204`) and `Draw` blits
  `c.mirrors[id].Surface` instead.

Either way the content is only reachable *inside* `Draw`. `HandleServerMsg`,
where the wipe is currently constructed, runs on the message path and has
no access to `drawPane` at all — so it cannot compose a frame even in
principle. That is the real design question for the spec: **the trigger is
on the message path, but frame capture has to be on the draw path**, and
frame A has to be captured *before* the snapshot overwrites focus and
placements.

One shape that resolves it without moving the trigger: have `Draw` always
compose into an offscreen `Surface` and blit that to `scr`, retaining it as
"last composed frame". Then A is simply the retained frame from the tick
before the focus change, and B is composed normally on the next tick.
Costs one full-screen buffer and one extra blit per frame. Noted as a
candidate, not a decision.

### Second hazard, never reachable until now

`docs/BEYOND-V1.md` §1 names "wide glyphs must be atomic in the change
set." `WipeTransition.Draw` cuts at a hard column index
(`revealCol := (wt.cols * wt.step) / wt.totalSteps`) and hands each side to
`compose.Blit`, which delegates to `src.Draw(dst, rect)`. Whether a
double-width glyph straddling the split survives has never been tested and
could not have mattered while the frames were blank. The sibling hazard,
"hide the cursor for the duration", is done.

## Defect 2 — OSC 133 has never matched

`internal/server/term/grid.go:180-196`.

**Root cause, verified empirically rather than by reading.** A handler
registered via `RegisterOscHandler(133, …)` against the pinned
`x/vt v0.0.0-20260913004009-c615ff2f7805`, fed
`"\x1b]133;A\x07\x1b]133;D;1\x07"`, receives:

```
["133;A" "133;D;1"]
```

The payload carries the command prefix. This matches the library's own
convention — `osc.go:21 handleTitle` does `bytes.Split(data, []byte{';'})`
and reads `parts[1]`, never `parts[0]`. So every `strings.HasPrefix(s, "A")`
and sibling in our handler is dead.

`g.sawOSC133.Store(true)` runs at the top of the handler, *before* the dead
switch. That latch also gates the fallback heuristic in `Write`
(`grid.go:207-210`) and the idle timeout in `Status` (`grid.go:214-221`), so
the first OSC 133 a child emits permanently freezes `Status()` at whatever
it held and disables the activity fallback.

Downstream consumers, both currently unreachable for the OSC-derived states:

- `Server.broadcastLayout` (`server.go:396`) maps `p.Status().Glyph()` into
  `MsgLayoutSnapshot.PaneStatuses`.
- `Client.Draw` renders it in the pane header (`client.go:230-233`) and the
  status bar (`client.go:373-375`).
- `VerbSmartJump` (`server.go:183-189`) scans for `StatusNeedsInput` or
  `StatusFailed`.

Note `»` (`StatusWorking`) *does* render today, via the `Write` fallback, so
the glyph pipeline is partly proven. `!`, `✓` and `✗` have never appeared.

### Three things the prefix strip alone does not fix

1. **A/B/C semantics.** A shell emits `A` (prompt start) then `B` (prompt
   end) back to back on every prompt. The current mapping sends `B` to
   `StatusWorking`, so it clobbers `A`'s `StatusNeedsInput` microseconds
   later and an idle shell reads as busy. **Decided with Les 2026-09-20:**
   `A`/`B` → `StatusNeedsInput`, `C` → `StatusWorking`, `D` and `D;0` →
   `StatusDone`, `D;<nonzero>` → `StatusFailed`.
2. **Payload parameters.** OSC 133 permits `D;0;aid=1` and `A;cl=m`. The
   current `strings.Contains(s, ";") && !strings.HasSuffix(s, ";0")` test
   reads `D;0;aid=1` as a failure. Needs field splitting, not prefix and
   suffix matching.
3. **The latch fires on unrecognised payloads.** `sawOSC133` should only
   latch on a *recognised* command, or one malformed `133` sequence
   permanently disables the `Write` activity heuristic.

### Knock-on to settle in the same change

With `A`/`B` meaning `StatusNeedsInput`, **every idle shell becomes a
`VerbSmartJump` target**, because a shell sits at a prompt almost all the
time. A key that means "take me to what needs me" should not land on an
idle shell. The filter at `server.go:185` needs narrowing; the options are
`StatusFailed`-only, `StatusFailed + StatusDone`, or a notion of "changed
since I last looked."

### Test coverage that does not exist

`internal/server/term` has **zero** OSC tests — `grid_test.go` covers
rendering, resize, cursor, SGR and key encoding only. `scripts/smoke.py`
deliberately has no OSC 133 case: the comment above
`case_quit_restores_and_reaps` (`smoke.py:339-346`) records that the
previous case passed for the feature's entire broken life by echoing a
command into a pane and grepping for its own text, and was deleted rather
than patched. A restored case must assert through `focus_pane_id`.

A prior implementer reported that the prefix strip alone makes the smoke
suite pass 24/24. That predates the semantics change decided above, so it
is a starting point, not a target.

## The shared root cause

Both defects are a unit-tested mechanism that nothing correctly feeds.
`WipeTransition` is proven against frames the test wrote; the OSC 133
switch is proven against nothing at all. In both cases the seam between
"the mechanism" and "the thing that drives it" is untested, and in both
cases the whole suite stayed green. Coverage that would have caught either
one is the part of this work worth doing once rather than twice.

## Baseline

`make test` green in this worktree before any change; `internal/server`
~16 s and `internal/server/ptyx` ~11.5 s dominate. `make check` adds
`fmt-check`, `lint`, `seam-check`, `race`, `verify-exit` (~18 s), `smoke`
and `attach-check` (~60 s) — not yet run for this session.
