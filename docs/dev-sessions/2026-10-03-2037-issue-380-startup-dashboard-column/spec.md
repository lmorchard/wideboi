# Spec: Pre-loading Status Dashboard as a Startup Column (Issue #380)

**Goal:** Allow users to designate a startup column in `config.toml` that pre-loads the status dashboard (with a configured width like 28 cols), defaulting focus to the first interactive shell pane.

**Source:** https://github.com/lmorchard/wideboi/issues/380

---

## Current State

- `[[startup]]` in `config.toml` only supports command-backed PTY panes (`Command` and `Width`).
- The status dashboard can only be created dynamically via `<prefix> s` or `:toggle-status`.
- Startup panes are all spawned via `spawnPaneWithSpecLocked`.

---

## Desired End State

1. **Config Syntax:**
   - `[[startup]]` supports `type = "dashboard"` (or `type = "status"`) and `dashboard = true`:
     ```toml
     [[startup]]
     type = "dashboard"
     width = 28

     [[startup]]
     width = 80
     ```
2. **Server Startup Handling:**
   - When launching startup panes on initial attach, if a spec designates a dashboard pane:
     - Invoke `spawnDashboardPaneWithWidthLocked(spec.Width, 0)`.
     - Set `statusPaneID` and initialize the in-memory dashboard.
     - Skip duplicate dashboard specs if more than one is configured.
3. **Initial Focus:**
   - Focus the first interactive terminal pane (e.g. the shell at column 1) rather than the dashboard pane so the user's terminal input is ready immediately.
   - If only dashboard panes are configured, fall back to focusing the dashboard pane.

---

## What We're NOT Doing

- We are not changing the `<prefix> s` toggle behavior; if a dashboard pane already exists from startup, `<prefix> s` focuses it as before.
- We are not implementing pinned columns here; pinning is tracked separately under #373.

---

## Verification Plan

- Unit test in `internal/config/config_test.go`:
  - Verify parsing `type = "dashboard"`, `type = "status"`, and `dashboard = true`.
- Integration tests in `internal/server/server_test.go`:
  - `TestStartupDashboardPane`: Configure startup with a dashboard pane (width 28) and shell pane (width 80).
  - Verify layout has 2 columns: column 0 is dashboard (width 28), column 1 is shell (width 80).
  - Verify focus is on the shell pane (column 1).
  - Verify pressing `<prefix> s` focuses the existing dashboard pane without creating a second one.
- Run `make quick` and ensure clean passes.
