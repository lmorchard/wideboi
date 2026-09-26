# Research: Issue 247: Enable and disable a session web server after launch

## 1. HTTP / WebSocket Server: Start, Configuration, and Management

### Configuration & Flags
- **CLI Flags** (`cmd/wideboi/main.go:120-125`):
  - `--websocket <addr>`: Listen address (e.g., `"127.0.0.1:8080"`).
  - `--websocket-token <token>`: Required auth token.
  - `--disable-tls` / `--tls`: Boolean flags to toggle TLS/HTTPS (TLS is enabled by default).
  - `--tls-cert <path>` / `--tls-key <path>`: Paths to PEM certificate and private key.
- **Config Struct & Defaults** (`internal/config/config.go:25-26, 48-53, 220-221`):
  - `Websocket` defaults to `""` (web server disabled).
  - `WebsocketToken` defaults to `""`.
  - `TLSEnabled` defaults to `true` (`config.go:421-423`).
- **Precedence & Env Overrides** (`internal/config/config.go:297-310, 359-417, 420-438`):
  - CLI flags override environment variables (`WIDEBOI_WEBSOCKET`, `WIDEBOI_WEBSOCKET_TOKEN`, `WIDEBOI_TLS`, `WIDEBOI_DISABLE_TLS`, `WIDEBOI_TLS_CERT`, `WIDEBOI_TLS_KEY`), which override TOML configuration file settings (`websocket`, `websocket_token`, `tls`, `tls_cert`, `tls_key`).

### Server Startup & Listener Creation
- **Lifecycle in `runServer`** (`cmd/wideboi/main.go:408-468`):
  - If `cfg.Websocket != ""`, web server initialization runs before entering `srv.Run(ctx)`.
  - **Token Generation**: If `cfg.WebsocketToken == ""`, generates 16 random bytes via `crypto/rand.Read` formatted as 32 hex chars (`cmd/wideboi/main.go:410-417`).
  - **HTTP Mux**: `mux := http.NewServeMux()` (`cmd/wideboi/main.go:419`).
  - **WebSocket Handler**: Attaches `/ws` endpoint via `srv.ListenWebSocket(ctx, mux, cfg.WebsocketToken)` (`cmd/wideboi/main.go:420`, `internal/server/server.go:2129-2183`).
  - **Static Asset Handler**: Serves embedded UI assets at `/` via `web.DistFS()` with `http.FileServer` (`cmd/wideboi/main.go:422-426`, `web/embed.go:10-21`).
  - **TCP Listener**: Created with `net.Listen("tcp", cfg.Websocket)` (`cmd/wideboi/main.go:432`).
  - **TLS Listener**: If `cfg.TLSEnabled`, loads cert/key or generates an ephemeral cert via `transport.LoadOrGenerateTLSConfig(cfg.TLSCert, cfg.TLSKey, cfg.Websocket)`, then wraps listener with `tls.NewListener(wsListener, tlsConfig)` (`cmd/wideboi/main.go:437-444`, `internal/transport/tls.go:114-140`).
  - **Serving Goroutine**: Background goroutine calls `httpSrv.Serve(wsListener)` (`cmd/wideboi/main.go:458-467`).
  - **Shutdown**: When `srv.Run(ctx)` exits, calls `httpSrv.Shutdown(context.Background())` (`cmd/wideboi/main.go:471-473`).

---

## 2. WebSocket Connection Lifecycle & Client Tracking

### Acceptance, Authentication, and Upgrade
- **Handler** (`internal/server/server.go:2139-2182`):
  - **Auth Verification** (`internal/server/server.go:2140-2147`): Validates against `token` using query param `r.URL.Query().Get("token")` or WebSocket subprotocol header via `websocketProtocolToken(r)` (`internal/server/server.go:219-230`).
  - **Subprotocol Version Check** (`internal/server/server.go:2148-2158`): Verifies the client offered subprotocol `wideboi.v<protocol.Version>` (currently `wideboi.v12`). If missing, returns `426 Upgrade Required`.
  - **Origin Validation** (`internal/server/server.go:2188-2206`): `webSocketOriginAllowed` permits empty origins (non-browser clients), matching `u.Host == r.Host`, or Vite dev server origins (`localhost:5173`, `127.0.0.1:5173`, `[::1]:5173`) only when the server itself is addressed via loopback.
  - **Upgrade**: `upgrader.Upgrade(cw, r, nil)` upgrades conn wrapping `cw` (`transport.CountingResponseWriter`) for wire accounting (`internal/server/server.go:2162-2167`).
  - **Connection Wrapper**: Wraps in `sConn := transport.NewWebSocketServerConn(conn, 256, cw)` (`internal/server/server.go:2169`, `internal/transport/websocket.go:38-49`).
  - **Attachment Loop**: Enqueues `sConn` into `s.transports` (`internal/server/server.go:2178`) and spawns `go s.handleClientConnLoop(ctx, sConn)` (`internal/server/server.go:2181`).

