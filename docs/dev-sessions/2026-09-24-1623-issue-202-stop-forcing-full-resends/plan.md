# Stop forcing full pane resends on unattached disconnects and status-only changes Plan

**Goal:** Eliminate redundant forced full pane updates when unattached socket connections disconnect or when layout snapshots change only status or titles.

**Approach:**
1. Track attached connections in `Server.attachedTransports`. In `dropClient`, only broadcast layout if the disconnecting transport had attached (`MsgAttach`).
2. Track `Server.lastColumns []protocol.ColumnData`. In `broadcastLayout`, compare current column set and dimensions against `lastColumns` using `sameColumnSetAndSizes`. Pass `columnsChanged` to `broadcastPaneUpdates(ctx, force)`.
3. Pin behavior with focused unit tests in `internal/server/`.

**Tech stack:** Go, standard library, wideboi internal packages (`internal/server`, `internal/protocol`, `internal/layout`, `internal/transport`).

---

## Phase 1: Skip layout broadcast on unattached disconnect

**Files:**
- Modify: `internal/server/server.go` — track `attachedTransports`, skip `broadcastLayout` in `dropClient` if unattached
- Test: `internal/server/paneupdate_test.go` — `TestUnattachedDisconnectDoesNotResendPanes`

**Key changes:**
In `internal/server/server.go`:
```go
type Server struct {
    ...
    attachedTransports map[transport.Transport]bool
}
```
In `removeTransportLocked(tp transport.Transport)`:
```go
    delete(s.attachedTransports, tp)
```
In `dropClient(ctx context.Context, tp transport.Transport)`:
```go
func (s *Server) dropClient(ctx context.Context, tp transport.Transport) (wasOwner bool) {
    s.mu.Lock()
    attached := s.attachedTransports != nil && s.attachedTransports[tp]
    s.removeTransportLocked(tp)
    if tp == s.owner {
        s.owner = nil
        wasOwner = true
    }
    s.mu.Unlock()
    if attached {
        s.broadcastLayout(ctx)
    }
    if cl, ok := tp.(io.Closer); ok {
        _ = cl.Close()
    }
    return wasOwner
}
```
In `handleClientMsg(ctx context.Context, tp transport.Transport, msg transport.ClientMessage)`:
```go
    case protocol.MsgAttach:
        if s.attachedTransports == nil {
            s.attachedTransports = make(map[transport.Transport]bool)
        }
        if tp != nil {
            s.attachedTransports[tp] = true
        }
```

**Verification — automated:**
- [ ] Failing test written and verified red: `go test ./internal/server -run TestUnattachedDisconnectDoesNotResendPanes`
- [ ] Implementation applied and test verified green
- [ ] `make quick` passes

---

## Phase 2: Force pane resends only on column set or pane size changes

**Files:**
- Modify: `internal/server/server.go` — add `lastColumns`, `sameColumnSetAndSizes`, update `broadcastLayout`
- Test: `internal/server/paneupdate_test.go` — `TestStatusOnlyChangeDoesNotResendUnchangedPanes`, `TestColumnOrSizeChangeForcesPaneResend`

**Key changes:**
In `internal/server/server.go`:
```go
type Server struct {
    ...
    lastColumns []protocol.ColumnData
}

func sameColumnSetAndSizes(a, b []protocol.ColumnData) bool {
    if len(a) != len(b) {
        return false
    }
    m := make(map[int]protocol.ColumnData, len(a))
    for _, col := range a {
        m[col.PaneID] = col
    }
    for _, col := range b {
        other, ok := m[col.PaneID]
        if !ok || other.Width != col.Width || other.Height != col.Height {
            return false
        }
    }
    return true
}
```
In `broadcastLayout(ctx context.Context)`:
```go
    s.mu.Lock()
    statuses := s.statusGlyphsLocked()
    titles := s.paneTitlesLocked()
    cols := layout.ToColumnData(s.strip.Columns())
    columnsChanged := !sameColumnSetAndSizes(cols, s.lastColumns)
    snapshot := protocol.MsgLayoutSnapshot{
        Columns:      cols,
        PaneStatuses: statuses,
        PaneTitles:   titles,
    }
    ...
    if delivered {
        s.mu.Lock()
        s.lastStatuses = statuses
        s.lastTitles = titles
        s.lastColumns = cols
        s.mu.Unlock()
    }

    s.broadcastPaneUpdates(ctx, columnsChanged)
```

**Verification — automated:**
- [ ] Failing tests written and verified red: `go test ./internal/server -run "TestStatusOnlyChangeDoesNotResendUnchangedPanes|TestColumnOrSizeChangeForcesPaneResend"`
- [ ] Implementation applied and tests verified green
- [ ] `make quick` passes

---

## Phase 3: Full suite verification

**Files:**
- All tests

**Verification — automated:**
- [ ] `make quick` passes
- [ ] `make check` passes
- [ ] `go test -race -count=4 ./internal/server` passes repeatedly
