# Notes: Issue 240 - Implement HTTPS for web client

- **Worktree:** `.worktrees/issue-240-web-https` (branch `issue-240-web-https`)
- **Baseline:** `make quick` clean on worktree creation.

## Decisions & Outcomes

1. **HTTPS by default:** The embedded web server now starts with TLS enabled by default (`TLSEnabled = true`) when `--websocket` is specified.
2. **Auto self-signed fallback:** If neither `--tls-cert` nor `--tls-key` is supplied, wideboi generates an in-memory ephemeral self-signed ECDSA P-256 certificate on startup with SANs covering `localhost`, `127.0.0.1`, `::1`, and any addresses configured for the web server (including all local network interface unicast IPs if bound to wildcard/0.0.0.0).
3. **Explicit opt-out:** `--disable-tls` flag, `tls = false` in TOML config, or `WIDEBOI_DISABLE_TLS` environment variable allow disabling TLS to serve plain HTTP/WS (e.g. behind a TLS-terminating reverse proxy).
4. **Custom cert/key:** Supported via `--tls-cert` / `--tls-key` flags, `tls_cert` / `tls_key` in TOML config, and `WIDEBOI_TLS_CERT` / `WIDEBOI_TLS_KEY` environment variables.
5. **Announcement & warnings:** `announceWebClient` emits `https://` URLs when TLS is active. `warnIfWebClientExposed` only warns about unencrypted network exposure if TLS has been explicitly disabled.
6. **Testing & harness updates:**
   - Unit tests in `internal/config`, `internal/transport`, and `cmd/wideboi` cover all configuration combinations, ephemeral cert generation, and live server startup for both HTTPS and disabled TLS.
   - `web/playwright.config.js` sets `ignoreHTTPSErrors: true` and `web/tests/live-terminal.spec.js` connects over HTTPS and WSS against the live server.
   - `scripts/traffic.py` explicitly sets `tls = false` so traffic benchmarks measure unencrypted transport baselines.
   - `scripts/wssink` supports TLS dialing (`wss://`) with `InsecureSkipVerify: true`.

## Verification

- `make quick`: passed.
- `make web-accept`: passed (29 tests passed in Playwright Chromium).
- `make check`: passed (full pre-merge check: fmt-check, lint, seam-check, test, web-test, web-accept, race detector, verify-exit, smoke, attach-check).

## Review Comments Addressed

1. **`scripts/wssink` default verification:** Added explicit `-insecure` flag so verification is enabled by default unless `--insecure` is opted into.
2. **Flag conflict validation:** Added validation error in `internal/config/config.go` if both `--tls` and `--disable-tls` are passed on the command line.
3. **Wildcard port SAN generation:** Handled `:port` binds (e.g. `:8080`) in `GenerateSelfSignedCert` so an empty host after port split defaults to `0.0.0.0` and enumerates all local network interface unicast IPs.