### WebSocket Clients vs. Unix Socket Clients
- Common interface: Both `*transport.WebSocketServerConn` (`internal/transport/websocket.go:22`) and `*transport.ServerSocketConn` (`internal/transport/socket.go:183`) implement `transport.Transport` (`internal/transport/inproc.go:15-20`).
- Both types are held uniformly in `s.transports` (`internal/server/server.go:39`) and `s.attachedTransports` (`internal/server/server.go:64`).
- Differentiated at runtime via type assertion in `transportKind` (`internal/server/traffic.go:126-135`), which labels them `"websocket"`, `"socket"`, or `"inproc"`.
- WebSocket clients are never assigned as `s.owner` (`internal/server/server.go:127, 236-240`).

### Disconnection & Server Close
- **Client Disconnection** (`internal/server/server.go:346-404`):
  - When `tp.ClientSendChan()` closes, `s.handleClientConnLoop` calls `s.dropClient(ctx, tp)`.
  - Under `s.mu`, `s.removeTransportLocked(tp)` removes `tp` from `s.transports`, `s.attachedTransports`, `s.clientSizes`, and all per-client pane generation/scroll tracking maps (`internal/server/server.go:406-432`), and folds its counters into `s.departed` (`internal/server/traffic.go:78-83`).
  - If `attached` was true, calls `s.broadcastLayout(ctx)` to notify remaining clients (`internal/server/server.go:388-390`).
  - If `tp` implements `io.Closer`, calls `cl.Close()` outside `s.mu` (`internal/server/server.go:400-402`, `internal/transport/websocket.go:193-199`).

---

## 3. Protocol Messages, CLI Subcommands, and Control Flows

### CLI Subcommands (`cmd/wideboi/main.go:245-281`)
- `wideboi [command...]` (default): Spawns daemon if needed and attaches TUI client (`cmd/wideboi/main.go:292-349`).
- `wideboi server`: Starts headless session server (`cmd/wideboi/main.go:351-499`).
- `wideboi attach`: Attaches terminal client to running server (`cmd/wideboi/main.go:584-633`).
- `wideboi kill-session`: Sends `MsgShutdown` to session server (`cmd/wideboi/main.go:554-582`).
- `wideboi status [--json] [--traffic]`: Prints layout snapshot or traffic stats (`cmd/wideboi/status.go:25-151`).
- `wideboi cleanup`: Sweeps dead sockets, logs, and token files (`cmd/wideboi/cleanup.go:44-78`).
- `wideboi ls`: Lists live session names answering socket dials (`cmd/wideboi/sessions.go:39-49`).
- `wideboi split`, `send`, `capture`, `close`, `wait`: Control verbs/RPCs (`cmd/wideboi/control.go`).

### Protocol Control Requests & Responses
- Synchronous RPC pattern: `rpcQuery[Resp]` / `rpcOn[Resp]` in `cmd/wideboi/control.go:25-71` connects to Unix socket, sends `Msg<Name>Request`, and awaits `Msg<Name>Response`.
- Wire types defined in `internal/protocol/wirepb/wideboi.proto` and encoded/decoded in `internal/protocol/codec.go`.

---

## 4. Desktop Session Manager / Discovery / Loopback Bridge Status
- There is currently no GUI desktop app or desktop loopback bridge in this repository.
- Sessions are discovered via Unix domain sockets in `cmd/wideboi/sessions.go:18-37` (`listSessions`): searches `$XDG_RUNTIME_DIR/wideboi/*.sock` (or `/tmp/wideboi-$UID/*.sock`).
- The reference in issue #247 to "Offer the same control from the CLI and the desktop session manager" means any session control must be queryable and controllable via standard CLI / RPC messages that an external tool (like a desktop app manager) can drive over the session's Unix socket or CLI commands.

---

## 5. Address, Port, and Token Reporting
- **Announcement**: Printed to stderr on startup (`cmd/wideboi/main.go:525-538`).
- **Token File**: Auto-generated token written to `<socket-path-without-.sock>.web-token` (mode 0600) (`cmd/wideboi/main.go:501-521`).
- **Status / RPC**: Currently neither `wideboi status` nor any RPC reports whether the web server is running, what address/port it is bound to, or the URL/token.
