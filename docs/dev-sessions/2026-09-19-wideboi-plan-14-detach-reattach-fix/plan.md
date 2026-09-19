# Plan 14 Implementation Plan — Socket-based Detach & Reattach Fix

**Goal:** Unify transport under `Transport` interface and wire `wideboi attach` / `wideboi server` to real Unix domain sockets.

## Tasks

### Task 1: Unified Transport Interface & Socket Roles
- Define `Transport` interface in `internal/transport/inproc.go`.
- Implement `ServerSocketConn` and `ClientSocketConn` in `internal/transport/socket.go`.
- Add unit tests in `internal/transport/socket_test.go`.

### Task 2: Server Multi-Client & Socket Listener Support
- Update `Server` in `internal/server/server.go` to accept socket connections via `SocketListener`.
- Send initial `MsgLayoutSnapshot` on `MsgAttach`.

### Task 3: Client & CLI Socket Connection (`cmd/wideboi/main.go`)
- Wire `runAttach` to dial Unix domain socket and run client terminal UI.
- Update `run()` to auto-detect running socket server or start local socket server.
- Test detach (`C-b d`) -> reattach (`wideboi attach`) cycle.

### Task 4: Verification & PR Creation
- Run `make check`.
- Commit, push `fix-detach-reattach-socket`, and open PR.
