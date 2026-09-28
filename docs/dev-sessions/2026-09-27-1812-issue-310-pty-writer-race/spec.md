# Fix Concurrent PTY Master WriteBounded Race and Deadline Clobbering (#310) Spec

**Goal:** Eliminate concurrent write race conditions, deadline clobbering, and in-place upgrade synchronization gaps on the PTY master by serializing paste input through `term.Grid`'s input pipe and serializing `ptyx.Pane.WriteBounded`.

**Source:** https://github.com/lmorchard/wideboi/issues/310

## Current state

- **`internal/server/pane.go:222-224`**: `key-writer` goroutine handles `RawBytes` by calling `p.Write(ev)` directly.
- **`internal/server/pane.go:178-205`**: `pty-writer` concurrently reads from `p.grid.Read(buf)` and calls `p.pty.WriteBounded(buf[:n], ptyWriteTimeout)`.
- **`internal/server/ptyx/pane.go:213-219`**: `WriteBounded` arms `Master.SetWriteDeadline` and clears it in `defer SetWriteDeadline(time.Time{})` without synchronization. Concurrent calls race on Go runtime poller file descriptors and clear each other's deadlines.
- **`internal/server/pane.go:630-662`**: In-place upgrade `p.drainInput` sends `inputFlush` (`p.grid.SendText("")`), assuming all prior client input passes through `p.grid`. `RawBytes` bypasses `p.grid` and can still be in flight during upgrade exec.
- **`internal/server/pane.go:190-192`**: The test hook `p.ptyWritten` is only invoked in `pty-writer`, so `RawBytes` bypasses it.

## Desired end state

1. **`key-writer` routes `RawBytes` through `p.grid.SendText`**:
   - In `internal/server/pane.go`, when `ev` is `RawBytes`, forward it via `p.grid.SendText(string(ev))` (with fallback to `p.Write(ev)` if `p.grid == nil`).
   - `pty-writer` becomes the sole writer for queued client input (`KeyEvent`, `MouseEvent`, `RawBytes`, `inputFlush`).
   - `p.ptyWritten` callback and byte accounting observe paste input.
   - `drainInput` reliably waits for paste input writes to complete before returning.
2. **`ptyx.Pane.WriteBounded` is protected by `writeMu`**:
   - `ptyx.Pane` gains a `writeMu sync.Mutex` held across setting the deadline, calling `p.Master.Write`, and clearing the deadline.
   - Out-of-band calls to `p.Write` (such as `protocol.MsgSendInputRequest` from `wideboi send-input`) cannot clobber deadlines or interleave writes on `p.Master`.
3. **Comprehensive automated tests**:
   - Concurrency unit test in `ptyx/pane_test.go` verifying concurrent `WriteBounded` calls under `-race` do not race or panic.
   - Unit test in `server/upgrade_test.go` verifying `drainInput` waits for `SendBytes` and triggers `p.ptyWritten`.

## Design decisions

- **Decision:** Route `RawBytes` through `p.grid.SendText` in `key-writer`.
  - **Why:** `p.grid.SendText` feeds into `vt`'s internal unbuffered pipe, which is already drained by `pty-writer`. This establishes a single serialization pipeline for all client events in FIFO order, unifies `drainInput` synchronization, and invokes `p.ptyWritten`.
  - **Rejected:** Leaving `p.Write` in `key-writer` and only adding a mutex. This would fail to fix the `drainInput` upgrade gap and would leave `p.ptyWritten` bypassed for paste.
- **Decision:** Add `writeMu sync.Mutex` to `ptyx.Pane.WriteBounded`.
  - **Why:** `WriteBounded` modifies global deadline state on `p.Master` (`*os.File`). A mutex ensures that `SetWriteDeadline`, `Write`, and `SetWriteDeadline(time.Time{})` are strictly atomic across any concurrent callers (such as out-of-band RPC writes).
  - **Rejected:** Relying solely on callers to serialize. PTY primitives should guarantee safe bounds on their own methods.

## Patterns to follow

- **`internal/server/pane.go:214-236`**: `key-writer` event dispatching pattern.
- **`internal/server/term/grid.go:50-65`**: `term.Grid.SendText` intended for raw text and paste injection.
- **`internal/server/upgrade_test.go:500-523`**: `TestDrainInputWaitsForPtyWrite` pattern using `p.ptyWritten` and slow writes to verify flush ordering.

## What we're NOT doing

- Not changing wire protocol or message formats.
- Not altering client-side bracketed paste decoding or router logic.
- Not changing `keyQueueDepth` or `ptyWriteTimeout` constants.
- Not altering `handleSendInputRequestLocked` response signature.

## Open questions

- None. (Design and scope fully agreed upon).
