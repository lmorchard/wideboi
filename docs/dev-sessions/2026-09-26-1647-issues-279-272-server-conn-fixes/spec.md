# Server Connection Security & Concurrency Fixes (Issues #279 & #272) Spec

**Goal:** Ensure client connections admitted before `srv.Run` never start duplicate reader loops (ordering guarantees and input integrity), and restrict sensitive session-control messages (`MsgShutdown`, `MsgUpgradeRequest`, `MsgWebServerControlRequest`) from remote WebSocket peers.

**Source:** GitHub Issues #279 and #272.

## Current state
- **Issue #279:**
  - `runServer` in `cmd/wideboi/main.go` starts `srv.ListenSocket(ctx, sl)` before `srv.Run(ctx)`.
  - Previously, `Run` snapshotted `s.transports` and started a loop unconditionally for each transport, creating a second reader loop for connections admitted between `ListenSocket` and `Run`.
  - PR #284 introduced `startedTransports map[transport.Transport]bool` and `startTransportLoopLocked(ctx, tp)` in `internal/server/server.go`.
  - A comprehensive integration test with a real Unix socket admitting a client before `Run()` and asserting ordered message handling without duplicate loops or race conditions is needed to pin this behavior and prevent regressions.
  - `docs/LESSONS.md` and `cmd/wideboi/main.go` still reference the open race as an interim restriction.
- **Issue #272:**
  - `handleClientConnLoop` in `internal/server/server.go` serves both Unix socket and WebSocket connections.
  - Any peer, including remote WebSocket peers holding the web token, can send:
    - `MsgShutdown`: triggers `s.CloseFor(ReasonShutdownRequest)`.
    - `MsgUpgradeRequest`: calls `s.PrepareUpgrade` and `s.ExecUpgrade`, executing an arbitrary binary path as the server.
    - `MsgWebServerControlRequest`: queries or mutates the web server (start/stop/TLS/token rotation).
  - Remote WebSocket peers should not be permitted to shut down the server, exec arbitrary binaries, or alter server TLS/tokens.

## Desired end state
1. **Issue #279 (Pre-Run Reader Loop):**
   - Connections admitted before `srv.Run` receive exactly one reader loop.
   - An integration test with a real Unix socket (`TestSocketClientAdmittedBeforeRunKeepsMessageOrder`) asserts that typing multiple messages/keystrokes before `srv.Run` starts preserves strict order and does not drop or duplicate messages.
   - Documentation in `docs/LESSONS.md` and comment in `cmd/wideboi/main.go` are updated to reflect the resolved race.
2. **Issue #272 (Remote Session-Control Restriction):**
   - Transports are tagged as remote at admission: `remoteTransports map[transport.Transport]bool` in `Server`.
   - In `ListenWebSocket`, accepted WebSocket transports are marked as remote.
   - A centralized rejection handler (`rejectRemoteClientMsg`) intercepts restricted messages before execution:
     - `MsgShutdown`: refused; server logs a warning and does not shut down.
     - `MsgUpgradeRequest`: refused; server returns `MsgUpgradeResponse{Error: "upgrade is restricted to local peers"}` and does not exec or terminate.
     - `MsgWebServerControlRequest`: refused; server returns `MsgWebServerControlResponse{Error: "web server control is restricted to local peers"}` and does not modify the web server.
   - Unix socket connections retain full permissions to perform all three actions.
   - Tests in `internal/server` verify that WebSocket peers receive refusals for all three messages, while Unix socket peers succeed.
   - `docs/PROTOCOL.md` documents this security restriction in the client message table and WebSocket section.

## Design decisions
- **Decision:** Track remote transports via `remoteTransports map[transport.Transport]bool` in `Server`.
  - **Why:** Fits project patterns alongside `attachedTransports`, `startedTransports`, and `peerPIDs`. Managed cleanly under `s.mu`.
  - **Rejected:** Modifying `transport.Transport` interface (which is a general abstraction used across packages and would widen interface requirements).
- **Decision:** Centralized check in `handleClientConnLoop` via `rejectRemoteClientMsg`.
  - **Why:** Keeps the security policy for remote peers in one single place before message dispatch.
  - **Rejected:** Scattering remote checks across `handleClientConnLoop` and `handleClientMsg`.
- **Decision:** Do not add a new wire message for `MsgShutdown` rejection; instead log a warning and ignore.
  - **Why:** `MsgShutdown` has no response in the protobuf schema. Introducing a new message would require a wire schema change and bumping `protocol.Version`. Ignoring `MsgShutdown` from remote peers cleanly keeps the session alive.

## Patterns to follow
- Transport bookkeeping: `internal/server/server.go:387-390, 510-515` (`startedTransports`, `peerPIDs`).
- WebSocket testing: `internal/server/websocket_auth_test.go:42-65` (`httptest.NewServer`, gorilla dialer with subprotocol).
- Socket listener testing: `internal/server/lifecycle_test.go:191-201` (`transport.NewSocketListener` with temp dir).

## What we're NOT doing
- We are not altering the protobuf schema or bumping `protocol.Version` (existing response messages already carry `Error string`).
- We are not changing how authentication tokens or TLS certificates are generated.
- We are not restricting regular terminal operations (resize, verb, mouse, input, resync, status, attach, detach) for WebSocket peers.

## Open questions
None. Both issues have precise specifications and acceptance criteria.
