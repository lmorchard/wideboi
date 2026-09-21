# Notes: Issue #38 — Bounded PTY Write (Plan 22)

## What was decided and why

- **The bug was real and fully reproduced:**
  When a pane child stops reading stdin (e.g. `sleep 100`, compiler, batch script), the OS canonical tty input buffer (`1024` bytes on macOS/Linux) fills up.
  The `pty-writer` pump goroutine in `internal/server/pane.go` blocks in `p.pty.Master.Write`.
  Because it is blocked, it stops reading from the VT emulator's reply pipe (`e.pr`, an unbuffered `io.Pipe`).
  The next keystroke causes `SafeEmulator.SendKey` to block trying to write to `e.pw` while holding `se.mu.Lock()`.
  The server's 33ms frame loop calls `broadcastPaneUpdates`, which calls `p.UpdateMessage()` -> `p.Draw()` while holding `srv.mu.Lock()`. `p.Draw()` blocks waiting for `se.mu.RLock()`, deadlocking `srv.mu` and freezing every pane in the multiplexer.

- **Why Go's `os.File.SetWriteDeadline` previously failed on PTYs:**
  `creack/pty.StartWithSize` creates `master` as a blocking file descriptor, wrapped in `os.NewFile`. Go's `poll.FD` only hooks descriptors into the runtime netpoller (kqueue on macOS, epoll on Linux) if the descriptor is non-blocking when passed to `os.NewFile`. Calling `SetDeadline` on a blocking `os.File` returns `file type does not support deadline`.
  In `ptyx.Spawn`, we `syscall.Dup` the raw master fd, close the original, set `syscall.CloseOnExec` and `syscall.SetNonblock(newFd, true)`, and wrap `newFd` in `os.NewFile`. This cleanly hooks the master descriptor into Go's netpoller with no fd leaks or GC finalizer races.

- **Bounded writes:**
  - `ptyx.Pane.WriteBounded(b []byte, timeout time.Duration)` arms a deadline on the master before write and disarms it after.
  - `pane.go`'s `pty-writer` pump uses a 50ms deadline. On `os.ErrDeadlineExceeded`, it counts dropped bytes in `p.dropped` and continues its loop, keeping the reply pipe drained.
  - `Pane.Write` also routes through `WriteBounded` so client paste/raw input under `s.mu` cannot hang callers either.

## Verification

- `TestWriteBoundedDeadline` in `internal/server/ptyx/pane_test.go` pins the 50ms timeout behavior when writing to a full PTY buffer.
- `TestChildNotReadingStdinDoesNotFreezeServer` in `internal/server/pane_wedge_test.go` recreates the exact deadlock sequence (`sleep 100`, full 1024-byte tty input queue, subsequent keystrokes) and confirms `p1.Draw()` completes and `srv.mu` remains fully responsive.
- `make quick` passed.
- `make check` passed across 4 consecutive full runs (including race detector, smoke tests, attach check, verify-exit, and golden snapshot).
