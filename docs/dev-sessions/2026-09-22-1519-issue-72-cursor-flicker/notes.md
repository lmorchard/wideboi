# Notes: issue #72, host cursor flicker

## Deviation from spec: verification layer
The spec asked for a pty-harness check asserting on the cursor position before each show-cursor. That check can't pass with the specified fix: the first of the two writes still ends in `?25h` at the last cell drawn. A pty byte stream has no write boundaries, so "end of frame" can't be told apart from "between the two writes". I replaced it with `cmd/wideboi/present_test.go`, which drives a real `uv.TerminalScreen` into a buffer, replays each frame through `x/vt`, and asserts the emulator's cursor. It's deterministic, and it failed on the old code with the exact signature from the issue (cursor at last written cell + 1).

## What the bytes look like now
Per changed frame (from a throwaway probe test):
- pass 1: `?25l ?25h CUP(cell) x ?25h`
- pass 2: `?25l CUP(cursor) ?25h`

The follow-up `dirtyFrames` tick now writes nothing; the "idle emits no bytes" smoke case still passes.

## Side finding (not fixed, out of scope)
`copyToHostScreen` (`internal/client/client.go:349-354`) calls `ShowCursor()` on every changed frame. That emits `?25h` inside pass 1, ahead of the cells, and cancels the hide that `Flush` wraps around the draw. The cursor is technically visible while the cells are drawn, but it's all within one write, so it's probably invisible. If a blink ever shows up, this is the first place to look: only emit show/hide on a state change.

## Copilot review: the gap between the two writes (resolved)
Copilot flagged the open question: the first write ends with `?25h` at the last drawn cell, and a terminal could repaint before the corrective write. Fixed by bracketing both passes in one synchronized update (mode 2026), written into the screen buffer via `scr.WriteString`. `SetSynchronizedUpdates` was not used because it brackets each Flush separately. The test now replays only up to the end of that update and asserts the cursor there. It fails on a single pass and on the bracket-closed-between-passes variant.

The golden snapshot counted `SYNC_BEGIN/END` exactly. Since every frame is now bracketed, the count is the number of startup frames (4 locally, 10/10 runs, but timing-dependent). With Les's OK, those two lines became presence-only, matching `golden.py`'s own "exact = one-off, present = per-frame" convention.

## Follow-up filed
- #77: remove the `dirtyFrames` follow-up tick. It now draws nothing and only writes an empty sync bracket after every change.

## Manual check
- Les tested with busy background panes on 2026-09-22: the cursor stays put.
