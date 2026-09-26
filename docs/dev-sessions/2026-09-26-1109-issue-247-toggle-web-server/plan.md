# Enable and Disable Session Web Server After Launch Implementation Plan

**Goal:** Enable a running wideboi session to dynamically start, stop, and inspect its HTTP/WebSocket server via CLI commands and Unix socket RPC without restarting the session.

**Approach:**
Manage the web server lifecycle directly inside `internal/server.Server` via a new web server manager. Expose control via new protocol messages (`MsgWebServerControlRequest`, `MsgWebServerControlResponse`) over the existing Unix socket RPC mechanism. Provide a new CLI surface `wideboi web [start|stop|status]` and integrate web server state into `wideboi status --json`. Bump wire protocol to version 14 (`wideboi.v14`).

**Tech stack:** Go, Protobuf (buf + protoc-gen-go), TypeScript/Lit (web client), Unix domain sockets, HTTP/WebSocket (gorilla/websocket).

---

## Phase 1: Protocol Messages & Wire Version Bump (Slice 1)

Bump wire protocol to version 14, add `MsgWebServerControlRequest` and `MsgWebServerControlResponse` to protobuf schema, regenerate Go and TypeScript bindings, update codec and wire tests.

**Files:**
- Modify: `internal/protocol/wirepb/wideboi.proto` — add messages and oneof fields
- Modify: `internal/protocol/version.go` — bump `Version` to 14
- Modify: `internal/protocol/messages.go` — add Go structs and enums
- Modify: `internal/protocol/codec.go` — encode/decode new messages
- Modify: `internal/protocol/wire_test.go` — test round-trip for new messages
- Modify: `web/src/client.ts` — update subprotocol to `wideboi.v14`
- Modify: `web/src/client.test.ts` — update test fixtures to `wideboi.v14`
- Modify: `web/src/lifecycle.test.ts` — update test fixtures to `wideboi.v14`
- Modify: `web/tests/*.spec.js` — update test fixtures to `wideboi.v14`

**Key changes:**
```protobuf
enum WebServerAction {
  WEB_SERVER_ACTION_UNSPECIFIED = 0;
  WEB_SERVER_ACTION_STATUS = 1;
  WEB_SERVER_ACTION_START = 2;
  WEB_SERVER_ACTION_STOP = 3;
}

message MsgWebServerControlRequest {
  WebServerAction action = 1;
  string addr = 2;
  string token = 3;
  bool rotate_token = 4;
  bool disable_tls = 5;
}

message MsgWebServerControlResponse {
  bool running = 1;
  string addr = 2;
  string url = 3;
  bool tls_enabled = 4;
  string token = 5;
  string error = 6;
}
```

Go definitions in `internal/protocol/messages.go`:
```go
type WebServerAction int

const (
    WebServerActionUnspecified WebServerAction = 0
    WebServerActionStatus      WebServerAction = 1
    WebServerActionStart       WebServerAction = 2
    WebServerActionStop        WebServerAction = 3
)

type MsgWebServerControlRequest struct {
    Action      WebServerAction
    Addr        string
    Token       string
    RotateToken bool
    DisableTLS  bool
}

type MsgWebServerControlResponse struct {
    Running    bool
    Addr       string
    URL        string
    TLSEnabled bool
    Token      string
    Error      string
}
```

**Verification — automated:**
- [x] `make proto` regenerates Go and TypeScript protobuf bindings
- [x] `make proto-check` passes
- [x] `go test ./internal/protocol -v` passes
- [x] `make web-test` passes with `wideboi.v14`

**Verification — manual:**
- [x] `git diff internal/protocol/version.go` shows `Version = 14` (bumped after main reached 13)

---

## Phase 2: Server-Side Web Server Lifecycle & Management (Slice 2)

Implement web server management in `internal/server` so the HTTP/WebSocket server can be started, stopped, and queried dynamically. Handle WebSocket client disconnections on stop, token file management, port conflicts, and RPC handling.

**Files:**
- Create: `internal/server/web.go` — web server manager, start/stop/status methods, client disconnection logic
- Modify: `internal/server/server.go` — integrate web manager, handle `MsgWebServerControlRequest`, teardown on `Server.Close()`
- Modify: `cmd/wideboi/main.go` — adapt `runServer` to delegate initial web server startup to `srv.StartWebServer`
- Create: `internal/server/web_test.go` — unit and integration tests for web server toggle

**Key changes:**
```go
type WebServerOptions struct {
    SocketPath     string
    ListenAddr     string
    Token          string
    TLSEnabled     bool
    TLSCert        string
    TLSKey         string
}

func (s *Server) StartWebServer(opts WebServerStartOptions) (protocol.MsgWebServerControlResponse, error)
func (s *Server) StopWebServer() (protocol.MsgWebServerControlResponse, error)
func (s *Server) WebServerStatus() protocol.MsgWebServerControlResponse
```

