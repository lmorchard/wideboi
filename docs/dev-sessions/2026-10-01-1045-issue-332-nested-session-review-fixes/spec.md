# Spec: Issue 332 Review Fixes (Nested Session Refusal)

## Motivation & Background
PR #354 merged the initial implementation of refusing nested wideboi sessions (#332). However, review feedback highlighted several important correctness, capability-preservation, and testing issues that need resolution.

## Scope of Fixes

1. **Preserve `TERM_PROGRAM` in Pane Environment**:
   - `internal/server/server.go` currently sets `TERM_PROGRAM=wideboi` in spawned panes.
   - Per `docs/TERMINAL-METADATA.md:129-140`, applications like Claude Code check `TERM_PROGRAM` for recognized terminal values (e.g. `ghostty`, `iTerm.app`) to enable terminal progress bar sequences (OSC 9;4). Setting `TERM_PROGRAM=wideboi` disables this.
   - Rely solely on the dedicated `WIDEBOI=1`, `LC_WIDEBOI=1`, and `WIDEBOI_PANE_ID` environment variables for nesting detection. Do not overwrite or strip `TERM_PROGRAM`.
   - Remove `TERM_PROGRAM` from nesting checks in `cmd/wideboi/main.go`.

2. **Refuse Nested Spawns in `connectOrSpawn` (Catching `split` Fallback)**:
   - When `wideboi split` targets a non-existent or fresh socket, it calls `connectOrSpawn`, which starts an owned server.
   - If running inside a wideboi pane without `--allow-nested` or `WIDEBOI_ALLOW_NESTED=1`, `connectOrSpawn` must refuse to spawn a new nested server, rather than allowing a nested server to start.
   - Unit test this guard *without* executing `spawnServer` during `go test`, avoiding the recursive test fork bomb that broke PR 356.

3. **Improve Error Message Hint**:
   - In `cmd/wideboi/main.go`, change the error message from:
     `already running inside a wideboi session (refusing to nest sessions; use --allow-nested or unset WIDEBOI to force)`
     to:
     `already running inside a wideboi session (refusing to nest sessions; use --allow-nested or WIDEBOI_ALLOW_NESTED=1 to force)`
     because unsetting only `WIDEBOI` leaves `LC_WIDEBOI` and `WIDEBOI_PANE_ID` set, which would still trigger refusal.

4. **SSH Documentation Accuracy**:
   - In `docs/MANUAL.md`, qualify the claim about nested detection across SSH to explicitly state that remote propagation requires locale forwarding configuration such as `SendEnv LC_*` in `ssh_config` and `AcceptEnv LC_*` in `sshd_config`.

5. **Test Isolation & Exact Assertions**:
   - In `internal/testenv/testenv.go`, unset `WIDEBOI_ALLOW_NESTED` at the start of `testenv.Run` so developer environment variables cannot affect test execution.
   - In `internal/server/server_test.go` (`TestServerSpawnsPanesWithWideboiEnvironment`), check exact line-bounded entries rather than substring matching `WIDEBOI=1\n` (which could falsely match `LC_WIDEBOI=1\n`). Verify that `TERM_PROGRAM=wideboi` is not present.
