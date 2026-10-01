# Notes: Column resize content blanking in CLI client

- Worktree: `.worktrees/column-resize-blanking`
- Branch: `column-resize-blanking`
- Session: `docs/dev-sessions/2026-10-01-0054-column-resize-blanking/`
- PR: #355

## Summary of Root Cause and Fix

### Root Cause
1. **Server-side `sizeOwner` tracking gap**:
   - When a server runs detached or the previous `sizeOwner` disconnects, `s.sizeOwner` becomes `nil`.
   - On subsequent attach, `handleAttachLocked` checked `s.sizeOwner == nil && s.rows == 0`. Because `s.rows != 0`, `s.sizeOwner` was never set and remained `nil`.
   - With `s.sizeOwner == nil`, any attached client's `MsgSetPaneWidth` or `VerbCycleWidth`/`VerbGrowWidth`/`VerbShrinkWidth` was rejected by the server because `tp == s.sizeOwner` was false.
   - The server's pane grid and column width remained unchanged on the server.
2. **Client-side mirror wiping in `applySnapshotLocked`**:
   - In the CLI client, column resizing updated local `c.displayWidths[id]` and placements.
   - ~3 seconds after typing stopped, the pane's status flipped to `StatusIdle` (OSC 133 / idle decay timeout), triggering `MsgLayoutSnapshot`.
   - In `applySnapshotLocked`, `for _, p := range c.placements` checked `p.Src.Dx() > m.Cols`. Because local display width exceeded server PTY width, it called `ensureMirrorLocked(p.PaneID, w, h)`, which created a brand new blank surface (`compose.NewSurface(w, h)`), wiping out all existing rendered cells, and deleted `c.paneUpdates[p.PaneID]`.
   - Because the snapshot was a status change, the server did not force a pane resend (`columnsChanged == false`), leaving the pane mirror completely blank.

### Fix
1. In `internal/client/viewport.go`:
   - Updated `applySnapshotLocked` to only allocate a mirror if one doesn't exist yet (`!ok`). It no longer reallocates existing mirrors or deletes `c.paneUpdates`.
   - Updated `ensureMirrorLocked` so that if an existing mirror is ever resized, existing cell contents are preserved rather than replaced with blanks.
2. In `internal/server/handlers.go`:
   - Updated `handleAttachLocked` to assign `s.sizeOwner = tp` if `s.sizeOwner == nil && (s.rows == 0 || s.attachedCountLocked() == 1)`.
   - Updated `handleResizeLocked` to assign `s.sizeOwner = tp` if `s.sizeOwner == nil && s.attachedCountLocked() <= 1 && len(s.transports) <= 1`.
   - Retained `tp == s.sizeOwner` enforcement so attached viewers without ownership cannot mutate shared PTY dimensions without claiming size.
   - Updated `handleVerbLocked` to set `eff.needBroadcast = true` on width verbs.
3. Added regression tests:
   - `internal/client/screen_test.go`: `TestResizeColumnDoesNotBlankOnSubsequentSnapshot`
   - `internal/server/sizing_test.go`: `TestReattachingClientBecomesSizeOwner` (using timeout loop for disconnect transition)

### Copilot Review
- Addressed Copilot's review findings:
  1. Maintained `tp == s.sizeOwner` in `handleSetPaneWidthLocked` and `handleVerbLocked` so remaining viewers cannot resize shared PTY geometry when `sizeOwner == nil`.
  2. Replaced fixed 50ms sleep in `TestReattachingClientBecomesSizeOwner` with bounded polling on `srv.SizeOwner() == nil`.

### Verification
- `go test -count=1 ./...` passed.
- `make quick` passed.
- `make check` passed with all targets (fmt-check, lint, seam-check, test, web-test, web-accept, race, verify-exit, smoke, golden, attach-check).
