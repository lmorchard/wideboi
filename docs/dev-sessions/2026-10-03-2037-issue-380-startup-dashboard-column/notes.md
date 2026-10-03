# Notes: Pre-loading Status Dashboard as a Startup Column (Issue #380)

- 2026-10-03: Session started in worktree `/home/lmorchard/devel/wideboi-issue-380` on branch `feat/issue-380-startup-dashboard-column`.
- Added `Type` and `Dashboard` fields to `config.StartupPane` and `server.StartupPane` along with `IsDashboard() bool` predicate.
- Updated `cmd/wideboi/main.go` to forward `Type` and `Dashboard` into server configuration.
- Added `spawnDashboardPaneWithWidthLocked(width, afterPaneID)` to `internal/server/server.go`.
- Updated `handleAttachLocked` in `internal/server/handlers.go` to spawn dashboard columns on initial attach, ignore duplicate dashboard specifications, and default focus to the first interactive terminal pane.
- Added test coverage in `internal/config/config_test.go` and `internal/server/server_test.go`.
- Hardened `TestPipePaneCommand` against goroutine connect timing.
- All tests and `make quick` passed cleanly.
