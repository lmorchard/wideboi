# Spec: Issue 274 Small cleanups

## Goal
Resolve the 17 small cleanup items identified in the 2026-09-26 review across documentation, protocol/config, and test/tooling infrastructure.

## Scope & Non-Goals
- Scope is strictly the 17 items listed in Issue 274.
- Non-goal: Any wire protocol schema changes (fields, semantics) or version bumps. The wire messages remain identical; only code duplication and handling details are addressed.

## Checklists
### Docs
- [x] CLAUDE.md premise pointers are stale: replaced with symbol names (`Strip.ColumnWidth` in `internal/layout/layout.go`; the `resizePanesLocked` / PTY-sizing-from-ColumnWidth comment in `internal/server/server.go`).
- [x] `main.go:~552`: runKillSession's doc comment moved to `runKillSession`; doc comment added for `runUpgradeServer`.
- [x] `scripts/seam-check.sh:67,81` updated to generic allowlist wording.

### Protocol / config
- [x] Macro conversion deduplicated via `encodeMacros`/`decodeMacros` in `codec.go`; toml tags added to `protocol.Macro`; config uses `type MacroConfig = protocol.Macro`.
- [x] `config.Load`: added `parseBoolEnv`, order dependence between `WIDEBOI_TLS` and `WIDEBOI_DISABLE_TLS` resolved, `DISABLE_TLS=0` enables TLS, and conflicting env values return an error.
- [x] `codec.go:66`: `BinPath` passed through `validUTF8`.
- [x] `logger.Log` unused export removed.
- [x] `registry.go:33-44`: commands allocated on heap (`new(Command)`), duplicate names update existing entry and clean up old aliases.
- [x] WebSocket token prefix / `wideboi.v%d` shared helpers added in `internal/transport/websocket.go` and used in both `server.go` and `gateway.go`.
- [x] `MsgSaveMacros` queues saves sequentially on a worker channel to prevent out-of-order disk writes.

### Tests / tooling
- [x] `internal/commands` `shellQuote`/`shellJoin`: added table tests in `registry_test.go`.
- [x] `internal/server/ptyx`: unit tests for `Close`, `Resize`, `Adopt` (running and already exited), `PID` added in `pane_test.go`; coverage increased from 49.6% to 83.2%.
- [x] `.github/workflows/ci.yml:13-22`: checkout run first and `[noci]` checked against both PR title and head commit message.
- [x] Makefile `web/dist` rule lists `web/public/**` (fonts).
- [x] Env pinning and `wait_until` unified in `ptylib.py` (`pinned_env` and `wait_until`); used in `attachcheck.py` and `traffic.py`.
- [x] Resize sleeps in `smoke.py` replaced with waiting for redraw marker and `settle_output`.
- [x] Added `make prune-worktrees` target and `scripts/prune-worktrees.sh` that checks `gh pr view` before removing merged/closed worktrees.

## Detailed Changes

### 1. Documentation
1. **CLAUDE.md premise pointers**: Replace line number references `server.go:240` and `layout.go:247` with symbol names: `Strip.ColumnWidth` in `internal/layout/layout.go` and the `resizePanesLocked` / PTY-sizing-from-ColumnWidth comment in `internal/server/server.go`.
2. **cmd/wideboi/main.go doc comment**: Move the misplaced `runKillSession` doc comment from above `runUpgradeServer` to `runKillSession`. Add a concise doc comment for `runUpgradeServer`.
3. **scripts/seam-check.sh allowlist messages**: Update lines 67 and 82 to remove "Plan 1 temporaries" and refer generically to allowlisted crossings.

### 2. Protocol and Config
4. **Macro conversion deduplication**:
   - Add `encodeMacros(macros []Macro) []*wirepb.Macro` and `decodeMacros(pb []*wirepb.Macro) []Macro` in `internal/protocol/codec.go`.
   - Use them across `MarshalClient`, `UnmarshalClient`, `MarshalServer`, `UnmarshalServer`.
   - Add `toml` tags (`toml:"text,omitempty"`, `toml:"key,omitempty"`, etc.) to `protocol.MacroStep` and `protocol.Macro`.
   - In `internal/config/config.go`, define `type MacroConfig = protocol.Macro` and `type MacroStepConfig = protocol.MacroStep`.
   - Simplify `c.ResolvedMacros()` and `SaveMacrosFile()` to use `protocol.Macro` directly without manual field-by-field conversion loops.
