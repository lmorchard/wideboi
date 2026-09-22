# Dev Session Notes: Issue #52 (Skip Render/Flush When Idle)

## Context & Problem

Issue #52 identified that wideboi emitted ~1,134 bytes/second while completely idle (no keystrokes, no pane output, no animations). This was caused by the unconditional 16ms ticker calling `cli.Draw`, `scr.Render()`, and `scr.Flush()`. In Ultraviolet's `TerminalScreen`, calling `ShowCursor()` stages `\x1b[?25h`, which triggers `Flush()` to bracket the frame with anti-flicker cursor hide/show escape sequences (`\x1b[?25l` ... `\x1b[?25h`) on every single tick.

## Solution

1. **Double-buffered frame staging in `Client.Draw` (`internal/client/client.go`):**
   - `Client` now composes into an internal offscreen screen (`offscreenHostScreen`).
   - Compares the staged frame (cells, dimensions, cursor visibility, cursor position) against `lastRenderedScreen`.
   - Also tracks `lastHostScreen` so callers passing a newly allocated `HostScreen` (common in unit tests) are always drawn.
   - If identical, `scr` is left completely untouched (no cells written, no `ShowCursor`/`HideCursor` called) and `Draw` returns `false`.
   - If changed, cells and cursor state are copied to `scr`, `lastRenderedScreen` is updated, and `Draw` returns `true`.

2. **Gating `scr.Render()` and `scr.Flush()` (`cmd/wideboi/main.go`):**
   - In both `run()` and `runAttach()`, rendering and flushing are gated on `cli.Draw` returning `true`.
   - To account for Ultraviolet's internal renderer pipeline (where `s.rend.MoveTo` in `Flush()` buffers into `s.rend.buf` and is flushed on the subsequent `Render()`), a 2-frame window (`dirtyFrames = 2`) is maintained so the settled cursor position is pushed to the terminal before going completely quiet.

3. **Cleanup of `scripts/ptylib.py`, Smoke Test, & Attach Acceptance Test:**
   - Deleted `_FRAME_NOISE` from `scripts/ptylib.py`. `settle_output` now directly measures `len(drainer.output())`.
   - Added `case_idle_emits_no_bytes` to `scripts/smoke.py` asserting that wideboi emits 0 bytes over an idle sampling window once settled.
   - Per Copilot review feedback: added `case_attached_client_idle_emits_no_bytes` to `scripts/attachcheck.py` to ensure `runAttach` has dedicated regression coverage for idle emission over the socket, and used `settle_output` for baseline settling in both tests.
   - Verified the red step: disabling the render gating caused the smoke test to fail with 558 bytes over 0.5s (~1,116 B/s).

## Key Learnings

- In Ultraviolet (`charmbracelet/ultraviolet`), `TerminalScreen.Flush` invokes `s.rend.MoveTo` to position the visible cursor. However, `s.rend.MoveTo` writes escape sequences to `s.rend.buf`, which is only flushed into the screen's output buffer on the *next* `TerminalScreen.Render()`. Because of this 1-frame latency in Ultraviolet's cursor positioning pipeline, gating render ticks requires one follow-up frame to push the final cursor position to the pty before going idle.
- Added this finding to `docs/LESSONS.md`.

## Verification

- `go test ./internal/client -v -run TestClientDrawDirtyDetection` passes.
- `go test -race -count=1 ./...` passes.
- `python3 scripts/smoke.py` passes (26/26).
- `python3 scripts/attachcheck.py` passes (8/8).
- `make check` passed 4 consecutive runs with 0 failures.
