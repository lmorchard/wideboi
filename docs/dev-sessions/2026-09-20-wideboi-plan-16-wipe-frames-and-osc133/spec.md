# Plan 16 — Two dead code paths: wipe frames and OSC 133

**Goal:** Make the focus-change wipe and OSC 133 agent status actually do the
thing they were written to do, and add the one test seam whose absence let both
ship broken.

**Source:** user request, 2026-09-20 — "resume development… there's a bug in the
OSC 133 handler."

**Prior work already on this branch:** a `docs/BEYOND-V1.md` reconciliation
pass (uncommitted at spec time) correcting §1, §2 and §7 against what Plans 7,
8, 9 and 12 actually shipped.

## Current state

Two features are fully implemented, unit-tested, green, and non-functional in
production. Both fail the same way: a mechanism proven in isolation against
inputs a test supplied, wired to a call site that supplies nothing.

**The wipe.** `client.go:97-98` allocates `fA`/`fB` with `compose.NewSurface`
and never writes to either, so `WipeTransition` interpolates blank against
blank. Measured on a real pty (`research.md`): after `C-b l`, one erase
sequence goes out, then 18→48 bytes over 96 ms with no repaint, then 973 bytes
at t+128 ms. Eight frames at 16 ms of empty screen, cursor hidden, on every
focus change. `wipe_test.go` passes because it writes `"AAA…"`/`"BBB…"` into
its own frames first.

**OSC 133.** `grid.go:180-196` matches `strings.HasPrefix(s, "A")` against a
payload that is actually `"133;A"`. Verified by probing the pinned emulator
directly, and rooted in `ansi.Parser.parseStringCmd`
(`ansi@v0.11.8/parser.go:242-259`), which parses the leading digits into
`p.cmd` *without removing them from* `p.data`. Every one of vt's own OSC
handlers splits on `;` and reads `parts[1]`. Ours does not, so no `!`, `✓` or
`✗` has ever rendered and `VerbSmartJump` has never had a target. `sawOSC133`
latches before the dead switch, which also disables the `Write` activity
fallback.

**The gap that allowed both.** No test anywhere calls `Client.Draw` — confirmed
by repo-wide grep. `internal/client` tests exercise `statusLineLocked`,
`drawHelpOverlay`, `layerLocked` and `WipeTransition.Draw` individually; the
function that assembles them is untested. `internal/server/term` has zero OSC
tests.

## Desired end state

- A focus change renders a visible directional wipe between the real
  before-frame and the real after-frame. No blank interval.
- A child emitting OSC 133 drives the pane's status glyph, and `$mod`
  smart-jump lands on the pane that most needs attention.
- `Client.Draw` is reachable from a Go test, and a test exists that fails if
  either frame of a wipe goes blank again.
- A detached client's socket connection is closed rather than dropped.
- `docs/BEYOND-V1.md` reflects reality: the reconciliation already on this
  branch, plus §6 rows for the analogues listed under "What we're NOT doing",
  plus the two fixed defects moved out of the parked-defect table.

## Design decisions

- **Decision: compose the wipe's frames from retained layout state, on the draw
  path.** `HandleServerMsg` records `prevPlacements`/`prevFocusPaneID`/
  `prevPaneStatuses` and sets a pending-wipe direction. The next `Draw`
  composes A from the retained state and B from the current state, then builds
  the `WipeTransition`.
  - **Why:** the trigger is on the message path but pane content is only
    reachable inside `Draw` — via the `drawPane` callback in-process, or
    `c.mirrors` when attached (`client.go:252-256`). Recomposing costs two
    extra composites per focus change and nothing in steady state, and it
    works identically for both content paths.
  - **Rejected:** retaining the last composed frame every tick. Correct, and
    frame A would be exactly what the user last saw, but it adds a full-screen
    buffer and blit to *every* frame to serve a transition that fires only on
    focus change — against the frame-budget concern in `BEYOND-V1.md` §1.
  - **Rejected:** deleting the wipe. Instant snap would beat today's blank, but
    Plan 9 built this deliberately and the fix is small once the seam exists.

- **Decision: extract `composeFrameLocked(dst, state, drawPane)` from `Draw`.**
  `state` carries placements, focus pane ID and status glyphs. `Draw`'s
  ordinary path calls it with current state; the wipe path calls it twice.
  - **Why:** this is the same seam the regression test needs. Fixing the bug
    and making the bug testable are the same refactor, which is the main reason
    to prefer it over the retained-frame approach.
  - **Note:** the composed frame covers rows `0..rows-2` — pane headers, pane
    content and dividers. It excludes the status bar and the cursor. See the
    revised open question below; this was changed during `plan`.

- **Decision: OSC 133 parses fields, and `A`/`B` both mean `NeedsInput`.**
  Split the payload on `;`, discard `parts[0]` (the `133`), switch on
  `parts[1]`: `A`,`B` → `StatusNeedsInput`; `C` → `StatusWorking`; `D` →
  `StatusDone` unless `parts[2]` is present and not `"0"`, which is
  `StatusFailed`.
  - **Why:** a shell emits `A` then `B` on every prompt, so the old
    `B` → `Working` clobbered `A` microseconds later and an idle shell read as
    busy. Field splitting also fixes `D;0;aid=1`, which `HasSuffix(s, ";0")`
    misread as a failure. The handler has never run, so there is no behaviour
    to preserve.
  - **Rejected:** prefix-strip only, keeping `B` → `Working`. Smallest diff,
    but leaves `!` effectively unobservable.

