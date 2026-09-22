# Research: Issue #52 (Idle Frame Rendering & Emission)

## Findings

### 1. The Render Tick and Call Path
- **In-process loop:** `cmd/wideboi/main.go:405-413`
  - On every 16ms tick (`frame.C`):
    - `cli.Draw(scr, srv.DrawPane, srv.CursorInfo)`
    - `scr.Render()`
    - `_ = scr.Flush()`
- **Attach loop:** `cmd/wideboi/main.go:260-266`
  - On every 16ms tick (`frame.C`):
    - `cli.Draw(scr, nil, nil)`
    - `scr.Render()`
    - `_ = scr.Flush()`

### 2. Screen Types and Cursor Emission
- `scr` is `*uv.TerminalScreen` from `github.com/charmbracelet/ultraviolet` (`terminal.go:161`, `terminal_screen.go:57`).
- `HostScreen` interface (`internal/client/client.go:260`):
  ```go
  type HostScreen interface {
      uv.Screen
      HideCursor()
      ShowCursor()
      SetCursorPosition(x, y int)
  }
  ```
- In `cli.Draw` (`internal/client/client.go:323-324`):
  - If cursor is visible, it calls `scr.SetCursorPosition(fx, fy)` and `scr.ShowCursor()`.
  - `*uv.TerminalScreen.ShowCursor()` (`terminal_screen.go:386-393`):
    - Writes `ansi.ShowCursor` (`\x1b[?25h`) directly to `s.buf`!
- In `scr.Flush()` (`terminal_screen.go:274-301`):
  - Because `s.buf.Len() > 0`, and synchronized updates are off while `cursor != nil && !cursor.Hidden`:
    - Wraps `s.buf` with `ansi.HideCursor` (`\x1b[?25l`) before, and `ansi.ShowCursor` (`\x1b[?25h`) after.
  - Result: 2 `\x1b[?25h` and 1 `\x1b[?25l` per frame tick (62.5 fps = 125 show, 62.5 hide per second = ~1,134 B/s).

### 3. Change Detection & Compositing
- `cli.Draw` contains no change detection (`internal/client/client.go:268-331`).
- Composed frame components:
  1. Pane headers (`composeFrameLocked`, lines 356-378).
  2. Pane content (either `drawPane` via `p.Draw` or `c.mirrors[p.PaneID].Surface`).
  3. Column dividers (`internal/client/client.go:400-408`).
  4. Hidden markers (`+N`, lines 415, 526-552).
  5. Status bar (`drawStatusBarLocked`, lines 561-564).
  6. Cursor: position `(fx, fy)` and visibility (`ShowCursor` / `HideCursor`).
  7. Motion / animation: `c.motion *motion` (`internal/client/client.go:279-286`). During animation, `c.motion.at()` produces new placements each tick until `c.motion.done()`.

### 4. Wire Noise & Test Harnesses
- `scripts/ptylib.py:81`:
  `_FRAME_NOISE = re.compile(rb"\x1b\[\?25[hl]")`
- `settle_output` in `scripts/ptylib.py:84-116` uses `_FRAME_NOISE` to filter out these cursor escape sequences so it can detect when output has settled.
- Issue #52 explicitly specifies deleting `_FRAME_NOISE` when this bug is fixed.
