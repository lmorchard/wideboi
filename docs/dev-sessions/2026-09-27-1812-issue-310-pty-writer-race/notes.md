# Notes: Issue 310 - Fix concurrent PTY Master WriteBounded race and deadline clobbering from paste input

- **Worktree:** `.worktrees/issue-310-pty-writer-race`
- **Branch:** `issue-310-pty-writer-race`
- **Issue:** https://github.com/lmorchard/wideboi/issues/310

## Summary of Changes

1. **Serialized `ptyx.Pane.WriteBounded`**:
   - Added `writeMu sync.Mutex` to `ptyx.Pane`.
   - Locked `writeMu` across `SetWriteDeadline`, `p.Master.Write`, and `SetWriteDeadline(time.Time{})`.
   - Added `TestWriteBoundedConcurrent` in `internal/server/ptyx/pane_test.go` to test concurrent calls under `-race`.
2. **Routed `RawBytes` through `p.grid.SendText` in `key-writer`**:
   - In `internal/server/pane.go`, `key-writer` now forwards `RawBytes` through `p.grid.SendText(string(ev))` when `p.grid != nil`, with fallback to `p.Write(ev)`.
   - Paste input is serialized through `vt`'s reply pipe, making `pty-writer` the single drainer that writes client input to `p.pty.WriteBounded`.
   - In `internal/server/pane.go`, updated `Write` to call `p.ptyWritten(n)` when `p.ptyWritten != nil && n > 0`.
   - Added `TestDrainInputWaitsForRawBytesPtyWrite` in `internal/server/upgrade_test.go` confirming `p.drainInput` waits for raw bytes/paste input writes to complete and `p.ptyWritten` accounts for them.

## Verification

- `go test -race -count=1 ./internal/server/ptyx` passed.
- `go test -race -count=1 ./internal/server/...` passed.
- `make quick` passed.
- `make check` passed with all 9 gates green (including 41 smoke tests, 29 attachcheck tests, golden tests, exit verify, race, and 50 browser specs).

## Retrospective

- **Recap:** Resolved Issue #310 by routing paste (`RawBytes`) in `internal/server/pane.go` through `p.grid.SendText` so it shares the synchronous pipe drained by `pty-writer`, ensuring single-writer serialization to `ptyx.Pane.WriteBounded`, proper `drainInput` flush during in-place upgrade, and `p.ptyWritten` accounting. In addition, added `writeMu sync.Mutex` in `ptyx.Pane.WriteBounded` to ensure deadline atomicity and write exclusivity across any concurrent callers.
- **Scope drift:** None. Implementation strictly addressed the two root causes identified in #310.
- **Surprises:** In `TestWriteBoundedConcurrent`, early hangup on PTY master cut off pending buffered bytes in the kernel line discipline before the reader read them, failing under parallel make check load. Waiting for all lines to be drained before tearing down the PTY master made the concurrency test completely deterministic and robust.
- **Workflow friction:** Smooth. Copilot review correctly called out verifying that concurrent writes are not just non-panicking, but actually produce un-interleaved output records.
- **Memory candidates:** `ptyx.Pane.Hangup` closes `p.Master` immediately, which cuts off unread bytes from the kernel PTY queue; tests asserting output echo must wait for expected output before hanging up.
