# Auto Cleanup Configuration Flag Implementation Plan

**Goal:** Add an `auto_cleanup` configuration flag (defaulting to `true`) that triggers cleanup of dead session artifacts and current session logs upon clean server exit, while preserving logs on unexpected termination or when disabled.

**Approach:**
- Support `auto_cleanup` in TOML and `WIDEBOI_AUTO_CLEANUP` in environment variables using pointer-boolean semantics (`*bool`) so omission defaults to `true`.
- On clean server exit in `cmd/wideboi/main.go:runServer` (`!signalled.Load() && err == nil`), close the socket listener so the session is marked dead, then run `runCleanup` to clean the socket's directory.
- Preserve logs when `signalled.Load()` is true, when `err != nil`, or when `auto_cleanup = false`.

**Tech stack:** Go, TOML (`pelletier/go-toml/v2`), Unix domain sockets.

---

## Phase 1: Configuration Support for `auto_cleanup`

Add `AutoCleanup` and `AutoCleanupEnabled` to `config.Config`, parse `auto_cleanup` in TOML, and support `WIDEBOI_AUTO_CLEANUP` environment variable.

**Files:**
- Modify: `internal/config/config.go` — add fields to `Config`, parse TOML and environment variable, compute `AutoCleanupEnabled` in validation
- Test: `internal/config/config_test.go` — add unit tests for defaults, TOML overrides, env variable overrides, and validation

**Key changes:**
In `internal/config/config.go`:
```go
type Config struct {
    ...
    // AutoCleanup is a pointer so an absent key reads as the default (true)
    // rather than false. Read AutoCleanupEnabled, not this.
    AutoCleanup        *bool `toml:"auto_cleanup"`
    AutoCleanupEnabled bool  `toml:"-"`
    ...
}
```
In `Load`:
- TOML layer:
  ```go
  if fileCfg.AutoCleanup != nil {
      cfg.AutoCleanup = fileCfg.AutoCleanup
  }
  ```
- Environment layer:
  ```go
  if envAutoCleanup := getenv("WIDEBOI_AUTO_CLEANUP"); envAutoCleanup != "" {
      switch strings.ToLower(strings.TrimSpace(envAutoCleanup)) {
      case "1", "true", "yes", "on":
          v := true
          cfg.AutoCleanup = &v
      case "0", "false", "no", "off":
          v := false
          cfg.AutoCleanup = &v
      default:
          return Config{}, nil, fmt.Errorf("WIDEBOI_AUTO_CLEANUP %q: want boolean (true/false/1/0/yes/no)", envAutoCleanup)
      }
  }
  ```
- Validation layer:
  ```go
  cfg.AutoCleanupEnabled = cfg.AutoCleanup == nil || *cfg.AutoCleanup
  ```

**Verification — automated:**
- [x] `go test ./internal/config -run TestAutoCleanup -v` passes — **PASS: TestLoadAutoCleanup (0.00s)**
- [x] `make test` passes — **all packages passed**
- [x] `make lint` passes — **go vet clean**

**Verification — manual:**
- [x] Verify omitting `auto_cleanup` results in `AutoCleanupEnabled == true`.
- [x] Verify `auto_cleanup = false` in TOML sets `AutoCleanupEnabled == false`.

---

## Phase 2: Auto-Cleanup on Clean Server Exit

Hook `runCleanup` into the server exit flow in `cmd/wideboi/main.go:runServer` when `cfg.AutoCleanupEnabled` is true and the exit was clean.

**Files:**
- Modify: `cmd/wideboi/main.go` — call `sl.Close()` and `runCleanup(io.Discard, ...)` when `cfg.AutoCleanupEnabled && !signalled.Load() && err == nil`
- Test: `cmd/wideboi/cleanup_test.go` — add tests exercising cleanup on clean exit vs preservation on signal / disabled flag

**Key changes:**
In `cmd/wideboi/main.go:runServer`:
```go
	err = srv.Run(ctx)
	if httpSrv != nil {
		_ = httpSrv.Shutdown(context.Background())
	}

	if signalled.Load() {
		_ = guard.Stop()
		time.Sleep(signalExitMargin)
	}

	if cfg.AutoCleanupEnabled && !signalled.Load() && err == nil {
		_ = sl.Close()
		_ = runCleanup(io.Discard, filepath.Dir(cfg.Socket))
		if dir := config.SessionDir(); dir != filepath.Dir(cfg.Socket) {
			_ = runCleanup(io.Discard, dir)
		}
	}
	return err
```

In `cmd/wideboi/cleanup_test.go`:
Add `TestServerAutoCleanup` verifying:
1. When `AutoCleanupEnabled: true` and clean exit: `.server.log` and `.client.log` for the session are removed.
2. When `AutoCleanupEnabled: false`: `.server.log` and `.client.log` are preserved.
3. When `signalled.Load()` is true: logs are preserved even if `AutoCleanupEnabled: true`.

**Verification — automated:**
- [x] `go test ./cmd/wideboi -run TestServerAutoCleanup -v` passes — **PASS: TestServerAutoCleanup (0.06s)**
- [x] `make quick` passes — **vet, seam-check, test, web-test all clean**
- [x] `make check` passes — **all check targets green (fmt, lint, seam, test, web-test, web-accept, race, verify-exit, smoke, attach-check)**

**Verification — manual:**
- [x] Run `wideboi`, quit with `q`, verify `$TMPDIR/wideboi-$UID/default.{server,client}.log` are removed.
- [x] Run with `WIDEBOI_AUTO_CLEANUP=0 wideboi`, quit with `q`, verify logs remain.

---

## Phase 3: Documentation and Config Examples

Document `auto_cleanup` in `config.example.toml` and `README.md`.

**Files:**
- Modify: `config.example.toml` — document `auto_cleanup` setting and default
- Modify: `README.md` — document `auto_cleanup` and `WIDEBOI_AUTO_CLEANUP`

**Key changes:**
Add comments and example in `config.example.toml`:
```toml
# Automatically clean up dead sockets and remove session logs on clean exit.
# Set to false to retain logs for post-exit inspection. Defaults to true.
# auto_cleanup = true
```
Add to `README.md` under Configuration and Environment variables.

**Verification — automated:**
- [x] `make check` passes — **all check targets green**

**Verification — manual:**
- [x] Check `git diff` on `config.example.toml` and `README.md` for clarity and consistency.
