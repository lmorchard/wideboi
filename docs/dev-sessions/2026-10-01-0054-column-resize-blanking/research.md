# Research: Column resize content blanking in CLI client

## Symptoms

1. Resizing a column in the CLI client (e.g., via `w`, `o`, `p`, or palette) causes the pane's contents to blank out a short time ("a little while") after the change.
2. The issue was observed particularly when connected to a remote devbox via SSH where wideboi is running.

## Root Cause Analysis

We identified two interacting bugs that cause this issue:

### 1. Server-side: `sizeOwner` is `nil` after reattaching to a detached session
- When wideboi server is running detached (or started via `wideboi server` in the background) and a client connects, or when the previous `sizeOwner` disconnected:
  - Disconnect clears `s.sizeOwner = nil` in `internal/server/clients.go:removeClientLocked`.
  - When a client attaches, `internal/server/handlers.go:handleAttachLocked` checks:
    ```go
    if s.sizeOwner == nil && s.rows == 0 {
        s.sizeOwner = tp
        s.cols = m.Cols
        s.rows = m.Rows
    }
    ```
  - Because `s.rows != 0` (the session already had rows established), `s.sizeOwner` is NOT set. It remains `nil` even though this client is the only client attached to the session!
  - When the client attempts to resize a column:
    - `handleSetPaneWidthLocked` checks `if (tp == s.sizeOwner || !s.isAttachedLocked(tp))`.
    - Because `s.sizeOwner` is `nil` and `tp` IS attached, this condition fails.
    - The server silently ignores `MsgSetPaneWidth` (and `VerbCycleWidth`/`VerbGrowWidth`/`VerbShrinkWidth`).
    - The server's pane grid and column width remain at their old size (e.g. 25 or 80 cols).

### 2. Client-side: `applySnapshotLocked` wipes existing mirror on layout snapshot
- In the CLI client, pressing resize updates the local `c.displayWidths[id]` and computes local placements with the new larger width (e.g. 35 or 100 cols).
- Initially, `BlitSource` renders the existing mirror (e.g. 25 or 80 cols) padded with spaces, so the user initially sees the content.
- ~3 seconds later, the pane's status flips to `StatusIdle` (via OSC 133 or idle decay timeout in `term/grid.go`).
- The status change triggers `broadcastLayoutIfStatusChanged` on the server, which sends `MsgLayoutSnapshot`.
- In `internal/client/viewport.go:139-151`:
  ```go
  for _, p := range c.placements {
      m, ok := c.mirrors[p.PaneID]
      if !ok || p.Src.Dx() > m.Cols || p.Src.Dy() > m.Rows {
          w := max(p.Src.Dx(), 40)
          h := max(p.Src.Dy(), 20)
          if ok {
              w = max(m.Cols, p.Src.Dx())
              h = max(m.Rows, p.Src.Dy())
          }
          c.ensureMirrorLocked(p.PaneID, w, h)
          delete(c.paneUpdates, p.PaneID)
      }
  }
  ```
- Because local `p.Src.Dx()` (e.g. 35) > `m.Cols` (e.g. 25), `applySnapshotLocked` calls `ensureMirrorLocked`.
- `ensureMirrorLocked` creates a brand new blank surface (`compose.NewSurface(w, h)`), completely discarding all existing terminal cells!
- It also calls `delete(c.paneUpdates, p.PaneID)`.
- Because the server sent this snapshot due to a status change, `columnsChanged` was false on the server, so `broadcastPaneUpdates(ctx, false)` did NOT send a full pane update.
- The client receives NO new pane update, leaving the pane's mirror completely blank!
- Furthermore, any subsequent `MsgPanePatch` fails to apply because `c.paneUpdates[p.PaneID]` was deleted, requiring a resync.

### Reproduction
- Unit test in `internal/client/screen_test.go`: `TestResizeColumnDoesNotBlankOnSubsequentSnapshot` fails with blank pane on snapshot.
- Unit test in `internal/server/sizing_test.go`: `TestReattachingClientBecomesSizeOwner` fails because `sizeOwner` is nil when client attaches to detached session.
