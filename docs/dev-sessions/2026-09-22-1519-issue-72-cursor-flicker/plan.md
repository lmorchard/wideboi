# Host cursor stays in the focused pane — Implementation Plan

**Goal:** At the end of every presented frame, the host cursor sits at the position the client asked for (the focused pane's cursor), not at the last cell the renderer wrote.

**Approach:** Add a `present(scr)` helper in `cmd/wideboi` that runs `Render(); Flush(); Render(); Flush()`. The second pass drains the `MoveTo` that `TerminalScreen.Flush` leaves stranded in the renderer's buffer. Both frame loops call it in place of their bare `Render(); Flush()`.

**Tech stack:** Go, `charmbracelet/ultraviolet` (`TerminalScreen`), `charmbracelet/x/vt` as the replay emulator in the test.

**Verification deviation from spec:** the spec proposed a pty-harness check asserting on the cursor position before each show-cursor. That can't pass with this fix: the first of the two writes still ends in `?25h` at the last written cell. What changes is that a corrective write follows immediately, and in a pty byte stream there are no write boundaries to tell "end of frame" apart from "between the two writes". So the test lives one level down instead. Drive a real `uv.TerminalScreen` into a buffer, replay each presented frame's bytes through `vt`, and assert the emulator's cursor after each frame. It's deterministic, runs in milliseconds, and asserts exactly "end of every frame".

---

## Phase 1: `present` drains the stranded cursor move

**Files:**
- Create: `cmd/wideboi/present.go`: `present(scr *uv.TerminalScreen)` plus a doc comment pointing at `docs/LESSONS.md`.
- Create: `cmd/wideboi/present_test.go`: the replay test.
- Modify: `cmd/wideboi/main.go`: both frame loops (~L351-355, ~L487-491) call `present(scr)`.

**Key changes:**

```go
// present writes the current frame to the terminal with the cursor where
// it was asked to be. ... (why: MoveTo stranded in rend.buf)
func present(scr *uv.TerminalScreen) {
	scr.Render()
	_ = scr.Flush()
	scr.Render()
	_ = scr.Flush()
}
```

Test (`TestPresentLeavesCursorAtRequestedPosition`):
1. `scr := uv.NewTerminalScreen(&buf, []string{"TERM=xterm-256color"})`, `Resize(80, 24)`, `EnterAltScreen()`; `em := vt.NewEmulator(80, 24)`.
2. Frame 1: put text near the top left, `SetCursorPosition(2, 1)`, `ShowCursor()`, `present`. Feed `buf` to `em`, reset `buf`. Assert `em.CursorPosition() == (2,1)`.
3. Frame 2 (the background card update): write a cell at `(60, 20)`, keep the cursor at `(2,1)`, `present`, feed. Assert the cursor is at `(2,1)`. With a single `Render(); Flush()` it's at `(61,20)`.
4. Repeat frame 2 with a different far cell a few times so the check holds across consecutive busy frames, not just one.

**Verification — automated:**
- [x] Test fails with `present` as a single `Render(); Flush()` (proves the cause). **All four frames failed, each with the cursor one cell past the last written cell: (1,1), (61,20), (41,10), (71,5).**
- [x] Test passes with the double pass. **PASS; the second pass emits only `ESC[?25l ESC[2;3H ESC[?25h`.**
- [x] `make quick` passes. **All packages ok.**
- [x] `make check` passes, four runs (timing-adjacent change). **4/4 OK: exit 8/8, smoke 28/28, golden matches, attach-check ok.**

**Post-review addition (Copilot, with Les's OK):** both passes are bracketed in one mode-2026 synchronized update; the test asserts the cursor at the end of that bracket; the golden `SYNC_*` entries are presence-only.
- [x] Bracketed test fails on single pass and on bracket-closed-between-passes. **Both FAIL with cursor at last cell + 1.**
- [x] `make check` ×4 after bracketing and golden change. **4/4 OK, golden matches.**

**Verification — manual:**
- [x] Run `./bin/wideboi` with two or three panes busy (`while :; do date; sleep 0.05; done`) and the focused pane at a prompt: the cursor stays put, with no visible blink. **Les confirmed on 2026-09-22: the cursor stays put.**
