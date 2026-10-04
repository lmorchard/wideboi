# Notes: Native wait-output and wait-status Commands (Issue #377)

- 2026-10-04: Session started in worktree `/home/lmorchard/devel/wideboi-issue-377` on branch `feat/issue-377-wait-output-and-status`.
- Added `MsgWaitOutputRequest`, `MsgWaitOutputResponse`, `MsgWaitStatusRequest`, `MsgWaitStatusResponse` to wire protocol.
- Bumped wire `protocol.Version` to 28 and updated schema test golden hash.
- Implemented `outputWaiters` and `statusWaiters` on `Server` with immediate match check and event-driven fulfillment.
- Hooked `p.SetOnOutput` on PTY read loop writes.
- Implemented `runWaitOutput` and `runWaitStatus` in `cmd/wideboi/control.go` with support for positional and flag syntax and timeout exit code 124.
- Added `:wait-output` and `:wait-status` commands in `internal/commands/registry.go`.
- Documented commands in `docs/skills/wideboi-control/SKILL.md`.
- All tests and `make quick` passed cleanly.
