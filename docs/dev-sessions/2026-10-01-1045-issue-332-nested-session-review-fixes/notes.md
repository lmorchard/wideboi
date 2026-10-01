# Notes: Issue 332 Review Fixes

- Worktree: `.worktrees/issue-332-nested-session-review-fixes`
- Branch: `issue-332-nested-session-review-fixes`
- Status: Completed implementation and full verification.

## Changes Addressed
1. Preserved `TERM_PROGRAM`: Removed `TERM_PROGRAM=wideboi` assignment in `internal/server/server.go` and stopped stripping it in `ptyx/pane.go`. Removed `TERM_PROGRAM` from `isNestedSession` in `cmd/wideboi/main.go`.
2. Guarded fallback nested spawn in `connectOrSpawn`: Added `isNestedSession && !isAllowNested` check before `spawnServer` in `cmd/wideboi/control.go`, preventing `split` from starting a nested server on missing sockets.
3. Fixed error message hint: Updated error to recommend `--allow-nested` or `WIDEBOI_ALLOW_NESTED=1` instead of `unset WIDEBOI`.
4. Accurate SSH documentation: Updated `docs/MANUAL.md` to note OpenSSH `SendEnv LC_*` / `AcceptEnv LC_*` configuration requirements.
5. Hardened tests and test isolation: Added `WIDEBOI_ALLOW_NESTED` to unsets in `testenv.Run` and `isolation_test.go`. Switched `server_test.go` to exact line matching and verified `TERM_PROGRAM=wideboi` is absent. Tested `connectOrSpawn` refusal safely in `cli_test.go`.
