# Notes: Issue 119 - Auto Cleanup Configuration Flag

- Session started for issue #119.
- Worktree: `.worktrees/issue-119-auto-cleanup`
- Branch: `issue-119-auto-cleanup`

## Decisions & Outcomes
- Implemented `auto_cleanup = true` by default, configurable in TOML and via `WIDEBOI_AUTO_CLEANUP`.
- When `auto_cleanup` is enabled and server exits cleanly (`!signalled.Load() && err == nil`), server unlinks its own logs and tokens before releasing the listener flock (preventing successor races), then sweeps dead sessions in `config.SessionDir()`.
- Never sweeps arbitrary parent directories when a custom socket path is used via `WIDEBOI_SOCK`.
- When terminated by signal (SIGTERM, SIGINT, SIGHUP, SIGQUIT) or error, or when `auto_cleanup = false`, logs are preserved for forensic post-mortem analysis.
- `*.lock` files remain untouched to preserve flock safety rules (per LESSONS.md).
- Updated `config.example.toml`, `README.md`, and CLI `--help`.

## Verification
- Unit tests: `TestLoadAutoCleanup` in `internal/config/config_test.go`
- Integration tests: `TestServerAutoCleanup` in `cmd/wideboi/cleanup_test.go` (covering clean exit, disabled flag, signal-driven exit, and teardown error exit)
- Full test gate (`make check`): `fmt-check`, `lint`, `seam-check`, `test`, `web-test`, `web-accept`, `race`, `verify-exit`, `smoke`, `attach-check` all green.
- Addressed Copilot code review:
  - Own logs/tokens removed before releasing flock to prevent successor race
  - Restricted directory sweeps to dedicated `config.SessionDir()` to protect arbitrary socket parent directories
  - Added test coverage for signal exit and error exit log preservation
  - Added `on`/`off` to parser diagnostic error message
