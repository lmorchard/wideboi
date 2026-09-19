# Plan 10 Implementation Plan — Unix Socket Transport

**Goal:** Implement Unix domain socket transport in `internal/transport/socket.go` with unit test coverage.

## Tasks

### Task 1: `SocketListener` and `SocketDialer` in `internal/transport/socket.go`
- Create `internal/transport/socket.go`.
- Implement JSON-line framing over Unix domain sockets.
- Implement server-side listener and client-side dialer with message channels.

### Task 2: Unit tests in `internal/transport/socket_test.go`
- Create `internal/transport/socket_test.go`.
- Test socket creation, client/server message exchange over Unix socket, and graceful connection teardown.

### Task 3: Verification & PR creation
- Run `make check`.
- Commit, push branch `plan-10-unix-socket-transport`, and open PR.
