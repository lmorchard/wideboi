# Plan: Issue 274 Small cleanups

## Phase 1: Docs & Low-Risk Quick Fixes
Addresses items:
- Item 1: CLAUDE.md premise pointers
- Item 2: cmd/wideboi/main.go doc comment on runKillSession vs runUpgradeServer
- Item 3: scripts/seam-check.sh allowlist messages
- Item 6: codec.go:66 BinPath validUTF8
- Item 7: logger.Log unused export removal

### Files
- `CLAUDE.md`: replace `server.go:240` and `layout.go:247` with symbol names (`Strip.ColumnWidth` in `internal/layout/layout.go`; the `resizePanesLocked` / PTY-sizing comment in `internal/server/server.go`).
- `cmd/wideboi/main.go`: move `runKillSession` doc comment to `runKillSession`; add doc comment to `runUpgradeServer`.
- `scripts/seam-check.sh`: replace "Plan 1 temporaries" on lines 67 and 82 with generic allowlist phrasing.
- `internal/protocol/codec.go`: wrap `m.BinPath` in `validUTF8(m.BinPath)` in `MsgUpgradeRequest` marshalling.
- `internal/logger/logger.go`: remove `var Log *slog.Logger = slog.Default()` and directly pass `slog.New(newHandler(w, level))` to `slog.SetDefault`.

### Verification — automated
- [x] `make quick` passes (fmt, vet, seam-check, go test, web-test)
- [x] `go test ./internal/logger` passes
- [x] `go test ./internal/protocol` passes

### Verification — manual
- [x] Inspect `git diff` for Phase 1 changes to confirm clean, minimal edits

---

## Phase 2: Protocol, Config & Command Registry Cleanups
Addresses items:
- Item 4: Macro conversion deduplication (`encodeMacros`, `decodeMacros`, `toml` tags on `protocol.Macro`, `Config` aliases)
- Item 5: `config.Load` `parseBoolEnv` and TLS env handling (`DISABLE_TLS=0` enabled, conflict detection)
- Item 8: `commands.Registry` independent pointer allocation & deduplication by name
- Item 9: WebSocket token prefix and `wideboi.v%d` shared helpers in `internal/transport`
- Item 10: `MsgSaveMacros` sequential write queue via worker channel
- Item 11: `internal/commands` `shellQuote`/`shellJoin` table tests

### Files
- `internal/protocol/messages.go`: add `toml` tags to `MacroStep` and `Macro`.
- `internal/protocol/codec.go`: add `encodeMacros` and `decodeMacros` helper functions, replacing the 4 manual loops.
- `internal/config/config.go`:
  - `type MacroConfig = protocol.Macro`, `type MacroStepConfig = protocol.MacroStep`.
  - Simplify `c.ResolvedMacros()` and `SaveMacrosFile()` using `protocol.Macro` directly.
  - Implement `parseBoolEnv(name, val string) (bool, error)`.
  - Update `config.Load` to parse `WIDEBOI_AUTO_CLEANUP`, `WIDEBOI_TLS`, and `WIDEBOI_DISABLE_TLS` with `parseBoolEnv`. Fix `DISABLE_TLS=0` and check for conflicting values when both TLS env vars are set.
- `internal/config/config_test.go`: add test cases for `parseBoolEnv`, `WIDEBOI_DISABLE_TLS=0/false`, and conflicting TLS env vars.
- `internal/commands/registry.go`:
  - In `Register(cmd Command)`, allocate `ref := new(Command); *ref = cmd`. If a command with the same name exists, update `r.commands` in place and clear old aliases.
- `internal/commands/registry_test.go`:
  - Add tests for duplicate command registration and pointer stability across appends.
  - Add table-driven tests for `shellQuote` and `shellJoin`.
- `internal/transport/websocket.go`:
  - Add `WebSocketSubprotocol() string`.
  - Add `WebSocketTokenFromSubprotocols(subprotocols []string) string`.
  - Add `CheckWebSocketToken(subprotocols []string, expected string) bool`.
- `internal/server/server.go`:
  - Use `transport.WebSocketSubprotocol()` and `transport.WebSocketTokenFromSubprotocols`.
  - Add `saveMacrosChan chan []protocol.Macro` to serialize macro saving to a single worker goroutine.
- `internal/desktop/gateway.go`:
  - Use `transport.WebSocketSubprotocol()` and `transport.CheckWebSocketToken`.

### Verification — automated
- [x] `go test -v ./internal/config` passes
- [x] `go test -v ./internal/commands` passes
- [x] `go test -v ./internal/protocol` passes
- [x] `go test -v ./internal/transport` passes
- [x] `go test -v ./internal/server` passes (including `macros_test.go`)
- [x] `make quick` passes

### Verification — manual
- [x] Verify `commands.Registry` does not duplicate command entries or leak stale aliases on re-registration.
- [x] Verify `config.Load` error message when `WIDEBOI_TLS=1` and `WIDEBOI_DISABLE_TLS=1` are both set.

---

## Phase 3: Tooling, Tests & Scripts Cleanups
Addresses items:
- Item 12: `internal/server/ptyx` unit tests for `Close`, `Resize`, `Adopt`, `setsize`
- Item 13: `.github/workflows/ci.yml` `[noci]` check
- Item 14: `Makefile` `web/dist` rule font dependencies (`web/public/**`)
- Item 15: `ptylib.pinned_env()` and `wait_until()` in `scripts/ptylib.py`, used by `attachcheck.py` and `traffic.py`
- Item 16: `scripts/smoke.py` resize sleeps replaced with output wait + settle ceiling
- Item 17: `make prune-worktrees` target checking `gh pr view`

### Files
- `internal/server/ptyx/pane_test.go`: add tests for `Resize`, `Close`, `Adopt` (running and pre-exited), and `setsize` on invalid/closed files.
- `.github/workflows/ci.yml`: check out first (or fetch head commit) and check both PR title and head commit message for `[noci]`.
- `Makefile`:
  - Add `$(shell find web/public -type f 2>/dev/null)` to `web/dist` dependencies.
  - Add `prune-worktrees` target.
- `scripts/prune-worktrees.sh`: script that lists worktrees, checks `gh pr view`, verifies clean state, and removes merged/closed worktrees.
- `scripts/ptylib.py`: define `pinned_env(overlay=None)` and `wait_until(pred, timeout, what, interval=0.05)`.
- `scripts/attachcheck.py`: use `ptylib.pinned_env()`.
- `scripts/traffic.py`: use `ptylib.pinned_env()` and `ptylib.wait_until()`.
- `scripts/smoke.py`: replace the two 1.5s sleeps in `case_resizing_host_propagates_sigwinch_to_child` and `case_partly_clipped_pane_keeps_full_width` with waiting for post-resize output and calling `settle_output`.

### Verification — automated
- [x] `go test -v -cover ./internal/server/ptyx` coverage increases significantly (>70%, achieved 83.2%)
- [x] `python3 scripts/attachcheck.py` passes
- [x] `python3 scripts/smoke.py` passes (including the resize tests)
- [x] `make prune-worktrees` dry-run or execution works cleanly
- [x] `make quick` passes
- [x] `make check` passes cleanly

### Verification — manual
- [x] Verify `smoke.py` runs faster without the 3.0s total fixed resize sleep overhead.
