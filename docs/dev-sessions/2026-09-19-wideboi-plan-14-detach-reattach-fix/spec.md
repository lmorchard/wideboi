# wideboi Plan 14 — Socket-based Detach & Reattach Fix

**Date:** 2026-09-19
**Status:** In Progress

## Goal

Fix `wideboi attach` and `wideboi server` so `attach` connects to a running background server via Unix domain socket instead of starting a new in-process session.

## Architecture

1. **Transport Abstraction (`internal/transport/`):**
   - Define `Transport` interface:
     ```go
     type Transport interface {
         SendClient(ctx context.Context, msg ClientMessage) bool
         SendServer(ctx context.Context, msg ServerMessage) bool
         ClientSendChan() <-chan ClientMessage
         ServerSendChan() <-chan ServerMessage
     }
     ```
   - Implement `InProcChannel`, `ServerSocketConn`, and `ClientSocketConn`.

2. **Server Multi-Client / Socket Support (`internal/server/server.go`):**
   - Server manages client connections (both `InProcChannel` and `SocketListener`).
   - Handles `MsgAttach` by sending initial layout snapshot to the connecting client.
   - Broadcasts layout snapshot to all active client connections.

3. **Client Socket Attach (`cmd/wideboi/main.go`):**
   - `runAttach`: Dials Unix domain socket at `socketPath`.
   - Constructs `ClientSocketConn`, attaches client, and handles `C-b d` (detach) by exiting client UI while leaving background server running.
   - Default `run()`: Dials socket if server is already running; if no server is running, auto-spawns server / socket listener and attaches.

4. **Testing & Acceptance:**
   - Unit tests for socket transport roundtrips and multi-client broadcasts.
   - `make check` (all unit, race, smoke, and golden tests passing).
