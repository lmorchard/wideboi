# Research: Issue 274 Small cleanups

## 1. Docs
- **CLAUDE.md lines 38-40**: References `internal/server/server.go:240` (now `SetOwner`) and `internal/layout/layout.go:247` (inside `MoveColumn`). Replace with symbol names: `Strip.ColumnWidth` in `internal/layout/layout.go` and the `resizePanesLocked` / PTY-sizing-from-ColumnWidth comment in `internal/server/server.go:1312-1319`.
- **cmd/wideboi/main.go lines 609-611**: Doc comment `// runKillSession ends the session at cfg.Socket...` is placed directly above `func runUpgradeServer(...)`. Move it above `func runKillSession(...)` at line 622.
- **scripts/seam-check.sh lines 67, 82**: "Plan 1 temporaries" appears in error hint and summary output. Update to generic allowlist wording.

## 2. Protocol / Config
- **Macro conversion deduplication**:
  - `internal/protocol/codec.go`: loops in `MarshalClient` (lines 68-85), `UnmarshalClient` (lines 155-172), `MarshalServer` (lines 291-308), `UnmarshalServer` (lines 435-452). Add `encodeMacros([]Macro) []*wirepb.Macro` and `decodeMacros([]*wirepb.Macro) []Macro`.
  - `internal/protocol/messages.go`: Add `toml` struct tags to `Macro` and `MacroStep`.
  - `internal/config/config.go`: `MacroConfig` and `MacroStepConfig` can be aliases to `protocol.Macro` and `protocol.MacroStep`. `ResolvedMacros()` and `SaveMacrosFile()` can avoid manual element-by-element loop copying.
- **config.Load boolean-env parsing**:
  - `internal/config/config.go:377-411`: `WIDEBOI_AUTO_CLEANUP`, `WIDEBOI_TLS`, and `WIDEBOI_DISABLE_TLS` duplicate boolean parsing.
  - Add `parseBoolEnv(name, val string) (bool, error)`.
  - Eliminate order-dependence between `WIDEBOI_TLS` and `WIDEBOI_DISABLE_TLS`. If both are specified with conflicting meanings (`tlsVal == disableTLSVal`), return an error. If `DISABLE_TLS` is false/0/no/off, it means TLS is enabled.
- **codec.go:66 BinPath validUTF8**:
  - `internal/protocol/codec.go:66`: `UpgradeRequest: &wirepb.MsgUpgradeRequest{BinPath: validUTF8(m.BinPath)}`.
- **logger.Log unused export**:
  - `internal/logger/logger.go:13,103`: `var Log *slog.Logger = slog.Default()`. Remove `var Log` and use `slog.SetDefault(slog.New(newHandler(w, level)))`.
- **commands.Registry slice pointer aliasing & duplicate names**:
  - `internal/commands/registry.go:33-44`: `ref := &r.commands[len(r.commands)-1]` stores pointers into a slice that reallocates on append.
  - Allocate command pointer independently (`ref := new(Command); *ref = cmd`), update existing command by name if already registered to avoid duplicates in `r.commands`, and clean up any previous aliases in `byAlias`.
- **WebSocket token prefix and wideboi.v%d helpers**:
  - `internal/transport/websocket.go`: Export `WebSocketSubprotocol() string` (using `protocol.Version`) and helpers for token extraction/validation `WebSocketTokenFromSubprotocols(subprotocols []string) string` and `CheckWebSocketToken(subprotocols []string, expected string) bool`.
  - Use these shared helpers in `internal/server/server.go:229,2212,2224` and `internal/desktop/gateway.go:46,112-127`.
- **MsgSaveMacros out-of-order writes**:
  - `internal/server/server.go:907-914`: `MsgSaveMacros` currently spawns `go fn(macrosCopy)`.
  - Process macro saves sequentially via a dedicated channel and single worker goroutine to prevent out-of-order disk writes.

## 3. Tests / Tooling
- **commands shellQuote/shellJoin coverage**:
  - Add `registry_test.go` table-driven tests for `shellQuote` and `shellJoin` testing empty string, bare words, spaces, quotes, special shell characters (`$`, `*`, `\`, `;`, `|`, etc.).
- **ptyx test coverage**:
  - Test `p.Resize(cols, rows)`, `p.Close()`, `ptyx.Adopt` (running and already exited), and `setsize` on invalid/closed fd.
- **.github/workflows/ci.yml [noci]**:
  - `github.event.head_commit.message` is empty on `pull_request`. Run checkout first, then check `git log -1 --format=%B` as well as `github.event.pull_request.title`.
- **Makefile web/dist dependencies**:
  - Update `web/dist` target to include `$(shell find web/public -type f 2>/dev/null)` so font additions or updates trigger rebuild.
- **ptylib.pinned_env() and wait_until**:
  - In `scripts/ptylib.py`: provide `pinned_env(overlay=None)` and `wait_until(pred, timeout, what, interval=0.05)`.
  - Use `pinned_env()` in `scripts/attachcheck.py:93` and `scripts/traffic.py:190`. Use `ptylib.wait_until` in `scripts/traffic.py`.
- **scripts/smoke.py resize sleeps**:
  - In lines 1044 and 1077, replace `time.sleep(1.5)` with waiting for output past the pre-resize length and calling `settle_output(s.drainer, timeout=2.0)`.
- **Makefile prune-worktrees target**:
  - Add `scripts/prune-worktrees.sh` and `make prune-worktrees` that inspects `.worktrees/*`, checks `gh pr view <branch> --json state`, and removes merged/closed worktrees if clean.
