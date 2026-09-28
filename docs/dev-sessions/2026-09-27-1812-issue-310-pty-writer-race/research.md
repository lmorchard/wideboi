# Research: Fix concurrent PTY Master WriteBounded race and deadline clobbering from paste input (#310)

## 1. How PTY Writes and Input Pipelines Work

- **`internal/server/pane.go:178-205`**: `pty-writer` goroutine drains emulator output (`p.grid.Read(buf)`) and writes it to the PTY master via `p.pty.WriteBounded(buf[:n], ptyWriteTimeout)`. It calls `p.ptyWritten(n)` when configured (test hook).
- **`internal/server/pane.go:207-237`**: `key-writer` goroutine drains `p.input` (`chan uv.Event`, capacity `keyQueueDepth = 256`).
  - `uv.KeyEvent`: `p.grid.SendKey(ev)` -> writes into `vt`'s internal `io.PipeWriter`.
  - `uv.MouseEvent`: `p.grid.SendMouse(ev)` -> writes into `vt`'s internal `io.PipeWriter`.
  - `RawBytes`: `_, _ = p.Write(ev)` -> calls `p.pty.WriteBounded(b, ptyWriteTimeout)` directly.
  - `inputFlush`: `p.grid.SendText("")` -> flushes `vt`'s pipe writer; drains synchronously when `pty-writer` reads.
- **`internal/server/ptyx/pane.go:213-219`**:
  ```go
  func (p *Pane) WriteBounded(b []byte, timeout time.Duration) (int, error) {
      if timeout > 0 {
          _ = p.Master.SetWriteDeadline(time.Now().Add(timeout))
          defer func() { _ = p.Master.SetWriteDeadline(time.Time{}) }()
      }
      return p.Master.Write(b)
  }
  ```
  `WriteBounded` has NO mutex.
  Concurrent calls to `WriteBounded`:
  - Data race in Go runtime poller on `Master.SetWriteDeadline`.
  - Deferred `SetWriteDeadline(time.Time{})` clears the deadline of whichever other goroutine is currently executing `WriteBounded`, removing write deadline protection.
  - Interleaved raw writes to `p.Master` (`*os.File`).

## 2. Issues with `RawBytes` Bypassing `p.grid`

1. **Concurrency with `pty-writer`**: `key-writer` and `pty-writer` run concurrently. Any paste event queued via `p.SendBytes` triggers `p.Write(ev)` in `key-writer` at the same time `pty-writer` is writing `p.grid.Read` buffers to the same PTY master.
2. **Upgrade synchronization gap**: `inputFlush` in `p.drainInput` (`internal/server/pane.go:630-662`) sends `p.grid.SendText("")` assuming all preceding inputs passed through `p.grid`. Since `RawBytes` bypassed `p.grid`, `inputFlush` does not guarantee `RawBytes` writes are finished before in-place upgrade exec.
3. **Bypassed test hook / accounting**: `p.ptyWritten` is called in `pty-writer`, so `RawBytes` never triggers `p.ptyWritten`.
4. **Ordering gap with child terminal output**: Key and mouse events go through `p.grid`'s pipe, while `RawBytes` bypassed `p.grid` and raced directly to `p.pty`.

## 3. How `term.Grid` and `vt.Emulator` Handle `SendText`

- **`internal/server/term/grid.go:50-65`**:
  ```go
  // SendText writes raw text to the child process without decoding it
  // as keys. Shifted printable characters route here from SendKey;
  // it can also be used directly for paste or script injection.
  // ...
  SendText(text string)
  ```
  `SendText` was explicitly designed for paste and script injection.
- **`internal/server/term/grid.go:613`**: `vtGrid.SendText(text string)` delegates to `g.em.SendText(text)`.
- **`github.com/charmbracelet/x/vt.Emulator.SendText`**: writes `text` directly to `e.pw` (`*io.PipeWriter`).
- **`github.com/charmbracelet/x/vt.Emulator.Read`**: reads from `e.pr` (`*io.PipeReader`).
- Because `io.Pipe` is synchronous, `SendText` writes into the same pipe that `pty-writer` drains via `p.grid.Read(buf)`.
- If `RawBytes` is forwarded to `p.grid.SendText(string(ev))`, it flows through the exact same pipe and is drained by `pty-writer`, strictly in order, with full `inputFlush` synchronization and `p.ptyWritten` notification.

## 4. Other Callers of `p.Write` and `WriteBounded`

- **`internal/server/handlers.go:235`**: `handleSendInputRequestLocked` handles `protocol.MsgSendInputRequest` (CLI command `wideboi send-input`) by calling `p.Write(m.Data)`.
- **`internal/server/pane.go:355-364`**:
  ```go
  func (p *Pane) Write(b []byte) (int, error) {
      if p.pty == nil {
          return len(b), nil
      }
      n, err := p.pty.WriteBounded(b, ptyWriteTimeout)
      if errors.Is(err, os.ErrDeadlineExceeded) && n < len(b) {
          p.dropped.Add(uint64(len(b) - n))
      }
      return n, err
  }
  ```
- Adding a `writeMu sync.Mutex` inside `ptyx.Pane.WriteBounded` guarantees that `WriteBounded` itself is concurrency-safe against deadline clobbering, race conditions, and interleaved writes for ANY caller (such as out-of-band `p.Write`).
