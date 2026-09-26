# Implementation Plan: Issues 261 & 263 Web Server Fixes

## Phase 1: Protocol Wire Updates
- Update `internal/protocol/wirepb/wideboi.proto`:
  - Add `bool enable_tls = 6;` to `MsgWebServerControlRequest`
  - Add `string warning = 7;` to `MsgWebServerControlResponse`
- Run `make proto` to regenerate Go and TypeScript protobuf code.
- Update `internal/protocol/messages.go`:
  - Add `EnableTLS bool` to `MsgWebServerControlRequest`
  - Add `Warning string` to `MsgWebServerControlResponse`
- Update `internal/protocol/codec.go`:
  - Wire `EnableTLS` in `MarshalClient` / `UnmarshalClient`
  - Wire `Warning` in `MarshalServer` / `UnmarshalServer`
- Verify with `go test ./internal/protocol/...` and `make proto-check`.

## Phase 2: Issue 261 Implementation & Cleanup
- Update `internal/server/web.go`:
  - Add `ExposureWarning(addr net.Addr, tlsEnabled bool) string` helper.
  - Call `ExposureWarning` in `Start` and assign to `response.Warning`.
  - Log warning to `slog.Warn`.
- Update `cmd/wideboi/main.go`:
  - Replace `webTokenPath(cfg.Socket)` with `server.WebTokenPath(cfg.Socket)`.
  - Delete `webTokenPath`, `writeWebToken`, `announceWebClient`, `warnIfWebClientExposed`.
  - In `runServer`, if `resp.Warning != ""`, print to `os.Stderr`.
- Update `cmd/wideboi/web.go`:
  - Pass `stderr` to `runWebStart`.
  - If `resp.Warning != ""`, print to `stderr`.
- Move test cases from `cmd/wideboi/web_token_test.go` to `internal/server/web_test.go`.
- Delete `cmd/wideboi/web_token_test.go`.
- Verify with `go test ./cmd/wideboi/... ./internal/server/...`.

## Phase 3: Issue 263 Implementation (TLS Toggle & Fixed Port Rebind)
- Update `internal/server/web.go`:
  - Support `req.EnableTLS`. If both `EnableTLS` and `DisableTLS` are set, return error.
  - Determine `targetTLS`:
    - If `req.EnableTLS`: true
    - If `req.DisableTLS`: false
    - Else: `w.tlsEnabled` (or `w.configuredTLS`)
  - Target address resolution:
    - If `req.Addr != ""`: `targetAddr = req.Addr`
    - Else if `w.running && w.boundAddr != ""`: `targetAddr = w.boundAddr`
    - Else if `w.addr != ""`: `targetAddr = w.addr`
    - Else: `targetAddr = "127.0.0.1:0"`
  - Listener rebind ordering:
    - If `w.running` and `targetAddr` matches `w.boundAddr` or resolves to the same port:
      - Shut down old `httpSrv` and close `listener` first.
      - If `req.RotateToken`, disconnect WebSocket clients.
      - Bind `net.Listen("tcp", targetAddr)`.
    - Else:
      - Bind `net.Listen("tcp", targetAddr)` first.
      - If success and `w.running`: shut down old `httpSrv` and close old `listener`.
  - Update `cmd/wideboi/web.go`:
    - Add `--enable-tls` flag to `wideboi web start`.
    - Check for conflicting `--enable-tls` and `--disable-tls` flags.
- Add tests in `internal/server/web_test.go`:
  - Test rotating token on a fixed port while server is running.
  - Test disabling TLS and then re-enabling TLS.
  - Test conflicting `--enable-tls` and `--disable-tls`.
- Add tests in `cmd/wideboi/web_test.go` for CLI flag handling.

## Phase 4: Verification and Quality Checks
- Run `make quick` (fmt-check, lint, seam-check, test, web-test).
- Run `make check` (all targets including smoke and attach-check).
- Verify git status, diff, and write session notes.
