# Stop forcing full pane resends on unattached disconnects and status-only changes Spec

**Goal:** Eliminate redundant forced full pane updates when unattached socket connections disconnect or when layout snapshots change only status or titles, reducing unnecessary network/socket traffic and client draw overhead.

**Source:** https://github.com/lmorchard/wideboi/issues/202

## Current state

Today, every layout broadcast ends with `s.broadcastPaneUpdates(ctx, true)` (`internal/server/server.go:1022`), which forces an uncompressed, unpatched full resend of every pane to every connected client.

This forced full resend fires in two common scenarios where no pane layout or size changed:
1. **Unattached socket disconnects:** Any socket peer (e.g. `wideboi status`, `wideboi kill-session`, or health checks) connects, is added to `s.transports` (`internal/server/server.go:236`), and when it disconnects, `dropClient` (`internal/server/server.go:278-301`) unconditionally invokes `s.broadcastLayout(ctx)`.
2. **Status/title changes:** When a pane transitions between working and idle (e.g., via OSC 133 prompt markers during typing) or updates its title, `broadcastLayoutIfStatusChanged` (`internal/server/server.go:931-952`) triggers `s.broadcastLayout(ctx)`.

In both cases, `client.go` does not prune or reallocate any pane mirrors, because the column set and pane dimensions are unchanged (`internal/client/client.go:204-244`). The forced resend sends full pane snapshots for all panes to all clients, which accounts for significant unnecessary traffic (e.g., 28% of bytes at 80x24, 44% at 160x48 in typical typing sessions).

## Desired end state

1. **Unattached disconnects do not broadcast layout:**
   The server tracks which transports have attached via `MsgAttach`. When a transport disconnects in `dropClient`, the server only calls `broadcastLayout(ctx)` if that transport had actually attached. If it never attached, `dropClient` cleans up the transport without broadcasting layout.
2. **Forced pane resends only occur on column set or pane size changes:**
   `broadcastLayout` tracks the last broadcast column set and dimensions (`lastColumns []protocol.ColumnData`).
   It only forces full pane resends (`broadcastPaneUpdates(ctx, true)`) when the column set or pane sizes have changed (i.e. panes added, removed, or resized).
   If only statuses, titles, or non-geometry attributes changed, `broadcastLayout` calls `broadcastPaneUpdates(ctx, false)`: unchanged panes send nothing, and active panes send incremental patches.
3. **Verification:**
   Unit tests pin that:
   - An unattached connection disconnecting does not resend panes to attached clients.
   - A status-only or title-only layout broadcast does not resend unchanged panes.
   - Column additions, removals, and pane resizes still force full pane updates.

## Design decisions

- **Decision:** Track attached status via `attachedTransports map[transport.Transport]bool` on `Server`.
  - **Why:** Simple, explicit, and localized. When `MsgAttach` is received in `handleClientMsg`, the transport is marked attached. When `dropClient` is called, it checks and clears this flag.
  - **Rejected:** Checking if `s.clientSizes[tp]` exists. While `clientSizes` is set during `MsgAttach` with positive cols/rows, a client could theoretically attach with zero sizes (as in tests like `handshake_test.go`), so an explicit boolean set is clearer and avoids coupling to size tracking.

- **Decision:** Compare column set and sizes via a helper `sameColumnSetAndSizes(a, b []protocol.ColumnData) bool`.
  - **Why:** Client mirror allocation and pruning in `internal/client/client.go:204-244` depends strictly on the set of pane IDs and their dimensions (`Dx()`, `Dy()`). A status change or title change preserves column set and sizes. Reordering columns (e.g. `VerbMoveLeft`/`VerbMoveRight`) also preserves the set and dimensions and does not require mirror re-allocation.
  - **Rejected:** Slice equality `slices.Equal(a, b)`. While slice equality would work for status/title changes, column reorders would still trigger a forced full resend even though mirror surfaces do not need to be replaced.

- **Decision:** Keep `lastColumns` tracking aligned with `lastStatuses` and `lastTitles` delivery semantics.
  - **Why:** `lastStatuses` and `lastTitles` are only updated when `delivered` is true (`internal/server/server.go:1015-1020`). If a broadcast fails to deliver to all clients, leaving `lastColumns` un-updated ensures that subsequent ticks retry with `force = true`.

## Patterns to follow

- State comparison helpers in `internal/server/server.go`:
  - `sameStatusMap` (`internal/server/server.go:94`)
  - `sameStringMap` (`internal/server/server.go:105`)
- Layout snapshot delivery and tracking in `internal/server/server.go:954-1023`:
  - `s.lastStatuses` and `s.lastTitles` pattern under `s.mu` and `delivered` check.
- Pane update verification tests in `internal/server/paneupdate_test.go`:
  - `drainPaneUpdates` (`internal/server/paneupdate_test.go:373-389`)
  - `TestUnchangedPanesAreNotResent` (`internal/server/paneupdate_test.go:399-421`)

## What we're NOT doing

- We are NOT modifying the client-side mirror management logic in `internal/client/client.go` or `web/src/wideboi-app.ts`.
- We are NOT removing `broadcastLayout` or changing the wire format of `MsgLayoutSnapshot`.
- We are NOT changing the behavior of `broadcastPaneUpdates(ctx, false)` itself (which already skips unchanged panes and uses diff patches when `force == false`).
- We are NOT modifying the transport implementations or handshake logic.

## Open questions

None.
