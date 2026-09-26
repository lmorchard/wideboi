# Notes: Issue 247: Enable and disable a session web server after launch

- **Branch:** `issue-247-web-server-toggle`
- **Worktree:** `.worktrees/issue-247-web-server-toggle`

## Summary of Changes

1. **Protocol & Version Bump (v14):**
   - Added `MsgWebServerControlRequest` with `WebServerAction` (`STATUS`, `START`, `STOP`).
   - Added `MsgWebServerControlResponse` with running state, address, URL, token, TLS state, and error.
   - Bumped wire version to `protocol.Version = 14` and TypeScript subprotocol to `wideboi.v14` (as `origin/main` had advanced to v13 for session CWD).
   - Recompiled protobuf with `make proto` and validated with `make proto-check`.

2. **Server-Side Web Server Manager (`internal/server`):**
   - Implemented `webServerManager` in `internal/server/web.go` to manage the HTTP/WebSocket listener, TLS certificates, token files, and active WebSocket transports.
   - `StartWebServer`: binds address (defaulting to configured addr or `127.0.0.1:0`), wraps with TLS (unless disabled), handles static assets and `/ws` handler, writes `<socket>.web-token` (0600), and outputs URL. Handles port conflicts cleanly without affecting running session.
   - `StopWebServer`: shuts down HTTP server, closes listener, removes `.web-token` file, and disconnects active WebSocket clients via `disconnectWebSocketClients()` without touching Unix domain socket / terminal clients.
   - `WebServerStatus`: reports running state, listener address, browser URL, TLS status, and token.
   - Hooked `MsgWebServerControlRequest` in `handleClientMsg` and server teardown in `Server.Close()`.

3. **CLI Subcommand `wideboi web` & Status JSON (`cmd/wideboi`):**
   - Added `wideboi web [status|start|stop]`.
   - `wideboi web status [--json]`: displays current web server state.
   - `wideboi web start [--addr|-a <addr>] [--rotate-token|-r] [--disable-tls] [--json]`: starts or updates web server.
   - `wideboi web stop [--json]`: stops web server.
   - `wideboi status --json`: includes `web_server` object in `statusOutput`.

4. **Documentation & Tests:**
   - Documented in `docs/MANUAL.md` and `README.md`.
   - Comprehensive test suite covering lifecycle, token reuse and rotation, port conflicts, WebSocket disconnection isolation, RPC over Unix sockets, and CLI commands.
   - Full gate validation with `make check` passed (all Go tests, web tests, browser accept tests, race detector, pty smoke, and exit verification).
