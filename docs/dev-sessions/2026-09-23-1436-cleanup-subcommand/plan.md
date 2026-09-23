# Plan: Cleanup Subcommand

## 1. Add `cleanup` subcommand to CLI
- **File:** `cmd/wideboi/main.go`
- Add `"cleanup"` to the usage text and switch statement.
- Under `case "cleanup":`, call `runCleanup(os.Stdout, config.SessionDir())`.

## 2. Implement `runCleanup`
- **File:** `cmd/wideboi/cleanup.go` (new file)
- **Logic:**
  1. Remove legacy `server.log` and `client.log` unconditionally.
  2. Find all `*.sock` files. Dial each to check if alive. If dead, collect the prefix (`name.sock` -> `name`).
  3. Find all `*.log` files (that are not the legacy ones). For each `name.server.log` or `name.client.log`, check if `name` is active (dial its socket if we haven't already).
  4. If a session is deemed dead, delete its `.sock`, `.server.log`, and `.client.log`.
  5. Print what was deleted (or a summary) to `io.Writer`.
- Note: Do NOT delete `*.lock` files.

## 3. Tests
- **File:** `cmd/wideboi/cleanup_test.go`
- Write tests that create a mock directory with:
  - Active session (socket + logs + lock) -> should be kept.
  - Dead session (socket + logs + lock) -> socket and logs removed, lock kept.
  - Legacy logs (`server.log`, `client.log`) -> removed.
  - Dangling logs (log with no socket) -> removed.
- Use `net.Listen("unix", ...)` to simulate an active session socket.

