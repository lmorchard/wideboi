
# Host cursor stays in the focused pane Spec

**Goal:** At the end of every frame, the host terminal cursor sits in the focused pane, not in a background card that happens to have been redrawn.

## Problem

With several cards producing output at once, the cursor visibly jumps around the screen. It lands in whichever card was redrawn most recently, stays there for a frame, and moves on. The busier the screen, the worse it gets.

## Current state

**The client logic is already correct.** `internal/client/client.go:421-445` shows only the focused pane's cursor (clamped to its placement), and hides it for control mode, help, animation, and no-focus. Other cards never ask for a cursor. So the flicker isn't a logic bug in what we *request*.

**The flicker comes from how ultraviolet orders the bytes.** This is the known quirk in `docs/LESSONS.md:31`:

- `uv.TerminalScreen.Flush` positions the cursor with `s.rend.MoveTo(...)`. That writes to the *renderer's* buffer (`rend.buf`), not the screen's output buffer (`s.buf`).
- `rend.buf` is only drained into `s.buf` by the next `TerminalScreen.Render()` (which calls `s.rend.Flush()`).
- `Flush` then writes `s.buf` wrapped in `HideCursor … ShowCursor` (when sync updates are off and the cursor is visible).

So each frame reaches the terminal as:

```
[hide] [MoveTo: previous frame's cursor spot] [cells for whichever card changed] [show]
                                                                       ^ cursor shown here
```

The terminal shows the cursor wherever the last changed cell was written, usually inside a background card. The move to the focused pane is held back until the next frame.

**Why the current mitigation doesn't cover this.** `cmd/wideboi/main.go:350` (attached client) and `:486` (standalone) use `dirtyFrames = 2` to run one extra `Render`/`Flush` after a changed frame, which releases the held-back `MoveTo`. That works once output stops. But while cards keep updating, every frame adds new cell writes that end somewhere else, so the cursor is out of place on every busy frame.

Vendored source checked: `github.com/charmbracelet/ultraviolet@v0.0.0-20260910203606-6c9e17dc7a16`, `terminal_screen.go` `Render` (~L230) and `Flush` (~L253), and `terminal_renderer.go` `Flush` (~L1348) / `MoveTo` (~L1581).

## Desired end state

- After each frame write, the last cursor-positioning sequence the terminal receives targets the focused pane's cursor (or the cursor is hidden, in the cases where the client hides it).
- Holds while several background panes produce output continuously.
- Identical behaviour in standalone and attached (`wideboi attach`) modes.

## Design decisions

- **Decision:** Present each frame as `Render(); Flush(); Render(); Flush()`, in one small helper used by both frame loops (`cmd/wideboi/main.go:350-355` and `:486-491`).
  - **Why:** The second `Render()` drains the held-back `MoveTo` from `rend.buf` into `s.buf` (the render buffer hasn't changed, so no cells are re-emitted), and the second `Flush()` sends it wrapped in hide/show. The cursor then ends every frame in the right place. The two writes go out back-to-back, a far shorter gap than the one-frame displacement we have now. It works within the upstream API and doesn't fork anything, per the `term.Grid` / "fix upstream gaps behind a narrow seam" guidance in `docs/LESSONS.md`.
  - **Rejected: synchronized output (mode 2026) alone** (`scr.SetSynchronizedUpdates(true)`). It makes each `Flush` atomic but still leaves the cursor wherever the last cell was written; the `MoveTo` is still held back a frame. It also turns off the hide/show wrapping. Could be layered on later, but it doesn't fix this bug.
  - **Rejected: patching or forking ultraviolet** to issue the `MoveTo` into `s.buf`. It's pre-1.0 with no public cursor setter (`docs/LESSONS.md:30`), and a fork is a bigger maintenance cost than the bug justifies.

## Verification

- Add a pty-harness check (alongside `scripts/smoke.py` / `scripts/ptylib.py`) with at least two panes producing continuous output and one focused pane sitting at a prompt. Parse the host output stream per frame and assert that the last cursor position before each show-cursor lands inside the focused pane's placement.
- **Prove it fails on current `main`** for the reason above before trusting it green.
- It's timing-sensitive, so run it four times green after the fix (per `CLAUDE.md`).
- `make check` passes.

## What we're NOT doing

- Removing or reworking `dirtyFrames`. It may become unnecessary once the held-back `MoveTo` is drained in the same frame, but that's a separate change with its own risk (a missed follow-up tick is how the LESSONS entry came about).
- Enabling synchronized output (mode 2026).
- Changing which pane's cursor is shown, or cursor style/shape.
- Touching the teardown `Flush` calls (`cmd/wideboi/main.go:271`, `:379`, `:407`); those run once at exit and don't flicker.
- Any upstream PR to ultraviolet (worth considering separately).

## Open questions

- *Does the brief hide→show→hide→show between the two back-to-back writes cause any visible cursor blink on real terminals (Ghostty, iTerm2, Terminal.app)?* Default assumption: no. Terminals repaint on their own schedule and both writes land well inside one repaint. Check by eye on at least one terminal before merging; if it does blink, wrapping the pair in mode 2026 is the fallback.

---

