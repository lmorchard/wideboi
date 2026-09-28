# Notes: Issue 311

- Branch: `issue-311-target-active-session`
- Worktree: `.worktrees/issue-311-target-active-session`
- Issue: #311

## Summary of Changes

1. **Active Session Environment Injection:**
   - Modified `internal/config/config.go:applySessionLayer` to allow both `session != ""` and `socket != ""` when `socket == SessionSocketPath(session)`.
   - Updated `internal/server/ptyx/pane.go:Spawn` to accept optional extra environment variables, cleanly stripping any inherited `WIDEBOI_SOCK` / `WIDEBOI_SESSION` before injecting the active session values.
   - Updated `internal/server/server.go` and `internal/server/pane.go` so `Server` records its active session and socket and injects `WIDEBOI_SOCK` (and `WIDEBOI_SESSION` if named) into every pane it spawns.
   - Updated `cmd/wideboi/main.go:runServer` to configure `srv.SetSession(cfg.Session, cfg.Socket)`.

2. **Web Client Native Command Palette & Prompt:**
   - Created `<wideboi-command-palette>` (`web/src/components/command-palette.ts`), an interactive dialog with search input, live filtering, and keyboard navigation (Up/Down/Enter/Escape).
   - Added command prompt execution support: typing `: <command>` executes the command directly without spawning ephemeral panes.
   - Wired `wideboi-app.ts` so `Ctrl+B Space` opens the Command Palette, `Ctrl+B :` opens prompt mode, and mobile command menu entries summon the native palette/prompt dialogs.
   - Removed ephemeral pane spawning via `wideboi prompt` / `wideboi palette` commands in `web/src/wideboi-app.ts`.

3. **Console TUI Native Prompt & Palette:**
   - Added in-process status bar command prompt (`:`) in `internal/client/prompt.go` and `internal/client/statusbar.go`. Keystrokes edit the prompt line and Enter executes via `commands.DefaultRegistry.Execute`.
   - Added in-process floating modal command palette (`Space`) in `internal/client/palette.go` with live search, arrow key navigation, and execution via `commands.DefaultRegistry.Execute`.
   - Updated `cmd/wideboi/router.go` and `cmd/wideboi/main.go` to route prompt and palette keys and actions directly in-process.
   - Removed detach file triggers (`detachTriggerDir`, `detachTriggerFile`, `createDetachTrigger`, `checkDetachTrigger`).

## Verification Results

- `make fmt-check`: PASS
- `make lint`: PASS
- `make test`: PASS (all Go package unit tests)
- `make web-test`: PASS (20 test files, 155 tests)
- `make web-accept`: PASS (50 Playwright browser tests)
- `make race`: PASS (Go tests under race detector with `-count=1`)
- `make verify-exit`: PASS (all 7 exit signal/size tests)
- `make smoke`: PASS (41 smoke tests)
- `make golden`: PASS
- `make attach-check`: PASS (29 attach tests)
- `make check`: PASS
