# wideboi Plan 13 — Detach & Reattach CLI Plumbing

**Date:** 2026-09-19
**Parent spec:** `docs/BEYOND-V1.md` §3
**Status:** In Progress

## Goal

Add CLI plumbing and control mode support for detaching and reattaching sessions over Unix domain sockets:
1. **Control Mode Detach Verb:** `C-b d` detaches the client cleanly without terminating the server or child PTYs.
2. **CLI Commands:**
   - `wideboi server [--socket <path>]`: Runs wideboi server standalone listening on a Unix domain socket.
   - `wideboi attach [--socket <path>]`: Connects a client to a running server over Unix domain socket.
   - `wideboi` (default): Runs in-process or attaches if socket exists.

## Design

### 1. Control Mode Detach
In `cmd/wideboi/router.go`:
- `d` in control mode returns `routeDetach`.
- `u` remains scrollback up. `Ctrl+D` or `Shift+D` scrolls down.
- On `routeDetach`, the client loop exits cleanly with code 0 without signaling or terminating the server.

### 2. Socket-based Client/Server CLI
- Server path: defaults to `/tmp/wideboi-<uid>/<session>.sock`.
- Server runs `transport.NewSocketListener(socketPath)` in `server` command or background mode.
- `attach` command uses `net.Dial("unix", socketPath)` and `transport.NewSocketConn`.
