# Research: Issue #38 — Bounded PTY Write & Render Wedge

## The Bug Chain

1. **Child stops reading stdin:**
   A child running on a pane PTY stops consuming input (e.g. running `sleep`, compiling, executing a long-running batch script, or unresponsive).
2. **PTY input queue fills:**
   On Unix (macOS / Linux), line discipline input queue is bounded (`_PC_MAX_INPUT` / `_PC_MAX_CANON`, 1024 bytes). When input reaches this limit, subsequent writes to the master block in the kernel.
3. **PTY-writer pump parks:**
   In `internal/server/pane.go:126-136`, the pump reads from `p.grid.Read(buf)` and calls `p.pty.Master.Write(buf[:n])`. Because the master fd is blocking, `Master.Write` blocks indefinitely. The pump stops calling `p.grid.Read(buf)`.
4. **Emulator reply pipe fills:**
   `p.grid.Read` reads from `SafeEmulator.Read` -> `Emulator.Read`, which reads from `e.pr` (an unbuffered `io.Pipe`).
5. **Key-writer parks holding `se.mu`:**
   When subsequent keys arrive, `p.keys` feeds the `key-writer` goroutine (`pane.go:148`). It calls `SafeEmulator.SendKey`, which acquires `se.mu.Lock()` and attempts to write to `e.pw`. Because `e.pw` is unbuffered and unread, this write blocks indefinitely **while holding `se.mu.Lock()`**.
6. **Server render loop parks holding `srv.mu`:**
   Every 33ms, `frameTicker` calls `s.broadcastPaneUpdates(ctx)`. This acquires `srv.mu.Lock()` and calls `p.UpdateMessage()` on each pane. `p.UpdateMessage()` calls `p.Draw()`, which calls `SafeEmulator.CellAt` / `Draw`, acquiring `se.mu.RLock()`.
   Because `se.mu` is held by the wedged `key-writer`, `p.Draw()` blocks forever **while holding `srv.mu.Lock()`**.
7. **Complete freeze:**
   With `srv.mu` held forever, all client input, resize events, other pane renders, and clean shutdown are blocked.

## The Reproduction

We reproduced this failure 100% reliably in `internal/server/repro_test.go`:
Running a shell that executes `sleep 100`, filling the 1024-byte input queue, and sending two keystrokes causes `p1.Draw` to block on `se.mu` and `srv.mu` to become deadlocked.

## Technical Investigation of Go runtime & PTY deadlines

- `creack/pty.StartWithSize` creates `master` as an `*os.File` wrapping a blocking fd.
- `master.SetWriteDeadline` on a blocking PTY fd returns `file type does not support deadline` because Go's runtime poller (kqueue on macOS, epoll on Linux) is only attached to file descriptors that are non-blocking when passed to `os.NewFile` (or set nonblocking via syscall).
- Setting `syscall.SetNonblock(int(master.Fd()), true)` hooks the master descriptor into Go's netpoller.
- Once registered, `master.SetWriteDeadline(time.Now().Add(d))` works natively, returning `os.ErrDeadlineExceeded` when the write times out.

## The Solution

1. In `internal/server/ptyx/pane.go`:
   - At spawn time, mark the master fd non-blocking: `syscall.SetNonblock(int(master.Fd()), true)`.
   - Provide a bounded write helper on `ptyx.Pane`:
     `WriteBounded(b []byte, timeout time.Duration) (int, error)`
     which arms `p.Master.SetWriteDeadline(time.Now().Add(timeout))`, writes, and disarms `p.Master.SetWriteDeadline(time.Time{})`.
2. In `internal/server/pane.go`:
   - In the `pty-writer` pump goroutine (`pane.go:126-136`):
     Use `WriteBounded(buf[:n], ptyWriteTimeout)` (e.g. 50ms or 100ms).
     If `errors.Is(werr, os.ErrDeadlineExceeded)`:
     - Increment `p.dropped.Add(uint64(n))`
     - Do NOT return from the pump; continue looping so `p.grid.Read(buf)` continues to drain the emulator's reply pipe!
     - If `werr != nil` (non-timeout error like EBADF or EIO when child exits): return as before.
   - In `Pane.Write(b []byte)` (`pane.go:208`):
     Route through `WriteBounded` so raw input under `s.mu` (from `handleClientMsg`) cannot hold `s.mu` indefinitely either.
