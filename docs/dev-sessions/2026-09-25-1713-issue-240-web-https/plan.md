# HTTPS for Web Client Implementation Plan

**Goal:** Enable secure HTTPS and WSS connections by default for wideboi's embedded web client, supporting custom certificates/keys, automatic ephemeral self-signed certificates, and full configuration across CLI flags, TOML config, and environment variables.

**Approach:** Follow the pattern of `AutoCleanup` in `internal/config/config.go` to provide TLS enabled by default (`TLSEnabled = true`) with explicit opt-out (`--disable-tls`, `tls = false`). In `internal/transport`, generate an in-memory ephemeral self-signed ECDSA P-256 certificate when no cert/key paths are provided, covering `localhost`, `127.0.0.1`, `::1`, and any address in `--websocket`. Serve TLS on the HTTP server in `cmd/wideboi/main.go`, update connection announcements and exposure warnings, and update tests and documentation.

**Tech stack:** Go `crypto/tls`, `crypto/x509`, `crypto/ecdsa`, `crypto/elliptic`, Playwright/TypeScript for web tests.

---

## Phase 1: Configuration & CLI Flags

Support TLS configuration across `internal/config` (structs, precedence, validation) and `cmd/wideboi` (flag parsing and help).

**Files:**
- Modify: `internal/config/config.go` — add TLS fields, parsing, precedence, and validation
- Test: `internal/config/config_test.go` — test defaults, TOML parsing, env overrides, flag overrides, validation errors
- Modify: `cmd/wideboi/main.go` — add `-disable-tls`, `-tls`, `-tls-cert`, `-tls-key` flags and help text

**Key changes:**
- In `internal/config/config.go`:
  ```go
  // In Config:
  TLS         *bool  `toml:"tls"`
  TLSEnabled  bool   `toml:"-"`
  TLSCert     string `toml:"tls_cert"`
  TLSKey      string `toml:"tls_key"`

  // In ConfigFlags:
  DisableTLS bool
  TLS        bool
  TLSCert    string
  TLSKey     string
  ```
- In `config.Load`:
  - Default: `cfg.TLSEnabled = true` (determined by `cfg.TLS == nil || *cfg.TLS`).
  - TOML parsing: copy `fileCfg.TLS`, `fileCfg.TLSCert`, `fileCfg.TLSKey`.
  - Environment variables:
    - `WIDEBOI_TLS` ("true"/"false"/"1"/"0"/"yes"/"no"/"on"/"off")
    - `WIDEBOI_DISABLE_TLS` ("true"/"1"/"yes"/"on")
    - `WIDEBOI_TLS_CERT` (path)
    - `WIDEBOI_TLS_KEY` (path)
  - CLI flags:
    - If `flags.DisableTLS`: `v := false; cfg.TLS = &v`
    - If `flags.TLS`: `v := true; cfg.TLS = &v`
    - If `flags.TLSCert != ""`: `cfg.TLSCert = flags.TLSCert`
    - If `flags.TLSKey != ""`: `cfg.TLSKey = flags.TLSKey`
  - Validation:
    - If `cfg.TLSCert != ""` && `cfg.TLSKey == ""`: return error `"both tls_cert and tls_key must be specified"`
    - If `cfg.TLSCert == ""` && `cfg.TLSKey != ""`: return error `"both tls_cert and tls_key must be specified"`
    - If `cfg.TLSCert != ""` && `cfg.TLSKey != ""` && cfg.TLS == nil: auto-enable `cfg.TLS = &true`
    - `cfg.TLSEnabled = cfg.TLS == nil || *cfg.TLS`
- In `cmd/wideboi/main.go`:
  - Register `-disable-tls`, `-tls`, `-tls-cert`, `-tls-key` in `NewFlagSet`.
  - Add help documentation to usage output.

**Verification — automated:**
- [x] `go test -v ./internal/config -run TestLoadTLS` passes — **12 subtests passed**
- [x] `make quick` passes — **all Go and web tests passed**

**Verification — manual:**
- [x] `bin/wideboi --help` displays `--tls`, `--disable-tls`, `--tls-cert`, and `--tls-key` descriptions — **verified with `go run ./cmd/wideboi --help`**

---

## Phase 2: Self-Signed Certificate Generation & TLS Transport Config

Generate in-memory ephemeral self-signed ECDSA P-256 certificates and prepare `*tls.Config` for the web server.

**Files:**
- Create: `internal/transport/tls.go`
- Test: `internal/transport/tls_test.go`

**Key changes:**
- `GenerateSelfSignedCert(hosts []string) (tls.Certificate, error)`:
  - Generates ECDSA key on P-256 (`elliptic.P256()`).
  - Constructs `x509.Certificate` template with:
    - Large crypto-random SerialNumber.
    - `Subject: pkix.Name{CommonName: "wideboi", Organization: []string{"wideboi"}}`.
    - `NotBefore: time.Now().Add(-1 * time.Hour)`.
    - `NotAfter: time.Now().Add(365 * 24 * time.Hour)`.
    - `KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment`.
    - `ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}`.
    - SANs: parses IP addresses and hostnames. Always includes `127.0.0.1`, `::1`, `localhost`. If `0.0.0.0` or empty host, discovers all unicast interface addresses from `net.InterfaceAddrs()` and adds them to `IPAddresses`.
  - Creates self-signed certificate using `x509.CreateCertificate`.
  - Returns `tls.Certificate{Certificate: [][]byte{derBytes}, PrivateKey: privKey}`.
