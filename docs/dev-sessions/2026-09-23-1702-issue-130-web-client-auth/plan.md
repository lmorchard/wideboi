# Implementation Plan: Web Client Authentication

## Goal
Add optional secret token authentication to the wideboi web client connection. When configured, the WebSocket connection must pass the token to be accepted.

## Current State
- The WebSocket server is wide open. It accepts any connection that passes the `CheckOrigin` check.
- `wideboi` configuration currently has a `Websocket` option for the listen address, but no auth configuration.
- The web client connects via a plain WebSocket URL (`ws://...`).
- There's no authentication handshake or token passing mechanism in the WebSocket protocol.

## Desired End State
- Introduce an optional `WebsocketToken` in the configuration (`websocket_token` in TOML, or generated randomly at startup if unspecified but `Websocket` is enabled).
- The web client should be able to authenticate by passing this token, likely via a query parameter (e.g. `ws://localhost:8080/ws?token=XYZ`), since WebSockets don't easily support custom headers from the browser API.
- If a token is configured, the server rejects connections without it or with an incorrect token (HTTP 401 Unauthorized or 403 Forbidden).
- If no token is configured, the server remains open (for backwards compatibility or development ease, or we can make a token mandatory when WS is enabled). Actually, the spec suggests "Maybe something dumb like secret token auth, randomized at server startup and/or specified in config file?". We will generate a random token and print it to stdout if it's not specified.
- The web client should prompt the user for a token if one is required, or maybe we just pass it in the URL when launching.

## Design Decisions
1. **Token Passing:** Since the browser `WebSocket` API does not allow setting custom HTTP headers, we will pass the token in the query string (e.g. `?token=...`) or via the `Sec-WebSocket-Protocol` header. The query string is the standard way to do this for browser WebSockets. We'll use the query string: `/ws?token=...`.
2. **Configuration:** Add `WebsocketToken string` to `Config` struct (`websocket_token` in TOML). If `cfg.Websocket != ""` and `cfg.WebsocketToken == ""`, generate a random token using `crypto/rand` and print it on startup.
3. **Server Verification:** In `s.ListenWebSocket(..., cfg.WebsocketToken)`, if `cfg.WebsocketToken` is set, check `r.URL.Query().Get("token") == cfg.WebsocketToken`. If it fails, return HTTP 401 Unauthorized and do not upgrade the connection.
4. **Client UI:** In `web/src/client.ts` and `web/src/wideboi-app.ts`, allow the user to provide a token, perhaps parsed from the page URL (e.g., `http://localhost:5173/?token=...`) so they can easily bookmark it.
5. **No Token = Open?** The spec says "randomized at server startup and/or specified in config file". This implies that if you enable the web client, it *will* have auth. Either you specify a token, or it generates one. Let's make it so if `Websocket` is set, auth is always on. If the user *really* wants it open, maybe `websocket_token = "none"` or we just accept that it's always authed now. Actually, let's just make it always authed if `cfg.Websocket != ""`.

Wait, let's refine step 5. If `cfg.WebsocketToken` is explicitly set to `"none"` or similar, maybe it disables it? Or perhaps if it's empty, we generate one.
Let's generate a 16-character hex string if `cfg.WebsocketToken` is empty and `cfg.Websocket` is enabled.
And print `wideboi: websocket token is: <token>` to stderr along with the listening message.
If they want no auth, maybe they can set `websocket_token = ""`? No, then it generates one. Maybe `websocket_token = "none"` disables it? Let's just generate it if empty. Wait, what if someone relies on it being open? The issue says "The web client is wide open right now. I'm assuming I'll use it over Tailscale or similar, but it could be good to implement some kind of authentication". So a token by default is fine.

## Steps
1. **Update `Config`**:
   - `internal/config/config.go`: Add `WebsocketToken string` to `Config`. Map to `websocket_token` in TOML.
   - Add `WebsocketToken` to `ConfigFlags`.
   - Update `internal/config/config_test.go`.
2. **Generate Token**:
   - In `cmd/wideboi/main.go`, before setting up `httpSrv`, check if `cfg.Websocket != ""`. If `cfg.WebsocketToken == ""`, generate a random hex token (e.g., 32 hex chars).
   - Log the token: `fmt.Fprintf(os.Stderr, "wideboi: websocket server listening at ws://%s/ws?token=%s\n", cfg.Websocket, cfg.WebsocketToken)`
3. **Server-side check**:
   - Update `func (s *Server) ListenWebSocket(ctx context.Context, mux *http.ServeMux, expectedToken string)` in `internal/server/server.go`.
   - In the `/ws` handler, if `expectedToken != ""`, check `r.URL.Query().Get("token") == expectedToken`. If not, write a 401 response and return.
4. **Web Client Update**:
   - `web/src/wideboi-app.ts` / `web/index.html`: Parse `token` from `window.location.search`.
   - If not found, perhaps prompt the user with a simple `prompt("Enter WebSocket token:")` and append it to the URL, then reload, or just use it.
   - Pass it to `WideboiClient`. `client.ts` uses `this.url = ... + "?token=" + token`.
