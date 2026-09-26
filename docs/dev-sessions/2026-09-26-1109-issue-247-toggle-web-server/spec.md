# Enable and Disable Session Web Server After Launch Spec

**Goal:** Enable a running wideboi session to start, stop, and inspect its HTTP/WebSocket server dynamically via CLI commands and RPC without restarting the session.

**Source:** https://github.com/lmorchard/wideboi/issues/247

## Current state

- Web server configuration (`--websocket`, `--websocket-token`, `--tls`, `--disable-tls`) is set at launch and evaluated in `cmd/wideboi/main.go:408-468`.
- If started with `--websocket`, `runServer` sets up `net.Listen`, TLS wrapping, and `http.Server`, passing an `http.ServeMux` to `srv.ListenWebSocket` (`internal/server/server.go:2129-2183`).
- If launched without `--websocket`, no HTTP or WebSocket listener is created, and there is no mechanism to start one later without ending the session.
- Once launched, the web server cannot be stopped without terminating the entire session server.
- The web server address, port, and token are printed to stderr at startup (`cmd/wideboi/main.go:525-538`) and auto-generated tokens are written to `<socket>.web-token` (`cmd/wideboi/main.go:501-521`), but neither `wideboi status` nor any RPC reports web server state.

## Desired end state

1. **CLI Subcommand `wideboi web`**:
   - `wideboi web status [--json]`: Queries and displays current web server state (running / disabled, address, URL with token fragment, TLS status). With `--json`, outputs a machine-readable JSON object.
   - `wideboi web start [--addr <addr>] [--rotate-token] [--disable-tls]`: Enables / starts the web server. If `--addr` is omitted, reuses the configured address or defaults to `127.0.0.1:0`. Outputs the listening URL with token on success. If a port conflict or listen failure occurs, reports an error without stopping or harming the session.
   - `wideboi web stop`: Disables the web server, closes the HTTP listener, disconnects connected WebSocket clients (without affecting Unix socket / terminal clients), and removes the `.web-token` file.
   - Running `wideboi web` with no arguments defaults to `wideboi web status`.
2. **Status Reporting Integration**:
   - `wideboi status --json` includes a `web_server` object reflecting current web server state.
3. **RPC & Protocol**:
   - Client sends `MsgWebServerControlRequest` with `action` (`STATUS`, `START`, `STOP`), optional `addr`, `token`, `rotate_token`, `disable_tls`.
   - Server responds with `MsgWebServerControlResponse` containing `running`, `addr`, `url`, `tls_enabled`, `token`, and `error`.
   - Wire protocol bumped to version 14 (`protocol.Version = 14` and `wideboi.v14` in TypeScript client, after version 13 added session CWD).
4. **Token Handling**:
   - On restart after stop, reuses the existing token by default unless `--rotate-token` is specified or a token is explicitly provided.
   - If no token was ever set, generates a new 32-hex-character token.
   - Token is masked in server logs (`***REDACTED***`) and passed in URL fragment (`#token=...`) so browsers strip it from history.
5. **Isolation**:
   - Disabling web access closes only the HTTP listener and WebSocket transport instances (`transportKind == "websocket"`). Terminal attach, Unix socket clients, and desktop bridge clients remain connected and fully operational.

## Design decisions

- **Decision:** Introduce a dedicated `wideboi web [start|stop|status]` subcommand surface.
  - **Why:** Keeps web server management intuitive and grouped, matching standard CLI ergonomics while allowing future extensions (e.g. `wideboi web url`, `wideboi web cert`).
  - **Rejected:** Overloading top-level flags on `wideboi status` or `wideboi server`.

- **Decision:** Address selection defaults to configured/previous address, or `127.0.0.1:0` if never configured.
  - **Why:** `127.0.0.1:0` allows the OS to select an available ephemeral loopback port, avoiding conflicts between multiple running sessions on the same machine.
  - **Rejected:** Defaulting to a fixed port like 8080 which easily collides when running multiple sessions.

- **Decision:** Retain access token across stop/start by default; provide `--rotate-token`.
  - **Why:** If a user temporarily toggles the web server or adjusts settings, existing browser bookmarks/tabs don't break unless rotation is explicitly requested for security.
  - **Rejected:** Rotating token on every start, which would force re-copying URLs after every toggle.

- **Decision:** Manage web server lifecycle inside `internal/server.Server` (or a dedicated manager struct owned by `Server`).
  - **Why:** `Server` receives control RPCs over Unix sockets, knows about active transports, and can close WebSocket transports directly while keeping socket transports open.
  - **Rejected:** Keeping web server solely in `cmd/wideboi/main.go`, which would make dynamic control via Unix socket RPCs clumsy or require IPC callback channels.

- **Decision:** Wire protocol version bumped to 14.
  - **Why:** Adding new messages to `ClientMessage` and `ServerMessage` changes wire encoding (version 13 was used on main for session CWD). Per `docs/LESSONS.md` and `docs/PROTOCOL.md`, any change to the wire protocol requires bumping `protocol.Version` and the WebSocket subprotocol version.
  - **Rejected:** Trying to sneak control messages outside the wire protocol.

## Patterns to follow

- **Synchronous RPC pattern:** `cmd/wideboi/control.go:25-71` (`rpcQuery` / `rpcOn`) for sending requests and receiving typed responses over the Unix domain socket.
- **RPC handlers in server:** `internal/server/server.go:500-580` (`handleClientMsg`) for handling requests and generating responses.
- **Client transport tracking & disconnection:** `internal/server/traffic.go:126-135` (`transportKind`) and `internal/server/server.go:346-432` (`dropClient`, `removeTransportLocked`).
- **Token writing & removal:** `cmd/wideboi/main.go:501-521` (`webTokenPath`, `writeWebToken`).
- **TLS generation:** `internal/transport/tls.go:114-140` (`LoadOrGenerateTLSConfig`).

## What we're NOT doing

- Not building a GUI desktop app or desktop release packaging in this repo (deferred to desktop client repo / issue #123 follow-ups).
- Not adding multi-user authentication or role-based access control (the single session token model remains).
- Not introducing automatic port-hunting retry loops if a specific port is explicitly requested (if the requested port is bound, return the port conflict error immediately).
- Not changing how terminal or Unix socket clients authenticate or connect.

## Open questions

- None. All product questions resolved in brainstorm.
