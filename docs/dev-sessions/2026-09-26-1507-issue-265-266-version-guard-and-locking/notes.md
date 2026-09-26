# Dev Session Notes: Issues 265 & 266

## Session Start
- Branch: `issue-265-266`
- Worktree: `.worktrees/issue-265-266`
- Issues:
  - #265: Guard protocol.Version against missed wire bumps (Go schema and web literal)
  - #266: Lock discipline: sends and writes under client/server mutexes
- Baseline verified: `make quick` passed cleanly.

## Key Changes Made

### Issue #265
- Added `web/src/version.ts` declaring `PROTOCOL_VERSION = 14` and `VERSION_PROTOCOL = "wideboi.v14"`.
- Updated `web/src/client.ts`, `client.test.ts`, `lifecycle.test.ts`, `tests/browser-fixture.ts`, and all 8 Playwright specs to import and use `VERSION_PROTOCOL`.
- Added golden schema guard test `internal/protocol/version_guard_test.go`:
  - `TestWireSchemaMatchesProtocolVersion`: compares sha256 of deterministic FileDescriptorProto marshal against `wireSchemaHashes[Version]`. Fails if schema changes without Version bump or if Version is bumped without updating golden.
  - `TestWebClientVersionMatchesGoProtocolVersion`: verifies `web/src/version.ts` matches `protocol.Version`. Fails during `make quick` if versions drift.

### Issue #266
- Client lock discipline:
  - `SendVerb`, `SendSplit`, `HandleServerMsg`, `SearchCommit`, `SearchNavigate`, and `SearchEnd` in `internal/client/` now release `c.mu` before calling `c.transport.SendClient`.
  - Added regression test `internal/client/blocked_send_test.go` proving `c.Draw` completes even when `transport.SendClient` is blocked on a stalled writer.
- Server lock discipline & atomicity:
  - Replaced split critical sections in `handleClientMsg` and `onPaneExit`: separated resize into `prepareResizePanesLocked()` (pure state update under `s.mu`) and `applyResizeJobs()` (invoked after releasing `s.mu`).
  - Added non-blocking `p.SendBytes(m.Data)` in `Pane` via `RawBytes` queued onto `p.input` and drained to `p.pty.WriteBounded` in the background input loop.
  - Shifted synchronous `p.Write(m.Data)` in `MsgSendInputRequest` to execute after releasing `s.mu`.
  - Updated `input_queue_test.go` to verify keys, mouse events, and pasted bytes share a single ordered queue.
- Server double-reader race:
  - Added `startedTransports map[transport.Transport]bool` and `startTransportLoopLocked(ctx, tp)` in `Server`.
  - Ensured clients admitted via socket or WebSocket before `Run()` do not get duplicate reader loops when `Run()` iterates initial transports.
  - Added regression test `internal/server/double_reader_test.go`.

## Verification
- `make quick` passed.
- `make check` passed (all 10 targets: fmt-check, lint, seam-check, test, web-test, web-accept, race, verify-exit, smoke, attach-check).
- Timing and concurrency test suites run 4x with race detector cleanly.

