# Research: Issue 337 Soft-Wrap Selection & Copying

- Prior Attempt & Lessons Learned (PR 356):
  - In PR 356, an attempt was made to hook into `CursorPosition` in `term/grid.go` and record wrapped lines in a map under a mutex. This caused severe lock contention on every character output (slowing `term` tests by 10x) and leaked memory because the map was never cleared or synced with scrolling/screen clearing.
  - Furthermore, `charmbracelet/x/vt` and `ultraviolet` have no native wrap bit on lines.
- Clean Architecture:
  - Computing soft-wrap status during `UpdateMessageForOffset` avoids all per-character callbacks and locks.
  - A row `y` soft-wraps into `y+1` if its rightmost column (`cols-1`) has non-empty/non-space content and row `y+1` contains content.
  - Wire protocol passes `wrapped` per line, allowing both TUI and Web clients to omit `\n` when copying text across wrapped lines.
