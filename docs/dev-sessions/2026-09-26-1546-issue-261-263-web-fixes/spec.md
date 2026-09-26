# Dev Session Spec: Issues 261 & 263 Web Server Fixes

## Problem Statement

### Issue 261: Unencrypted-exposure warning no longer reaches the user; dead web helpers in main.go
`cmd/wideboi/main.go` defines `writeWebToken`, `announceWebClient`, `warnIfWebClientExposed`, and `webTokenPath`, but only `cmd/wideboi/web_token_test.go` called them. The live versions are in `internal/server/web.go`.
The dead copy printed `WARNING: web client is exposed beyond loopback over unencrypted HTTP/WS` to stderr. The live copy in `internal/server/web.go` only called `slog.Warn`, so launching with `--websocket 0.0.0.0:... --disable-tls` or `wideboi web start --addr 0.0.0.0:... --disable-tls` never warned the user on stderr. The test stayed green because it only exercised the dead copy.

### Issue 263: Disabling TLS is permanent; token rotation fails on a fixed port
In `internal/server/web.go` `Start`:
1. `DisableTLS` is sticky: once `DisableTLS` is requested, `w.tlsEnabled` is set to false and there is no `EnableTLS` request field or mechanism to re-enable TLS.
2. Rebind before shutdown: `net.Listen` is called before shutting down the old listener. On a configured fixed port, rotating the token or reconfiguring TLS fails with `EADDRINUSE`.

## Proposed Solution

1. **Clean up dead code in `cmd/wideboi`:**
   - Remove `writeWebToken`, `announceWebClient`, `warnIfWebClientExposed`, and `webTokenPath` from `cmd/wideboi/main.go`.
   - Update line 539 in `main.go` to use `server.WebTokenPath(cfg.Socket)`.
   - Remove `cmd/wideboi/web_token_test.go`.

2. **Propagate exposure warning to user:**
   - Add `ExposureWarning(addr net.Addr, tlsEnabled bool) string` helper in `internal/server/web.go`.
   - Add `Warning string` field to `protocol.MsgWebServerControlResponse` and wire protobuf definition.
   - Set `resp.Warning` in `StartWebServer` when exposed beyond loopback over plain HTTP.
   - In `cmd/wideboi/main.go:runServer`: print `resp.Warning` to `os.Stderr`.
   - In `cmd/wideboi/web.go:runWebStart`: pass `stderr` to `runWebStart` and print `resp.Warning` to `stderr`.

3. **Support TLS re-enabling:**
   - Add `EnableTLS bool` to `protocol.MsgWebServerControlRequest` and wire protobuf definition.
   - Add `--enable-tls` flag to `wideboi web start`. Return error if both `--enable-tls` and `--disable-tls` are specified.
   - In `webServerManager.Start`:
     - If `req.EnableTLS && req.DisableTLS`: return error.
     - Determine target TLS:
       - `req.EnableTLS` -> `true`
       - `req.DisableTLS` -> `false`
       - neither -> keep `w.tlsEnabled` (or configured default).
     - When TLS is re-enabled, properly load or generate TLS config and wrap listener with TLS.

4. **Fix rebind on fixed port:**
   - If server is running and the target address matches the currently bound address (or if `req.Addr` is empty), shut down the old HTTP server and listener before binding `net.Listen` on the port.
   - If target address is different, attempt binding first, then shut down the old listener on success.
   - Disconnect WebSocket clients on token rotation.

5. **Unit and Integration Tests:**
   - Move tests for `WebTokenPath`, `WriteWebToken`, and `ExposureWarning` to `internal/server/web_test.go`.
   - Add test verifying warning is returned in `MsgWebServerControlResponse` when exposed unencrypted.
   - Add test verifying token rotation on a fixed port succeeds while the server is running.
   - Add test verifying disabling TLS then re-enabling TLS succeeds.
   - Add test verifying `wideboi web start --enable-tls` and `--disable-tls` CLI flags.
