# Research: Pre-loading Status Dashboard as a Startup Column (Issue #380)

## 1. Context & Motivation

Wideboi supports configuring initial columns via `[[startup]]` in `config.toml` (`internal/config/config.go`):
```toml
[[startup]]
width = 80
```
However, every configured startup pane is currently assumed to be a PTY process running a shell or command (`spawnPaneWithSpecLocked`).

The status dashboard (`internal/server/dashboard.go`, `<prefix> s`, #196) is spawned dynamically via `spawnDashboardPaneLocked`.

With responsive layout (#379) now in place, the dashboard can adapt to narrow drawer widths (20–35 cols). Allowing `[[startup]]` to designate a dashboard column enables sessions to launch immediately with a dedicated status drawer on the left.

## 2. Code Locations

- `internal/config/config.go`:
  - `StartupPane`: struct deserialized from TOML. Currently has `Command string` and `Width int`.
- `cmd/wideboi/main.go`:
  - Copies `cfg.Startup` to `server.StartupPane` and passes to `srv.SetStartupPanes()`.
- `internal/server/server.go`:
  - `StartupPane`: Server representation.
  - `spawnDashboardPaneLocked(afterPaneID int)`: Currently creates dashboard with default preset width.
- `internal/server/handlers.go`:
  - `handleAttachLocked()`: Loops over `s.startup` on first attach and spawns each column.

## 3. Design Choices

1. **Config Syntax**:
   - Support both `type = "dashboard"` (or `"status"`) and `dashboard = true` on `[[startup]]`.
   - Optional `width = 28` sets the initial column width.
2. **Focus Semantics**:
   - When a session starts with both dashboard and interactive shell panes, initial focus should land on the first interactive shell pane so the user can begin typing immediately.
3. **Idempotency**:
   - Ensure duplicate dashboard definitions in `[[startup]]` are ignored, preserving the invariant that at most one status dashboard pane exists in the session.
