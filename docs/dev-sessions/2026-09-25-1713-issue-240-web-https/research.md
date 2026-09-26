# Research: Issue 240 - Implement HTTPS for web client

## 1. Embedded Web Server / HTTP / WebSocket Initialization & Startup Trace

### CLI Flags & Configuration
- **CLI Flag Registration**: `cmd/wideboi/main.go:119-120` registers `--websocket` (bound to `opts.flags.Websocket`) and `--websocket-token` (bound to `opts.flags.WebsocketToken`).
- **Configuration Loading**: `cmd/wideboi/main.go:343-345` calls `config.Load(opts.flags, os.Getenv)`.
- **Precedence in `internal/config/config.go`**:
  - Baseline defaults: `Websocket = ""` and `WebsocketToken = ""` (`internal/config/config.go:210-211`).
  - TOML file values: fields `websocket` and `websocket_token` populate `cfg.Websocket` and `cfg.WebsocketToken` (`internal/config/config.go:25-26, 287-292`).
  - Environment overrides: `WIDEBOI_WEBSOCKET` and `WIDEBOI_WEBSOCKET_TOKEN` override TOML values (`internal/config/config.go:340-345`).
  - CLI flag overrides: `flags.Websocket` and `flags.WebsocketToken` take top precedence (`internal/config/config.go:375-380`).

### Server Startup & Listeners (`cmd/wideboi/main.go`)
- **Activation Gate**: In `runServer`, if `cfg.Websocket != ""` (`cmd/wideboi/main.go:395`):
  - **Token Generation**: If `cfg.WebsocketToken == ""`, generates 16 crypto-random bytes formatted as hex (`cmd/wideboi/main.go:397-404`).
  - **Mux & WebSocket Route**: Instantiates `mux := http.NewServeMux()` (`cmd/wideboi/main.go:406`) and invokes `srv.ListenWebSocket(ctx, mux, cfg.WebsocketToken)` (`cmd/wideboi/main.go:407`).
  - **Static Asset Route**: Retrieves embedded filesystem via `web.DistFS()` (`cmd/wideboi/main.go:409`, defined in `web/embed.go:13-18` embedding `web/dist`) and mounts `mux.Handle("/", http.FileServer(distFS))` (`cmd/wideboi/main.go:413`).
  - **HTTP Server**: Wraps mux in `httpSrv = &http.Server{ Handler: mux }` (`cmd/wideboi/main.go:415-417`).
  - **TCP Listener**: Calls `net.Listen("tcp", cfg.Websocket)` (`cmd/wideboi/main.go:419`).
  - **Exposure Warning**: Checks non-loopback addresses via `warnIfWebClientExposed` (`cmd/wideboi/main.go:424, 514-521`).
  - **Token Persistence**: If token was generated, writes to `<socket>.token` with `0600` permissions via `writeWebToken` (`cmd/wideboi/main.go:426, 475-499`), and registers deferred file removal (`cmd/wideboi/main.go:430`).
  - **Background Serving**: Logs connection URL via `announceWebClient` (`cmd/wideboi/main.go:441, 503-512`) and serves in goroutine via `httpSrv.Serve(wsListener)` (`cmd/wideboi/main.go:442`).
  - **Shutdown**: Calls `httpSrv.Shutdown(context.Background())` after `srv.Run(ctx)` terminates (`cmd/wideboi/main.go:448-451`).

### HTTP Upgrade & Server Dispatch (`internal/server/server.go`)
- **`Server.ListenWebSocket`** (`internal/server/server.go:2129-2183`):
  - Configures `websocket.Upgrader` with `Subprotocols: []string{"wideboi.v<Version>"}`, `CheckOrigin: webSocketOriginAllowed`, and `EnableCompression: true` (`internal/server/server.go:2130-2137`).
  - Registers `/ws` handler (`internal/server/server.go:2139`).
  - Validates token against query parameter `?token=` or subprotocol `wideboi-token.<base64url>`.
  - Upgrades connection to `*websocket.Conn`, wraps in `transport.WebSocketServerConn`, runs transport pumps, and attaches to server client loop.

---

## 2. Frontend Connection URL, Protocol, and Port Determination

- **Default URL Construction**:
  - Initialized in `web/src/wideboi-app.ts:745`:
    ```ts
    private wsUrl = `${window.location.protocol === 'https:' ? 'wss:' : 'ws:'}//${window.location.host}/ws`;
    ```
  - Used as the placeholder in connection UI (`web/src/wideboi-app.ts:2299`).
- **Protocol Determination**:
  - Evaluates `window.location.protocol`: if `'https:'`, uses `'wss:'`; otherwise `'ws:'`.
- **Host & Port**:
  - Evaluates `window.location.host`.
- **Validation**:
  - In `connectClient()` (`web/src/wideboi-app.ts:1146-1155`), parses `this.wsUrl` with `new URL(...)`. If `url.protocol !== 'ws:' && url.protocol !== 'wss:'`, rejects.
- **Client Connection**:
  - In `web/src/client.ts:31-38`, passes subprotocols `wideboi-token.<b64>` and `wideboi.v12` to `new WebSocket(this.url, protocols)`.

---

## 3. Configuration Loading & Validation

- `Config` struct in `internal/config/config.go:22-94`.
- Precedence: Defaults -> TOML -> Environment (`WIDEBOI_*`) -> CLI Flags.
- Validation in `internal/config/config.go:395-470`.

---

## 4. Test Coverage for Web Server & WebSocket

- **Go Tests**:
  - `internal/transport/websocket_test.go`: Transport frames, compression, buffer limits.
  - `internal/server/websocket_auth_test.go`: Token auth, subprotocols, origin checking.
  - `cmd/wideboi/startup_error_test.go` and `web_token_test.go`: Listener collisions, token logging redaction, exposure warning.
- **Web Unit Tests**: `web/src/client.test.ts`, `web/src/token.test.ts`.
- **Acceptance / E2E Tests**:
  - `web/tests/live-terminal.spec.js`: Starts real `wideboi server --websocket ...`, connects Playwright to `http://127.0.0.1:<port>/#token=<token>`, asserts live terminal interactivity.
