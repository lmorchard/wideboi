# Plan 12 Implementation Plan — Client-Side Placement Calculation

**Goal:** Implement client-side `ComputePlacements` calculation from `ColumnData` received in `MsgLayoutSnapshot`.

## Tasks

### Task 1: Protocol & Layout updates
- Update `internal/protocol/messages.go` with `ColumnData` and `Columns` field in `MsgLayoutSnapshot`.
- Add `ToColumnData` and `SyncColumns` in `internal/layout/layout.go`.
- Register `ColumnData` with `gob.Register` in `internal/transport/socket.go`.

### Task 2: Server layout broadcast
- Update `broadcastLayoutLocked` in `internal/server/server.go` to include `Columns`.

### Task 3: Client-side placement calculation
- Add local `strip *layout.Strip` to `Client` in `internal/client/client.go`.
- Compute placements locally on `MsgLayoutSnapshot` and `SendResize`.

### Task 4: Verification & PR creation
- Add client unit tests in `internal/client/client_test.go`.
- Run `make check`.
- Commit, push branch `plan-12-client-side-placement`, and open PR.
