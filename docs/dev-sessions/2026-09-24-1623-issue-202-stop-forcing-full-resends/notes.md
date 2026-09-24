# Notes: Stop forcing full pane resends on unattached disconnects and status-only changes

- **Branch:** `issue-202-stop-forcing-full-resends`
- **Worktree:** `.worktrees/issue-202-stop-forcing-full-resends`
- **Issue:** #202

## What Changed

1. **Skip layout broadcast on unattached disconnects:**
   - Added `attachedTransports map[transport.Transport]bool` to `Server`.
   - Transports are registered on `MsgAttach` and unregistered in `removeTransportLocked`.
   - In `dropClient`, `s.broadcastLayout(ctx)` is only invoked if the disconnected transport had actually attached. Connections that only dialed in for queries (e.g. `status`, `status --traffic`, `kill-session`, health probes) close without broadcasting layout or triggering pane resends to attached clients.

2. **Force pane resends only on column set or pane size changes:**
   - Added `lastColumns []protocol.ColumnData` to `Server`.
   - Added `sameColumnSetAndSizes` helper to compare column sets and pane dimensions.
   - In `broadcastLayout`, `columnsChanged := !sameColumnSetAndSizes(cols, s.lastColumns)` is computed and passed to `broadcastPaneUpdates(ctx, columnsChanged)`.
   - Status-only or title-only changes pass `force = false` so unchanged panes are not resent, and active panes send incremental patches.
   - Pane additions, pane removals, and column resizes pass `force = true` so client mirrors can be properly allocated or pruned.

3. **Tests:**
   - `TestUnattachedDisconnectDoesNotResendPanes`: verifies that an unattached peer disconnecting sends no layout snapshot and no pane resends to attached clients.
   - `TestStatusOnlyChangeDoesNotResendUnchangedPanes`: verifies that a status change from idle to working delivers the new status snapshot but does not resend unchanged panes.
   - `TestColumnOrSizeChangeForcesPaneResend`: verifies that column additions or resizes continue to force full resends to refresh client mirrors.

## Verification Evidence

- `go test -v ./internal/server -run "TestStatusOnlyChangeDoesNotResendUnchangedPanes|TestColumnOrSizeChangeForcesPaneResend|TestUnattachedDisconnectDoesNotResendPanes"`: passed
- `go test -race -count=4 ./internal/server`: 4 consecutive runs passed cleanly with 0 races
- `make quick`: passed
- `make check`: passed (unit tests, web vitest, Playwright browser test, verify-exit, smoke 37/37, golden snapshot, attach-check 25/25)

