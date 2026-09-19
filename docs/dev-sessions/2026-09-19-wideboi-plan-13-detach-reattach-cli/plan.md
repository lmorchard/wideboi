# Plan 13 Implementation Plan — Detach & Reattach CLI Plumbing

**Goal:** Implement `C-b d` detach verb, CLI commands for `server` and `attach`, and socket connection handling in `cmd/wideboi`.

## Tasks

### Task 1: Detach verb in router & status bar
- Add `routeDetach` in `cmd/wideboi/router.go` for `d` in control mode.
- Update `controlVerbs` in `internal/client/client.go` to include `d detach`.
- Handle `routeDetach` in `main.go` event loop (clean exit without process teardown).

### Task 2: CLI subcommand parsing & Unix socket wiring
- Add subcommand handling in `cmd/wideboi/main.go` for `wideboi server` and `wideboi attach`.
- Wire `transport.NewSocketListener` for server mode and `net.Dial` for attach mode.

### Task 3: Verification & Smoke/Unit Tests
- Add unit tests for `routeDetach` in `cmd/wideboi/router_test.go`.
- Run `make check`.
- Commit, push branch `plan-13-detach-reattach-cli`, and open PR.
