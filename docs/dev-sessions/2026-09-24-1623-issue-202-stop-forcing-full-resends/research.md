# Research: Stop forcing full pane resends on unattached disconnects and status-only changes

**Target Issue:** #202 (Stop forcing full pane resends on unattached disconnects and status-only changes)

## Relevant Codebase Findings

### 1. `dropClient` and connection teardown
- `internal/server/server.go:242-273` (`handleClientConnLoop`):
  When a client disconnects or channel closes (`!ok`), or detaches (`MsgDetach`), it calls `s.dropClient(ctx, tp)`.
- `internal/server/server.go:278-301` (`dropClient`):
  ```go
  func (s *Server) dropClient(ctx context.Context, tp transport.Transport) (wasOwner bool) {
      s.mu.Lock()
      s.removeTransportLocked(tp)
      if tp == s.owner {
          s.owner = nil
          wasOwner = true
      }
      s.mu.Unlock()
      // Broadcast layout outside the lock to push the resized panes to remaining clients
      s.broadcastLayout(ctx)
      if cl, ok := tp.(io.Closer); ok {
          _ = cl.Close()
      }
      return wasOwner
  }
  ```
  Every connection, even one that dialed briefly to run `wideboi status`, `status --traffic`, `kill-session`, etc., is added to `s.transports` on accept (`admitSocketConn`, line 236). When it disconnects, `dropClient` calls `s.broadcastLayout(ctx)`.
  Currently, there is no distinction between transports that sent `MsgAttach` and transports that only queried or dropped without attaching.

### 2. `broadcastLayout` and forced pane updates
- `internal/server/server.go:954-1023` (`broadcastLayout`):
  ```go
  func (s *Server) broadcastLayout(ctx context.Context) {
      ...
      s.mu.Lock()
      statuses := s.statusGlyphsLocked()
      titles := s.paneTitlesLocked()
      snapshot := protocol.MsgLayoutSnapshot{
          Columns:      layout.ToColumnData(s.strip.Columns()),
          PaneStatuses: statuses,
          PaneTitles:   titles,
      }
      ...
      if delivered {
          s.mu.Lock()
          s.lastStatuses = statuses
          s.lastTitles = titles
          s.mu.Unlock()
      }

      s.broadcastPaneUpdates(ctx, true)
  }
  ```
  `broadcastLayout` unconditionally calls `s.broadcastPaneUpdates(ctx, true)`.
  The comment on line 1025 states:
  > force sends every pane to every client: broadcastLayout needs that, because a snapshot can prune a client's mirror or replace it with a blank one (client.go, the MsgLayoutSnapshot case), so an unchanged pane must still be resent after one.

### 3. What prunes or replaces client mirrors
- `internal/client/client.go:204-244` (`HandleServerMsg` for `MsgLayoutSnapshot`):
  - Client mirrors (`c.mirrors[p.PaneID]`) are allocated when `!ok` (new pane in layout) or when a pane's dimensions grow (`p.Src.Dx() > m.Cols || p.Src.Dy() > m.Rows`).
  - Mirrors are pruned only when a pane is no longer in `snapshot.Columns`: `live[col.PaneID] = true; if !live[id] { delete(c.mirrors, id) }`.
  - Status changes (`m.PaneStatuses`) and title changes (`m.PaneTitles`) do NOT prune or replace mirrors.
  - Web client (`web/src/wideboi-app.ts:344-372`) similarly only prunes/reorders panes on column list changes.

### 4. Status change triggers
- `internal/server/server.go:931-952` (`broadcastLayoutIfStatusChanged`):
  Runs on every 33ms frame tick in `Server.Run` (`server.go:347-355`).
  Checks if `statusGlyphsLocked()` or `paneTitlesLocked()` changed compared to `lastStatuses` or `lastTitles`.
  If changed, it calls `s.broadcastLayout(ctx)`, which then calls `s.broadcastPaneUpdates(ctx, true)`.
  Typing in a shell causes status flips between working and idle (via OSC 133), triggering forced full updates on every flip.

### 5. `broadcastPaneUpdates` behavior with `force`
- `internal/server/server.go:1031-1153`:
  When `force == true`:
  - `dirty := force || ...` -> evaluates to true for every pane for every transport.
  - `hasBase = hasBase && hasWireGen && !force` -> `hasBase` becomes false.
  - With `hasBase == false`, the server sends a full `MsgPaneUpdate` instead of a `MsgPanePatch`.
  - When `force == false`:
    - `dirty` is false for any pane where `hasWireGen && wireGen == lastWireGen && !offsetChanged && !unreadChanged`. Unchanged panes send 0 bytes.
    - If a pane did change, `hasBase` is preserved so it sends a patch (`MsgPanePatch`).