- `LoadOrGenerateTLSConfig(certFile, keyFile string, wsAddr string) (*tls.Config, error)`:
  - If `certFile != "" && keyFile != ""`: calls `tls.LoadX509KeyPair(certFile, keyFile)`.
  - Else: calls `GenerateSelfSignedCert(deriveHosts(wsAddr))`.
  - Returns `&tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}`.

**Verification — automated:**
- [x] `go test -v ./internal/transport -run TestGenerateSelfSignedCert` passes — **generated valid ECDSA P-256 cert with SANs**
- [x] `go test -v ./internal/transport -run TestLoadOrGenerateTLSConfig` passes — **ephemeral generation and file loading verified**
- [x] `make quick` passes — **all tests pass**

**Verification — manual:**
- [x] Inspect generated X.509 certificate fields (ECDSA P-256, SANs including localhost, loopback, and LAN IPs) — **verified in unit tests and x509 parsed fields**.

---

## Phase 3: Server Listener & Announcement Integration

Wire TLS configuration into `cmd/wideboi/main.go` web server startup, update connection URLs and exposure warnings.

**Files:**
- Modify: `cmd/wideboi/main.go` — start TLS listener, update `announceWebClient`, update `warnIfWebClientExposed`
- Test: `cmd/wideboi/web_token_test.go` — test HTTPS announcement and exposed warnings
- Test: `cmd/wideboi/tls_server_test.go` (new) — test live server serving HTTPS & WSS with self-signed cert and with `--disable-tls`

**Key changes:**
- In `cmd/wideboi/main.go:runServer`:
  - If `cfg.Websocket != ""`:
    - If `cfg.TLSEnabled`:
      - Load or generate TLS config:
        ```go
        tlsConfig, err := transport.LoadOrGenerateTLSConfig(cfg.TLSCert, cfg.TLSKey, cfg.Websocket)
        if err != nil {
            return fmt.Errorf("configure tls: %w", err)
        }
        wsListener = tls.NewListener(wsListener, tlsConfig)
        ```
    - Check exposure: `warnIfWebClientExposed(os.Stderr, slog.Default(), wsListener.Addr(), cfg.TLSEnabled)`.
      Only warn if exposed beyond loopback AND unencrypted (`!cfg.TLSEnabled`).
    - Pass `cfg.TLSEnabled` to `announceWebClient`:
      - Scheme: `https` if `cfg.TLSEnabled`, else `http`.
      - Message: `wideboi: web client listening at https://%s/#token=%s\n`
- New tests:
  - Verify HTTPS handshake, GET `/` returns HTML over TLS, and `/ws` upgrades to WebSocket over TLS (`wss`).
  - Verify `--disable-tls` serves plain HTTP and `http://` announcement.

**Verification — automated:**
- [x] `go test -v ./cmd/wideboi -run TestTLS` passes — **both HTTPS/WSS and disabled TLS tests passed**
- [x] `go test -v ./cmd/wideboi -run TestAnnounceWebClient` passes — **https:// and http:// schemes verified**
- [x] `make quick` passes — **all tests pass**

**Verification — manual:**
- [x] Run `bin/wideboi server --websocket 127.0.0.1:8888` in terminal, verify it announces `https://127.0.0.1:8888/#token=...` without unencrypted warning — **verified via unit tests & server startup**.

---

## Phase 4: Test Suite Adaptations, Documentation & Release Verification

Update existing end-to-end tests, scripts, examples, and documentation to reflect TLS by default.

**Files:**
- Modify: `web/playwright.config.js` — add `ignoreHTTPSErrors: true` to `use` block
- Modify: `web/tests/live-terminal.spec.js` — update server readiness check to HTTPS (with `rejectUnauthorized: false`) and URL to `https://`
- Modify: `scripts/traffic.py` — add `tls = false` to `traffic.toml` generated config so traffic benchmarks stay HTTP/WS
- Modify: `config.example.toml` — document `tls`, `tls_cert`, `tls_key`
- Modify: `README.md` and `docs/MANUAL.md` — document HTTPS by default, `--disable-tls`, cert/key configuration

**Key changes:**
- `web/playwright.config.js`:
  ```js
  use: { baseURL: 'http://127.0.0.1:4179', browserName: 'chromium', ignoreHTTPSErrors: true }
  ```
- `web/tests/live-terminal.spec.js`:
  - Readiness polling uses HTTPS (or `node:https` request with `rejectUnauthorized: false`).
  - Page navigation to `https://127.0.0.1:${port}/#token=${token}`.
- `scripts/traffic.py`:
  - Adds `tls = false` to generated `traffic.toml`.
- Documentation in `config.example.toml`, `README.md`, `docs/MANUAL.md`.

**Verification — automated:**
- [x] `make quick` passes — **all Go and web unit tests pass**
- [x] `make web-accept` passes (browser acceptance test with live HTTPS server) — **all 29 tests pass including live-terminal HTTPS**
- [x] `make check` passes (full suite: fmt-check, lint, seam-check, test, web-test, web-accept, race, verify-exit, smoke, attach-check) — **full pre-merge check passed cleanly**

**Verification — manual:**
- [x] Inspect `git diff` to confirm clean changes and complete docs — **diff reviewed and verified**
