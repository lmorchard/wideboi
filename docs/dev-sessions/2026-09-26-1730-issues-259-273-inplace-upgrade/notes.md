# Notes: Issues 259 & 273 Dev Session

- Worktree: `.worktrees/issue-259-273-upgrade-fixes` (branch `issue-259-273-upgrade-fixes`)
- Tracking `origin/main` at `a7eb1c3`

## Work Completed

### Issue 259: Client leak on failed in-place upgrade
- Fixed `internal/server/server.go`: on `execFn()` failure, call `s.dropClient(ctx, tp)` to remove the closed transport from server mappings and broadcast lists.
- On `PrepareUpgrade` failure, client remains attached and serviced via `continue`.
- Added unit tests in `internal/server/upgrade_test.go`:
  - `TestPrepareUpgradeFailureKeepsClientAttached`
  - `TestExecFailureDropsClient`

### Issue 273: In-place upgrade state survival
- **Output loss minimization**: moved grid snapshotting and state serialization into `execFn` right before `syscall.Exec`, eliminating the 500ms client drain window where output was previously lost.
- **Race fix**: replaced direct `p.cols`/`p.rows` reads with `cols, rows := p.Size()` under lock in `upgrade.go`.
- **Terminal emulator modes**:
  - `term.GridSnapshot` now records `IsAltScreen`, `BracketedPaste`, and `CursorKeys`.
  - In `RestoreSnapshot`, alt-screen mode (`\033[?1049h`) is entered *before* populating screen cells, ensuring cells land in the alternate screen buffer.
  - Bracketed paste (`\033[?2004h`) and cursor keys mode (`\033[?1h`) are restored.
  - Added unit test `TestGridSnapshotModesRoundTrip` in `internal/server/term/snapshot_test.go`.
- **Ownership & sizing preservation**:
  - `UpgradeState` carries `OwnerPID`, `SizeOwnerPID`, and `StartupLaunched`.
  - In `RestoreState`, `expectedOwnerPID` and `expectedSizeOwnerPID` are staged; when the reconnecting client sets its PID in `setPeerPIDLocked`, `s.owner` and `s.sizeOwner` are automatically restored.
  - `s.startupLaunched` prevents duplicate startup panes from spawning on reconnect.
  - Added unit tests `TestCleanExecArgs` and `TestUpgradeStateRoundTrip` in `internal/server/upgrade_test.go`.
- **Web server preservation**:
  - `UpgradeState` carries `WebRunning`, `WebAddr`, `WebToken`, and `WebTLSEnabled`.
  - In `cmd/wideboi/main.go`, `srv.RestoredWebState()` restarts the web server if it was active before upgrade.
  - Extended `TestUpgradeServerE2E` in `cmd/wideboi/upgrade_e2e_test.go` to verify dynamically started web server survives upgrade and remains running.

## Verification
- `make quick`: passed
- `go test -v ./internal/server/...`: passed
- `go test -v ./cmd/wideboi -run TestUpgradeServer`: passed
- `python3 scripts/attachcheck.py`: 28 passed, 0 failed
- `make check`: 100% passed (fmt-check, lint, seam-check, test, web-test, web-accept, race, verify-exit, smoke, attach-check)
