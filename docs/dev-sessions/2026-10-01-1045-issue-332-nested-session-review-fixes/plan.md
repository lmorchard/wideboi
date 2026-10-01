# Implementation Plan: Issue 332 Review Fixes

## Proposed Changes

### Phase 1: Clean Up `TERM_PROGRAM`
1. `internal/server/server.go`:
   - In `paneEnvLocked`, remove `TERM_PROGRAM=wideboi`. Keep `WIDEBOI=1` and `LC_WIDEBOI=1`.
2. `internal/server/ptyx/pane.go`:
   - In `Spawn`, do not strip `TERM_PROGRAM`. Strip `WIDEBOI_SOCK`, `WIDEBOI_SESSION`, `WIDEBOI`, `LC_WIDEBOI`, and `WIDEBOI_PANE_ID`.
3. `cmd/wideboi/main.go`:
   - In `isNestedSession`, remove `getenv("TERM_PROGRAM") == "wideboi"`.
4. `cmd/wideboi/cli_test.go`:
   - Remove `{"TERM_PROGRAM", map[string]string{"TERM_PROGRAM": "wideboi"}}` from `TestCheckNestedSession`.

### Phase 2: Refuse Nested Spawns in `connectOrSpawn` & Fix Error Hint
1. `cmd/wideboi/control.go`:
   - In `connectOrSpawn`, before calling `spawnServer`:
     ```go
     if isNestedSession(os.Getenv) && !isAllowNested(os.Getenv) {
         return nil, false, errors.New("already running inside a wideboi session (refusing to spawn a nested session; use --allow-nested or WIDEBOI_ALLOW_NESTED=1 to force)")
     }
     ```
2. `cmd/wideboi/main.go`:
   - Update error string in `checkNestedSession` to reference `--allow-nested` or `WIDEBOI_ALLOW_NESTED=1`.
3. `cmd/wideboi/cli_test.go`:
   - Test `connectOrSpawn` refusal when nested:
     - When `WIDEBOI=1` and `WIDEBOI_ALLOW_NESTED` is unset, `connectOrSpawn` returns the refusal error immediately.
     - Note: DO NOT test the allowed branch by calling `connectOrSpawn` with a nonexistent socket, because during tests `spawnServer` executes `wideboi.test`, causing a recursive fork bomb! Instead, test `isAllowNested` or verify the refusal error logic directly.

### Phase 3: Documentation & Test Hardening
1. `docs/MANUAL.md`:
   - Qualify SSH nested session detection by explaining the required locale forwarding configuration (`SendEnv LC_*` / `AcceptEnv LC_*`).
2. `internal/testenv/testenv.go`:
   - Add `WIDEBOI_ALLOW_NESTED` to the list of environment variables unset in `testenv.Run`.
3. `cmd/wideboi/isolation_test.go`:
   - Add `WIDEBOI_ALLOW_NESTED` to `TestSuiteIsIsolatedFromRealSessions`.
4. `internal/server/server_test.go`:
   - In `TestServerSpawnsPanesWithWideboiEnvironment`, split output into lines/sets to verify exact variable matching (`WIDEBOI=1` independent of `LC_WIDEBOI=1`), and assert that `TERM_PROGRAM=wideboi` is NOT present.

## Verification
- `go test ./...`
- `make check`
