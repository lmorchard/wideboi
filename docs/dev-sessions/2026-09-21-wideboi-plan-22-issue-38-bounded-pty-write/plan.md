# Plan: Issue #38 — Bounded PTY Write (Plan 22)

## Phase 1: Bounded Write in `ptyx`
- In `internal/server/ptyx/pane.go`:
  - Hook master fd into poller using `syscall.SetNonblock(int(master.Fd()), true)` in `Spawn`.
  - Add `WriteBounded(b []byte, d time.Duration) (int, error)`.
- In `internal/server/ptyx/pane_test.go`:
  - Add `TestWriteBoundedDeadline`: verify that writing into a full PTY buffer with a short deadline (e.g. 50ms) returns `os.ErrDeadlineExceeded` within ~50ms rather than hanging.

## Phase 2: Update `internal/server/pane.go`
- Add `const PTYWriteTimeout = 50 * time.Millisecond`.
- Update the `pty-writer` pump in `Start()`:
  - Call `p.pty.WriteBounded(buf[:n], PTYWriteTimeout)`.
  - If `errors.Is(werr, os.ErrDeadlineExceeded)`: increment `p.dropped.Add(uint64(n))` and `continue`.
  - On non-deadline error: `return`.
- Update `Pane.Write(b []byte)` to use `p.pty.WriteBounded(b, PTYWriteTimeout)`.

## Phase 3: Integration Regression Test
- Move the reproduction test from `repro_test.go` into `internal/server/pane_wedge_test.go`:
  `TestChildNotReadingStdinDoesNotFreezeServer`:
  - Start server with 2 panes.
  - Make pane 1 run a command that stops reading stdin (`sleep 100`).
  - Fill the 1024-byte tty input queue.
  - Send keys to pane 1.
  - Assert that pane 1's `Draw()` and `srv.mu` remain responsive and do not deadlock.
- Remove temporary `repro_test.go`.

## Phase 4: Full Verification
- `make quick`
- `make check` (run 4 times to ensure concurrency and timing stability per LESSONS.md)
