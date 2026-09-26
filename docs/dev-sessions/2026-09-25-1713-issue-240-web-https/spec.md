# HTTPS for Embedded Web Client Spec

**Goal:** Enable secure HTTPS and WSS connections to wideboi's embedded web client for LAN and remote security.

**Source:** Issue #240: Implement HTTPS for web client

## Current state

- **CLI flags and config:**
  - `cmd/wideboi/main.go:119-120` registers `--websocket` and `--websocket-token`.
  - `internal/config/config.go:25-26, 210-211` defines `Websocket` and `WebsocketToken`.
  - TOML file and `WIDEBOI_*` environment variables provide configuration overrides (`internal/config/config.go:287-292, 340-345`).
- **Server listener:**
  - `cmd/wideboi/main.go:415-445` starts `http.Server` on a plain TCP listener `net.Listen("tcp", cfg.Websocket)`.
  - `announceWebClient` logs `http://<host>/#token=<token>` to stderr (`cmd/wideboi/main.go:503-512`).
  - `warnIfWebClientExposed` emits a warning when bound beyond loopback over unencrypted HTTP/WS (`cmd/wideboi/main.go:514-521`).
- **Web client:**
  - `web/src/wideboi-app.ts:745, 2299` checks `window.location.protocol === 'https:' ? 'wss:' : 'ws:'` for the default WebSocket URL.
  - `web/src/wideboi-app.ts:1146-1155` validates WebSocket URLs and accepts both `ws:` and `wss:`.
  - `web/src/client.ts:31-38` connects using `new WebSocket(this.url, protocols)`.

## Desired end state

1. **Configuration options:**
   - **Enabled by default:** TLS is enabled by default (`TLSEnabled = true`) whenever the web server is started (`--websocket`).
   - New CLI flags:
     - `-disable-tls` / `--disable-tls` (bool): explicitly disables TLS and falls back to plain HTTP / WS.
     - `-tls` / `--tls` (bool): explicitly enables TLS (also on by default unless disabled).
     - `-tls-cert` / `--tls-cert` (path): path to PEM-encoded TLS certificate.
     - `-tls-key` / `--tls-key` (path): path to PEM-encoded TLS private key.
   - New TOML fields in `config.Config`:
     - `tls` (bool, optional pointer): `tls = true` or `tls = false` (default `true` when omitted).
     - `tls_cert` (string): path to certificate file.
     - `tls_key` (string): path to private key file.
   - New environment variables:
     - `WIDEBOI_TLS` ("true" / "false" / "1" / "0" / "yes" / "no" / "on" / "off").
     - `WIDEBOI_DISABLE_TLS` ("true" / "1" / "yes" / "on").
     - `WIDEBOI_TLS_CERT` (path).
     - `WIDEBOI_TLS_KEY` (path).
   - Activation logic & precedence:
     - Default is `TLSEnabled = true`.
     - TOML `tls = false` disables TLS.
     - Environment `WIDEBOI_TLS` or `WIDEBOI_DISABLE_TLS` overrides TOML.
     - CLI flags `--disable-tls` or `--tls` take top precedence.
     - Specifying both `--tls-cert` and `--tls-key` automatically enables TLS (unless explicitly disabled with `--disable-tls`).
     - Specifying only one of cert or key is a configuration validation error.
2. **TLS Certificate & Key Provisioning:**
   - If `tls_cert` and `tls_key` are provided, the server loads the keypair from the specified file paths.
   - If TLS is enabled without explicit cert and key paths (the default), the server automatically generates an in-memory ephemeral self-signed ECDSA (P-256) X.509 certificate on startup.
   - The self-signed certificate SANs include `localhost`, `127.0.0.1`, `::1`, and any hostname or IP parsed from `--websocket` (including non-loopback local IP addresses if listening on `0.0.0.0` or all interfaces).
3. **Server Listener & Announcement:**
   - When TLS is active (`cfg.TLSEnabled`), `http.Server` serves TLS connections via `tls.NewListener` (or `httpSrv.TLSConfig`).
   - `announceWebClient` prints `https://...` when TLS is enabled, and `http://...` when TLS is disabled.
   - `warnIfWebClientExposed` only emits an unencrypted HTTP warning if TLS is disabled and the server is exposed beyond loopback.
4. **Web Client & Existing Tooling:**
   - Works seamlessly over HTTPS: frontend loads via `https://` and connects WebSocket via `wss://`.
   - Update test suites/scripts (`live-terminal.spec.js`, `scripts/traffic.py`, etc.) to work with TLS enabled by default or pass `--disable-tls` where plaintext WS is expected.

## Design decisions

- **Decision:** Enable TLS by default for embedded web server, with ephemeral self-signed cert as zero-conf fallback.
  - **Why:** Secure by default. Web terminal sessions contain interactive shells with full user privileges. Enabling TLS out of the box prevents accidental unencrypted transmission of credentials and keystrokes on LANs or shared networks.
  - **Rejected:** Plaintext HTTP by default (leaves users vulnerable unless they remember to pass TLS flags).
- **Decision:** Explicit `--disable-tls` flag and `tls = false` TOML setting to opt out.
  - **Why:** Follows the established pattern in wideboi (e.g. `--disable-auto-cleanup` and `auto_cleanup = false`).
  - **Rejected:** Only boolean flags that invert meaning or have non-standard names.
- **Decision:** Support all TLS options across CLI flags, TOML config, and environment variables.
  - **Why:** Consistency across the configuration layers (`config.go`). Users configuring headless servers via config file need the exact same capabilities as CLI flag users.
- **Decision:** Support both explicit cert/key paths and zero-configuration auto-generated self-signed certificates.
  - **Why:** Makes LAN access secure immediately without requiring manual `openssl` commands, while supporting custom or CA-trusted certificates.
- **Decision:** Ephemeral in-memory self-signed certificate.
  - **Why:** Cleanest lifecycle with zero disk state, no cleanup hooks needed on shutdown/crash, no permission or stale file issues across machines.
- **Decision:** Use standard library `crypto/tls` and `crypto/x509`.
  - **Why:** Go's standard library provides robust ECDSA P-256 key generation, X.509 v3 certificate template creation, and TLS listener wrapping without adding third-party dependencies.

## Patterns to follow

- CLI flag registration: `cmd/wideboi/main.go:106-130`.
- Config loading, precedence, and validation: `internal/config/config.go:240-470`.
- Environment variable overrides: `internal/config/config.go:336-370`.
- Web server listener lifecycle and shutdown: `cmd/wideboi/main.go:394-451`.
- Web announcement and logging: `cmd/wideboi/main.go:503-521`.

## What we're NOT doing

- Automatic ACME / Let's Encrypt / HTTP-01 challenge handling (out of scope for an embedded terminal server).
- Root CA installation or trusting on the host OS / browser (mkcert or manual trust is left to the user if desired).
- Persisting generated self-signed certificates across server restarts.
- Adding mutual TLS (mTLS) client certificate authentication (token-based auth already exists).

## Open questions

None. All design decisions resolved.
