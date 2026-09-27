# Implementation Plan: Issues #279 & #272

## Phase 1: Issue #279 - Real Unix Socket Integration Test & Verification
- Task: Add a unit/integration test in `internal/server/double_reader_test.go` that:
  - Creates a Unix socket listener with a short socket path in `os.TempDir()`.
  - Starts `s.ListenSocket(ctx, sl)`.
  - Connects a real Unix socket client via `transport.Handshake` / `transport.NewClientSocketConn`.
  - Sends an `MsgAttach` and multiple sequential `MsgInput` messages *before* `s.Run(ctx)` is invoked.
  - Starts `s.Run(ctx)` in a goroutine.
  - Asserts that all inputs are received and processed without data corruption, duplicate execution, or crashes.
- Verification: Run `go test -v -count=1 ./internal/server -run TestSocketClientAdmittedBeforeRun`.

## Phase 2: Issue #272 - Tag Remote Transports and Restrict Session Control
- Task:
  - In `internal/server/server.go`:
    - Add `remoteTransports map[transport.Transport]bool` field to `Server`. Initialize in `NewServer`.
    - In `ListenWebSocket`, mark `s.remoteTransports[sConn] = true` under `s.mu.Lock()`.
    - In `removeTransportLocked`, clean up `delete(s.remoteTransports, tp)`.
    - Add helper `isRemoteTransport(tp transport.Transport) bool` (under `s.mu.Lock()`).
    - Add helper `setRemoteTransport(tp transport.Transport, remote bool)` for test support.
    - Implement `rejectRemoteClientMsg(ctx context.Context, tp transport.Transport, msg transport.ClientMessage) bool`:
      - If not remote, return false.
      - If `MsgShutdown`: log warning, return true (refusing shutdown).
      - If `MsgUpgradeRequest`: log warning, send `protocol.MsgUpgradeResponse{Error: "upgrade is restricted to local peers"}`, return true.
      - If `MsgWebServerControlRequest`: log warning, send `protocol.MsgWebServerControlResponse{Error: "web server control is restricted to local peers"}`, return true.
    - In `handleClientConnLoop`, invoke `if s.rejectRemoteClientMsg(ctx, tp, msg) { continue }` before `MsgShutdown`, `MsgUpgradeRequest`, and `s.handleClientMsg`.
- Verification: Compile and run existing server unit tests.

## Phase 3: Issue #272 - Tests for Remote Peer Rejection vs Local Peer Acceptance
- Task:
  - Add tests in `internal/server/remote_control_test.go`:
    - `TestRemoteWebSocketPeerCannotShutdown`: WebSocket peer sending `MsgShutdown` does not close server; subsequent messages still work.
    - `TestRemoteWebSocketPeerCannotUpgrade`: WebSocket peer sending `MsgUpgradeRequest` gets `MsgUpgradeResponse` with error; binary is not executed.
    - `TestRemoteWebSocketPeerCannotControlWebServer`: WebSocket peer sending `MsgWebServerControlRequest` gets `MsgWebServerControlResponse` with error; web server is not modified.
    - `TestLocalSocketPeerCanShutdownAndUpgrade`: Unix socket peer sending these requests is not refused with remote restrictions.
- Verification: Run `go test -v -count=1 ./internal/server -run TestRemoteWebSocket`.

## Phase 4: Documentation Updates
- Update `docs/PROTOCOL.md`:
  - Document `MsgUpgradeRequest` and `MsgWebServerControlRequest` in Section 3.1.
  - Document remote peer restriction in Section 4.2 / Section 3.
- Update `docs/LESSONS.md`:
  - Update the "Nothing may run between `ListenSocket` and `Run`" lesson to explain how reader loops are deduplicated per transport.
- Update comment in `cmd/wideboi/main.go:456`.

## Phase 5: Full Project Verification & PR Preparation
- Run `make quick` (fmt-check, lint, seam-check, test, web-test).
- Run `make check` (full suite including race, verify-exit, smoke, attach-check).
- Write `notes.md`.
- Commit changes.
- Push branch and create PR using `gh pr create`.