- **Decision: `sawOSC133` latches only on a recognised command.** An
  unparseable `133` payload returns `false` (letting vt log it as unhandled)
  without latching.
  - **Why:** the latch gates the `Write` activity fallback and the `Status`
    idle timeout. One malformed sequence should not permanently disable them.

- **Decision: `VerbSmartJump` picks by priority, not first match.**
  `StatusFailed` > `StatusDone` > `StatusNeedsInput`; `Working` and `Idle` are
  never targets; ties break by ascending pane ID.
  - **Why:** once `A`/`B` mean `NeedsInput`, nearly every shell is `NeedsInput`
    nearly always, so first-match would land arbitrarily. `s.panes` is a map,
    so "first" is not even stable today.

- **Decision: `removeTransportLocked` closes the transport it drops**, via an
  `io.Closer` type assertion, with the actual `Close` called after `s.mu` is
  released.
  - **Why:** `Close` is not on the `Transport` interface and is called only
    from tests, so every detach leaks an fd and a reader goroutine on a
    long-lived server. Closing under `s.mu` would add a new way to hold that
    lock across a blocking call, which `BEYOND-V1.md` §6 already flags as the
    shape behind the unkillable-server chain.

## Patterns to follow

- Status derivation and the atomic status word: `internal/server/term/grid.go:203-222`.
- How vt itself parses an OSC payload: `x/vt/osc.go:21-25` (`bytes.Split`, read
  `parts[1]`). Mirror it.
- Draw-path layering and the existing early returns: `internal/client/client.go:189-216`.
- Mirror-vs-callback content selection to preserve in `composeFrameLocked`:
  `internal/client/client.go:252-256`.
- Lock-release discipline before a potentially blocking call:
  `resizePanesLocked`/`PaneSize`/`CursorInfo` in `internal/server/server.go`.
- Smoke assertions must go through `focus_pane_id` (`scripts/smoke.py:54`), not
  by echoing text into a pane and grepping for it — see the deleted-case
  comment at `scripts/smoke.py:339-346`.

## Test plan

1. **`internal/client`** — the seam that was missing. Drive `Client.Draw`
   across a focus change and assert no transition frame is blank, and that the
   final frame matches the steady-state render. This test must fail against
   today's code.
2. **`internal/server/term`** — new OSC tests, the package's first. Table-drive
   `A`, `B`, `C`, `D`, `D;0`, `D;1`, `D;0;aid=1`, `A;cl=m`, and a malformed
   payload that must not latch `sawOSC133`.
3. **`internal/server`** — smart-jump priority, including the tie-break and the
   never-target statuses.
4. **`scripts/smoke.py`** — restore an OSC 133 case that writes the escape
   sequences into panes and asserts smart-jump's landing through
   `focus_pane_id`.

Wire-level assertion of the wipe is deliberately excluded: after ~128 ms the
screen repaints either way, so distinguishing fixed from broken on the wire
needs timing, which is flaky. The Go-level test distinguishes them directly.

## What we're NOT doing

- **Retargeting a wipe in flight.** A focus change during an active wipe
  restarts it from the state immediately before the newest change. True
  retargeting ("snapshot the live screen as the new A") stays in `BEYOND-V1.md` §1.
- **The wide-glyph-at-the-split hazard.** `WipeTransition.Draw` cuts at a hard
  column index; whether a double-width glyph straddling it survives is
  untested. It becomes reachable for the first time with this fix. Record it,
  don't chase it.
- **Springs, motion, cards.** No `SetStrategy` caller, no `CardStrategy` verb.
- **The other dead code.** `Pane.Dead`, `Pane.SendText`, `SocketListener.Path`
  and `Server.SpawnPane` have no callers at all; `Client.FocusPaneID` is
  test-only. Record in `BEYOND-V1.md` §6, fix nothing.
- **Reworking `PaneStatus` into a wire enum.** Only the rendered glyph crosses
  the socket (`messages.go:99`). Leave it.
- **Changing the `Write`-based activity heuristic or its 3-second idle
  timeout**, beyond making the latch conditional.

## Open questions

- **Should the status bar participate in the wipe, or stay live?**
  **Resolved during `plan`: it stays live.** Brainstorm's default was that it
  participates. Writing the plan changed that for two reasons. Mechanically,
  `statusLineLocked` reads `c.focusPaneID`/`c.placements`/`c.paneStatuses`
  directly and has seven existing test call sites, so parameterising it to
  compose a frame's bar would churn tests unrelated to this fix. Behaviourally,
  a status bar that dissolves reads as a glitch, and showing the new focus
  immediately is better feedback than animating it. So a composed frame is
  rows `0..rows-2` and `Draw` paints row `rows-1` itself in both branches,
  with the `WipeTransition` built at height `rows-1` so its blits never reach
  the bar.
- **Does a resize landing between the focus change and the next draw tick
  invalidate the retained frame?** Default: yes — if `c.cols`/`c.rows` differ
  from when the state was retained, skip the wipe and snap. Cheap guard,
  avoids compositing against a stale geometry.
