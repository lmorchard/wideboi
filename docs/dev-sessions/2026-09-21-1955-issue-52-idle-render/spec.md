# Spec: Issue #52 - Skip Render/Flush When Idle

**Goal:** Eliminate wideboi's ~1.1 KB/s idle wire emission by skipping `Render` and `Flush` when the composed frame is unchanged.

**Source:** https://github.com/lmorchard/wideboi/issues/52

## Current state

Today, `cmd/wideboi/main.go` runs a 16ms ticker (`frame.C`) in both in-process (`run`, line 405) and attach (`runAttach`, line 260) loops. On every tick, it calls:
- `cli.Draw(scr, ...)`
- `scr.Render()`
- `_ = scr.Flush()`

`scr` is `*uv.TerminalScreen`. In `cli.Draw` (`internal/client/client.go:323-324`), when the focused placement has a visible cursor, it calls `scr.SetCursorPosition(fx, fy)` and `scr.ShowCursor()`. In `ultraviolet/terminal_screen.go:386-393`, `ShowCursor()` writes `\x1b[?25h` directly into `s.buf`. In `scr.Flush()` (`terminal_screen.go:274-301`), because `s.buf.Len() > 0`, it emits `\x1b[?25l`, the buffer content, and `\x1b[?25h`. This produces ~62 cursor hide/show pairs per second (~1,134 B/s) while completely idle.

Because the pty is never quiet, `scripts/ptylib.py:81` maintains `_FRAME_NOISE = re.compile(rb"\x1b\[\?25[hl]")` to strip cursor hide/show escapes before calculating output size in `settle_output`.

## Desired end state

1. **Zero idle emission:** When no pane output arrives, no keys are pressed, no animation is running, and cursor state is unchanged, wideboi emits **0 bytes/second** to the pty/terminal.
2. **`Client.Draw` signature & return value:**
   `func (c *Client) Draw(scr HostScreen, drawPane func(id int, dst uv.Screen, area image.Rectangle), cursorInfo func(id int) (image.Point, bool)) bool`
   Returns `true` if the frame changed and was written to `scr`, or `false` if the frame is identical to the previous one and `scr` was left untouched.
3. **Loop integration:** `cmd/wideboi/main.go` calls:
   ```go
   if cli.Draw(scr, ...) {
       scr.Render()
       _ = scr.Flush()
   }
   ```
   in both in-process and attach modes.
4. **`_FRAME_NOISE` removed:** Delete `_FRAME_NOISE` from `scripts/ptylib.py`. `settle_output` measures `len(drainer.output())` directly.
5. **Regression test:** A smoke test in `scripts/smoke.py` asserts that once settled, wideboi emits 0 bytes over a quiet window.

## Design decisions

- **Decision:** Off-screen frame staging and comparison in `Client.Draw`.
  - **Why:** `Client` already owns frame composition (`composeFrameLocked`, `drawStatusBarLocked`, `drawHelpOverlay`, cursor positioning and visibility). By composing into an internal offscreen buffer (`offscreenHostScreen`) and comparing with the last-rendered frame (cells, dimensions, cursor visibility, cursor position), change detection is completely self-contained. It captures any change — PTY child output, cursor move, status line update, OSC 133 status, animation advancement, window resize — without fragile dirty flags across packages.
  - **Rejected:** Event-driven dirty flagging across server emulator and client. That would require intrusive hooks into `term.Emulator`, `Server`, and `Client`, and risks missing subtle changes (like cursor blink from a child app, terminal activity timer decays, or race conditions).
- **Decision:** Compare `lastHostScreen` pointer in `Client.Draw`.
  - **Why:** Existing unit tests allocate fresh `newFakeHostScreen` instances and call `cli.Draw(scr, ...)`. If `scr != c.lastHostScreen`, `Draw` considers it dirty and copies to `scr`, ensuring all test assertions on `scr` remain valid and green.
  - **Rejected:** Caching globally across different `HostScreen` targets, which would break test cases that pass multiple `scr` instances to the same client.
- **Decision:** Double-buffered offscreen screens inside `Client`.
  - **Why:** Reusing two preallocated offscreen buffers (`staging` and `lastRendered`) avoids heap allocations every 16ms tick.
  - **Rejected:** Allocating new `uv.ScreenBuffer` surfaces on every 16ms frame tick.

## Patterns to follow

- `internal/client/screen_test.go:19-33`: `fakeHostScreen` structure implementing `HostScreen` (`Surface`, `cursorShown`, `cursorX`, `cursorY`).
- `internal/client/motion.go:158-168`: `placementsEqual` pattern for pure structural comparison.
- `scripts/smoke.py`: pty test assertions using `Session` and `settle_output`.

## What we're NOT doing

- We are NOT modifying the server's internal socket broadcast ticker or socket protocol in this session (the issue specifically targets idle wire emission from the client to the terminal/pty).
- We are NOT removing the 16ms ticker — animations and smooth interactive responsiveness still rely on the 60fps frame tick.
- We are NOT altering terminal cursor blink handling (the host terminal handles cursor blinking; wideboi does not synthesize cursor blinks).
- We are NOT modifying `charmbracelet/ultraviolet` or vendoring overrides.

## Open questions

None.
