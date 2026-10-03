# Plan: Pre-loading Status Dashboard as a Startup Column (Issue #380)

**Goal:** Allow users to designate a startup column in `config.toml` that pre-loads the status dashboard (with a configured width like 28 cols), defaulting focus to the first interactive shell pane.

**Approach:** Extend `StartupPane` in `config` and `server` packages with `Type` and `Dashboard` fields and an `IsDashboard()` predicate. Teach server attach handler to spawn the dashboard pane when configured, setting its column width, and focusing the first interactive pane.

**Tech stack:** Go, TOML configuration, Wideboi server strip layout.

---

## Phase 1: Configuration Support

Extend `StartupPane` in `internal/config/config.go` and `internal/server/server.go`.

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/server/server.go`
- Modify: `cmd/wideboi/main.go`
- Test: `internal/config/config_test.go`

**Key changes:**
- Add `Type string` and `Dashboard bool` to `config.StartupPane` and `server.StartupPane`.
- Add `IsDashboard() bool` predicate on both structs.
- Forward `Type` and `Dashboard` in `cmd/wideboi/main.go`.

**Verification — automated:**
- [x] `go test -v ./internal/config -run TestStartupDashboardConfig` passes — **verified parsing type="dashboard", dashboard=true, and type="status"**

---

## Phase 2: Server Spawning & Focus Handling

Teach server to spawn dashboard panes on attach and focus the first interactive pane.

**Files:**
- Modify: `internal/server/server.go` — add `spawnDashboardPaneWithWidthLocked(width, afterPaneID)`
- Modify: `internal/server/handlers.go` — handle `spec.IsDashboard()` in `handleAttachLocked`
- Test: `internal/server/server_test.go` — add `TestStartupDashboardPane`

**Key changes:**
- `spawnDashboardPaneWithWidthLocked`: takes width parameter, sets column width and VT width accordingly.
- `handleAttachLocked`: spawns dashboard pane if `spec.IsDashboard()`, tracks `firstInteractiveID`, and focuses it.

**Verification — automated:**
- [x] `go test -v ./internal/server -run TestStartupDashboardPane` passes — **verified column creation, width, focus on shell, and duplicate prevention**
- [x] `make quick` passes cleanly — **`go vet`, `seam-check`, Go test suite, and 182 Vitest web tests passed**
