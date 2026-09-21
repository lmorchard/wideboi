# Spec: Issue #38 — Bounded PTY Write (Plan 22)

## Goal

Prevent an unresponsive child process (or a child that has stopped reading stdin) from wedging the multiplexer's render loop and input processing across all panes.

## Root Cause

`internal/server/pane.go`'s `pty-writer` pump goroutine performs unbounded writes to `p.pty.Master`. When a child stops reading stdin, the OS tty input buffer fills (`_PC_MAX_INPUT` = 1024 bytes), parking the `pty-writer` pump in `Master.Write`. This stops the pump from reading the emulator's unbuffered reply pipe (`e.pr`), causing `SafeEmulator.SendKey` to block while holding `se.mu.Lock()`. The server's 33ms frame ticker calls `broadcastPaneUpdates`, which calls `p.Draw()` (needing `se.mu.RLock()`) while holding `s.mu.Lock()`. This deadlocks the entire multiplexer.

## Design

### 1. Poller-Integrated PTY Master in `ptyx`
In `internal/server/ptyx/pane.go`:
- After `pty.StartWithSize`, set `syscall.SetNonblock(int(master.Fd()), true)`. This registers the file descriptor with Go's runtime poller (kqueue on macOS, epoll on Linux), enabling deadline support.
- Add `WriteBounded(b []byte, d time.Duration) (int, error)` to `*ptyx.Pane`.
  - Sets deadline `time.Now().Add(d)` before writing.
  - Clears deadline (`time.Time{}`) after writing.
  - Returns `n, err`. If deadline is exceeded, `err` matches `os.ErrDeadlineExceeded`.

### 2. Bounded Write in `pane.go`
In `internal/server/pane.go`:
- Define `const PTYWriteTimeout = 50 * time.Millisecond`.
  - Normal PTY writes take < 1ms. 50ms provides ample headroom for bursts while bounding any pause to well below user perception.
- In `Start()`'s `pty-writer` pump:
  ```go
  n, err := p.grid.Read(buf)
  if n > 0 {
      if _, werr := p.pty.WriteBounded(buf[:n], PTYWriteTimeout); werr != nil {
          if errors.Is(werr, os.ErrDeadlineExceeded) {
              p.dropped.Add(uint64(n))
              continue
          }
          return
      }
  }
  ```
- In `Pane.Write(b []byte)`:
  Call `p.pty.WriteBounded(b, PTYWriteTimeout)`. If a deadline error occurs, return `0, werr` or dropped, ensuring raw client writes under `s.mu` also never block indefinitely.

### 3. What we're NOT doing
- We are not changing how `pty-reader` works (`Master.Read` is the child-output pump and blocks until child produces output; child exit produces EOF/EIO cleanly).
- We are not changing the 256-key channel buffer (`keyQueueDepth`).
- We are not introducing background goroutine leaks or complex watchdog timers.

## Verification
1. `TestChildNotReadingStdinFreezesRendering` (in `internal/server/pane_wedge_test.go`):
   - Proven to fail before the fix (wedged for > 2s with `se.mu` and `s.mu` held).
   - Proven to pass after the fix.
2. `make quick` (fmt, vet, seam-check, test).
3. `make check` 4 times with `-count=1` per LESSONS.md.
