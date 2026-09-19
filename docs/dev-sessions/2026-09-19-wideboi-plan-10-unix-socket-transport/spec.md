# wideboi Plan 10 — Unix Socket Transport

**Date:** 2026-09-19
**Parent spec:** `docs/BEYOND-V1.md` §3
**Status:** In Progress

## Goal

Add Unix domain socket transport support in `internal/transport/socket.go`, enabling client/server communication across a Unix domain socket for detach and reattach capabilities.

## Architecture & Design

1. **`SocketListener` & `SocketConn` (`internal/transport/socket.go`):**
   - Unix domain socket server (`net.Listen("unix", socketPath)`).
   - Frame encoding/decoding for `ClientMessage` and `ServerMessage` using JSON codec.
   - Clean socket cleanup on listener close.

2. **`SocketDialer` (`internal/transport/socket.go`):**
   - Connects to Unix domain socket (`net.Dial("unix", socketPath)`).
   - Marshals client messages and unmarshals server messages.

3. **Testing:**
   - Unit tests in `internal/transport/socket_test.go` testing socket creation, message round-tripping, reconnect handling, and cleanup.