In `handleClientMsg` (`internal/server/server.go`):
```go
case protocol.MsgWebServerControlRequest:
    switch m.Action {
    case protocol.WebServerActionStatus:
        webResp = s.WebServerStatus()
    case protocol.WebServerActionStart:
        webResp, _ = s.StartWebServer(opts)
    case protocol.WebServerActionStop:
        webResp, _ = s.StopWebServer()
    }
```

When stopping:
- Close HTTP listener and shutdown `http.Server`.
- Find all transports where `transportKind(tp) == "websocket"`.
- Close transports, removing them from attached lists and broadcasting layout update to remaining clients.
- Remove `.web-token` file.
- Keep terminal and Unix socket clients intact.

When starting:
- If `--addr` / `m.Addr` not specified, use configured address or default to `127.0.0.1:0`.
- If port conflict occurs during `net.Listen`, return `MsgWebServerControlResponse{Error: err.Error()}` without panicking or shutting down the session.
- Retain existing token unless `RotateToken` is true.
- Write `.web-token` file with permissions 0600.

**Verification — automated:**
- [x] `go test ./internal/server -run TestWebServerToggle -v` passes
- [x] `go test ./internal/server -run TestWebServerPortConflict -v` passes
- [x] `go test ./internal/server -run TestWebServerDisconnectsClientsOnStop -v` passes
- [x] `make quick` passes

**Verification — manual:**
- [x] Verify that stopping web server does not close in-proc or socket transports

---

## Phase 3: CLI Subcommand `wideboi web` & Status JSON Integration (Slice 3)

Add the `wideboi web [start|stop|status]` CLI subcommand and integrate web server status into `wideboi status --json`.

**Files:**
- Create: `cmd/wideboi/web.go` — implementation of `wideboi web` subcommands (`runWeb`, `runWebStart`, `runWebStop`, `runWebStatus`)
- Modify: `cmd/wideboi/main.go` — route `wideboi web` subcommand, update usage text
- Modify: `cmd/wideboi/status.go` — add `WebServer` field to `statusOutput` in `wideboi status --json`
- Modify: `cmd/wideboi/main_test.go` — test CLI command dispatch and exit codes
- Create: `cmd/wideboi/web_test.go` — end-to-end tests for `wideboi web` commands

**Key changes:**
`cmd/wideboi/web.go`:
```go
func runWeb(cfg config.Config, args []string, w io.Writer) error
func runWebStatus(cfg config.Config, jsonOut bool, w io.Writer) error
func runWebStart(cfg config.Config, addr string, rotateToken bool, disableTLS bool, w io.Writer) error
func runWebStop(cfg config.Config, w io.Writer) error
```

CLI behavior:
- `wideboi web status`:
  - Human-readable output showing Running status, Listen address, Browser URL (with token), and TLS state.
  - `--json` outputs machine-readable JSON object.
- `wideboi web start [--addr <addr>] [--rotate-token] [--disable-tls]`:
  - Sends start request to server.
  - Prints: `wideboi: web client listening at <url>`
  - Exits with code 1 if port conflict or bind failure.
- `wideboi web stop`:
  - Sends stop request to server.
  - Prints: `wideboi: web server stopped`
- `wideboi status --json`:
  - `statusOutput.WebServer` populated from `MsgWebServerControlRequest{Action: STATUS}`.

**Verification — automated:**
- [x] `go test ./cmd/wideboi -run TestWebCommand -v` passes
- [x] `go test ./cmd/wideboi -run TestStatusJSONIncludesWebServer -v` passes
- [x] `make quick` passes

**Verification — manual:**
- [x] `wideboi web --help` outputs clear usage instructions

---

## Phase 4: System Gate Verification & Documentation (Slice 4)

Run full verification suite across the repository and document the new commands.

**Files:**
- Modify: `docs/MANUAL.md` — document `wideboi web` CLI commands and options
- Modify: `README.md` — update CLI commands summary

**Verification — automated:**
- [x] `make fmt-check` passes
- [x] `make lint` passes
- [x] `make seam-check` passes
- [x] `make test` passes
- [x] `make web-test` passes
- [x] `make web-accept` passes
- [x] `make race` passes
- [x] `make verify-exit` passes
- [x] `make smoke` passes
- [x] `make attach-check` passes
- [x] `make check` passes (all gate targets green)

**Verification — manual:**
- [x] Verify `docs/MANUAL.md` contains accurate documentation for `wideboi web start`, `wideboi web stop`, and `wideboi web status`.
