
# Remove the dirtyFrames follow-up tick Spec

**Goal:** Present a frame only when it changed, now that `present()` no longer leaves a cursor move stranded for the next tick.

## Current state

Both frame loops in `cmd/wideboi/main.go` (attach ~L346-355, standalone ~L481-491) set `dirtyFrames = 2` when `cli.Draw` reports a change, so every changed frame is followed by one extra `present` on the next 16ms tick.

The follow-up existed only to release the `MoveTo` that `uv.TerminalScreen.Flush` leaves in the renderer's buffer until the next `Render()` (`docs/LESSONS.md`, the `MoveTo` bullet). #72 (PR #75) moved that release into `present()` itself (`cmd/wideboi/present.go`): it runs `Render(); Flush()` twice inside one mode-2026 synchronized update. After `present` returns there's nothing left in the renderer's buffer: the second pass's `MoveTo` targets the position the cursor is already at, and a hidden cursor issues no `MoveTo` at all.

So the follow-up tick draws nothing. What it still does is write an empty bracket to the host on the frame after every change: `?25l ?2026h ?25h ?25l ?2026l ?25h`.

## Desired end state

- `if cli.Draw(...) { present(scr) }` in both loops; `dirtyFrames` removed.
- A changed frame produces exactly one synchronized update; an unchanged tick writes nothing.

## Design decisions

- **Decision:** delete the counter outright rather than keep it at 1.
  - **Why:** with the drain happening inside `present`, a counter set to 1 is just a roundabout `if changed`.
  - **Rejected:** having `present` skip the bracket when nothing is pending. `TerminalScreen` doesn't expose its pending-buffer length, and guessing at it is worse than not calling `present`.

## Verification

- `cmd/wideboi/present_test.go` should already cover the cursor. Add an assertion (or a small test) that a `present` on an unchanged screen after a full present leaves the cursor where it is. If a stranded move ever came back, the next frame's replay would show the cursor out of place.
- `make check` four times. The "idle emits no bytes" smoke case must stay green; it currently tolerates the follow-up because it samples after settle.
- Manual: the cursor still lands correctly after a focus switch and after control mode is left (both are cursor-only changes, the case the follow-up was originally added for).

## What we're NOT doing

- Changing the 16ms frame ticker.
- The `copyToHostScreen` `ShowCursor()`-every-changed-frame quirk (see `docs/dev-sessions/2026-09-22-1519-issue-72-cursor-flicker/notes.md`). It's separate, and may be worth its own issue.
- Any change to `present()` itself.

## Open questions

- *Can a cursor-only change (focus switch, entering or leaving control mode) produce a `Draw` that returns false?* Default: no. `offscreenHostScreen.equal` compares cursor shown/position (`internal/client/client.go` ~L301-306), so a cursor change counts as a change. Confirm while implementing.

---

