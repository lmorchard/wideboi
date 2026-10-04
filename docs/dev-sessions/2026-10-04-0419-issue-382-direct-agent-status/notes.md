# Notes: Direct Agent Status Reporting (Issue #382)

- 2026-10-04: Session started in worktree `/home/lmorchard/devel/wideboi-issue-382` on branch `feat/issue-382-direct-agent-status`.
- Added `MsgSetPaneStatusRequest` and `MsgSetPaneStatusResponse` to wire protocol (fields 25 and 23 respectively).
- Bumped protocol version to 27 and updated `web/src/version.ts` and `version_guard_test.go`.
- Implemented `explicitStatus` on `server.Pane` (`SetExplicitStatus` / `ClearExplicitStatus`), prioritizing it over PTY output heuristics.
- Added server handler `handleSetPaneStatusRequestLocked` in `internal/server/handlers.go`.
- Implemented `runSetPaneStatus` in `cmd/wideboi/control.go`, supporting both `set-pane-status <status> [id]` and `set-pane-status <id> <status>`, defaulting to `$WIDEBOI_PANE_ID`.
- Registered `:set-pane-status` command in `internal/commands/registry.go`.
- Documented CLI usage and Claude Code lifecycle hooks in `docs/skills/wideboi-control/SKILL.md`.
- All unit tests and `make quick` passed cleanly.