5. **Boolean environment variable parsing in `config.Load`**:
   - Add `parseBoolEnv(name, val string) (bool, error)`.
   - Use `parseBoolEnv` for `WIDEBOI_AUTO_CLEANUP`, `WIDEBOI_TLS`, and `WIDEBOI_DISABLE_TLS`.
   - Fix `DISABLE_TLS=0`: when false, it enables TLS (`!disabled`).
   - If both `WIDEBOI_TLS` and `WIDEBOI_DISABLE_TLS` are set and conflict (`tlsVal == disableVal`), return an error rather than silently prioritizing one based on evaluation order.
6. **BinPath validUTF8 in `codec.go`**:
   - In `MsgUpgradeRequest` marshalling, pass `m.BinPath` through `validUTF8(m.BinPath)`.
7. **Unused exported `logger.Log`**:
   - In `internal/logger/logger.go`, remove the unused `var Log *slog.Logger = slog.Default()`.
   - In `Init()`, call `slog.SetDefault(slog.New(newHandler(w, level)))` directly.
8. **Commands Registry slice pointer aliasing & deduplication**:
   - In `internal/commands/registry.go`, store independent pointer allocations (`ref := new(Command); *ref = cmd`) so slice appending in `r.commands` does not alias or dangle.
   - When registering a command whose name is already registered, update the entry in `r.commands` and remove old aliases from `r.byAlias`.
9. **WebSocket subprotocol & token helper sharing**:
   - In `internal/transport/websocket.go`, export `WebSocketSubprotocol() string`, `WebSocketTokenFromSubprotocols(subprotocols []string) string`, and `CheckWebSocketToken(subprotocols []string, expected string) bool`.
   - Use these in `internal/server/server.go` and `internal/desktop/gateway.go`.
10. **MsgSaveMacros sequential persistence**:
    - In `internal/server/server.go`, serialize macro save operations via a channel and worker loop instead of unbounded `go fn(...)` goroutines per message, preventing out-of-order writes.

### 3. Tests and Tooling
11. **commands shellQuote/shellJoin coverage**:
    - Add unit tests in `internal/commands/registry_test.go` covering `shellQuote` and `shellJoin` with table-driven test cases (empty, simple, quotes, spaces, special chars `$`, `*`, `\`, `;`, `|`, etc.).
12. **ptyx test coverage**:
    - Add unit tests in `internal/server/ptyx/` for `p.Resize()`, `p.Close()`, `ptyx.Adopt()`, and `setsize` on invalid/closed files.
13. **.github/workflows/ci.yml `[noci]` check**:
    - Run checkout first, then inspect both `github.event.pull_request.title` and `git log -1 --format=%B` so `[noci]` works on PRs.
14. **Makefile web/dist dependencies**:
    - Add `$(shell find web/public -type f 2>/dev/null)` to `web/dist` prerequisites.
15. **ptylib.pinned_env() and wait_until**:
    - Add `pinned_env(overlay=None)` and `wait_until(pred, timeout, what, interval=0.05)` in `scripts/ptylib.py`.
    - Update `scripts/attachcheck.py` and `scripts/traffic.py` to use them.
16. **scripts/smoke.py resize sleeps**:
    - Replace the two fixed 1.5s sleeps in `smoke.py` lines 1044 and 1077 with waiting for post-resize output and calling `settle_output(s.drainer, timeout=2.0)`.
17. **make prune-worktrees target**:
    - Add `scripts/prune-worktrees.sh` and Makefile target `prune-worktrees` that inspects `.worktrees/*`, verifies PR state via `gh pr view`, checks for clean working tree, and removes merged/closed worktrees.

## Verification
- `make quick`: fmt-check, lint, seam-check, go test, web-test
- `make check`: full gate including race, exit verification, smoke, attachcheck
- Unit tests for ptyx, registry shellQuote/shellJoin, and config bool env parsing pass with increased coverage.
