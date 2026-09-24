# Research: Issue 205 - vt.Scrollback Ring Buffer

## 1. Upstream `vt.Scrollback` Implementation

- **Location**: `github.com/charmbracelet/x/vt@v0.0.0-20260913004009-c615ff2f7805/scrollback.go:13-16`
- **Data structure**:
  ```go
  type Scrollback struct {
      lines    []uv.Line
      maxLines int
  }
  ```
- **Eviction in `Push` (`scrollback.go:50-54`)**:
  ```go
  if len(s.lines) >= s.maxLines {
      s.lines = slices.Delete(s.lines, 0, 1)
  }
  s.lines = append(s.lines, cloned)
  ```
  Once `len(s.lines) >= maxLines` (default `10,000`), every `Push` calls `slices.Delete(s.lines, 0, 1)`.
  This performs a `copy(s.lines[0:], s.lines[1:])` of 9,999 slice headers (~240 KB memmove) on every single scrolled line.
- **Allocation in `Push` (`scrollback.go:48`)**:
  `cloned := slices.Clone(line[:lastNonEmpty+1])` allocates a new backing array for every line pushed.
- **Upstream status in `charmbracelet/x`**:
  - Issue 821 (`vt: reduce per-line allocation churn in Scrollback.Push`) reported April 2026.
  - PR 822 (`vt: reuse scrollback line buffers`) pools line buffers.
  - PR 888 (`perf(vt): ring-buffer Scrollback eviction + cheap trailing-blank scan`) introduces a ring buffer with `head int` index into `s.lines` and fast-path empty cell check.
  - Upstream `main` (`v0.0.0-20260924144451-d676b019604b`) still has the unmerged O(n) `slices.Delete` implementation.

## 2. Emulator & Screen Encapsulation in `charmbracelet/x/vt`

- **Screen owns `scrollback` (`screen.go:19, 26`)**:
  ```go
  type Screen struct {
      ...
      scrollback *Scrollback
  }
  ```
  `scrollback` is a concrete pointer `*Scrollback`, not an interface.
- **Emulator owns screens (`emulator.go:26`)**:
  ```go
  type Emulator struct {
      ...
      scrs [2]Screen
      scr  *Screen
  }
  ```
  `scrs` is private. `Emulator` exposes `Scrollback() *Scrollback`, `ScrollbackLen() int`, `ScrollbackCellAt(x, y int) *uv.Cell`, `SetScrollbackSize(maxLines int)`, and `ClearScrollback()`.
  There is no `SetScrollback` on `Emulator`, and `Screen.SetScrollback(sb *Scrollback)` takes the concrete type.
- **Scroll invocation points**:
  - `Screen.DeleteLines` (`screen.go:359-368`): called when text scrolls off the top of the scroll region. Calls `s.scrollback.PushN(s.buf, y, linesToSave)`.
  - `Screen.ClearWithScrollback` (`screen.go:94-106`): called on ED 2 (Erase in Display).
- **Callbacks (`callbacks.go:10-63`)**:
  `vt.Callbacks` contains no scroll, line-eviction, or line-push callbacks.

## 3. How `wideboi` Uses `term.Grid` and `vt.Emulator`

- **Instantiation**:
  - `Pane` (`internal/server/pane.go:91`) calls `term.NewVT(cols, rows)`.
  - `term.NewVT` (`internal/server/term/grid.go:226`) wraps `vt.NewSafeEmulator(cols, rows)`.
- **Scrollback methods on `term.Grid` (`internal/server/term/grid.go:105-107, 130`)**:
  - `ScrollbackLen() int`: queries `g.em.ScrollbackLen()`.
  - `ScrollOffset() int`: returns atomic current offset.
  - `SetScrollOffset(offset int)`: clamps offset to `[0, g.em.ScrollbackLen()]` and bumps generation.
  - `DrawAt(dst uv.Screen, area image.Rectangle, offset int)`:
    - If `offset <= 0`, delegates to `g.em.Draw(dst, area)`.
    - If `offset > 0`, fetches cells with `g.em.ScrollbackCellAt(x, sbY)`.
- **Resize and Reflow**:
  - `vtGrid.Resize` (`internal/server/term/grid.go:602-662`): reflows only the visible screen (`Reflow(before, oldCols, cols)`). Scrollback lines are untouched during resize.
- **Wire & Rendering Consumers**:
  - `Server.handleClientMsg` (`internal/server/server.go:490-523`): handles `protocol.MsgScroll`, clamps offset to `p.ScrollbackLen()`.
  - `Server.broadcastPaneUpdates` (`internal/server/server.go:1096-1133`): pins scrolled history if `sbLen` increases while scrolled.
  - `Pane.UpdateMessageForOffset` (`internal/server/pane.go:308-367`): draws history into temporary buffer for client updates.

## 4. Current Workloads and Benchmark Baselines

- `internal/server/pane_traffic_test.go:20`: `TestPaneTrafficWorkloads` runs `scroll_80x24` and `scrollback_80x24`, but only with 24-30 lines of history (never fills the 10,000 line scrollback).
- Profiling under #179 (`seq 1 200000`, 10,000-line scrollback):
  - Server CPU was ~73% of a core.
  - ~80% of that was the emulator write path; `Scrollback.Push`'s `memmove` was ~30% of write time.
  - `slices.Clone` in `Push` was 63% of burst allocation.
