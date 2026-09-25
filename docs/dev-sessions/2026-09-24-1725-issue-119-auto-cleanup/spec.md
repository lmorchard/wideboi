# Auto Cleanup Configuration Flag Spec

**Goal:** Provide an `auto_cleanup` configuration option (enabled by default) that automatically cleans up dead session artifacts and removes the current session's logs upon clean exit, while preserving logs when an exit is unexpected or signal-driven.

**Source:** https://github.com/lmorchard/wideboi/issues/119

## Current state

- A manual `wideboi cleanup` subcommand exists (`cmd/wideboi/cleanup.go:24-93`) which removes legacy logs, dead sockets, dead session logs (`*.server.log`, `*.client.log`), and dead web tokens in `config.SessionDir()`.
- Server exit currently unlinks only its own socket (`internal/transport/socket.go:175`) and web token (`cmd/wideboi/main.go:382`), leaving session logs on disk forever.
- Configuration loading (`internal/config/config.go:150-374`) uses layered defaults -> TOML -> environment -> CLI flags. Boolean flags like `mouse` use pointer fields (`*bool`) so absence defaults to true (`internal/config/config.go:38, 346`).

## Desired end state

- `auto_cleanup` boolean configuration option in TOML (default: `true`).
- `WIDEBOI_AUTO_CLEANUP` environment variable override (`"false"`, `"0"`, `"no"` to disable; `"true"`, `"1"`, `"yes"` to enable).
- Resolved into `config.Config.AutoCleanupEnabled` (bool, default `true`).
- When `AutoCleanupEnabled` is `true`:
  - On normal, clean server shutdown (not terminated by signal, no error from `srv.Run`), the server invokes `runCleanup(io.Discard, dir)`.
  - Since the server's socket is closed prior to cleanup, the current session is recognized as dead, and its `.server.log` and `.client.log` are removed along with any other dead session artifacts.
- When `signalled.Load()` is true or server exits with an error:
  - Auto-cleanup does NOT run; logs and forensic evidence are preserved.
- When `auto_cleanup` is explicitly `false`:
  - Auto-cleanup does NOT run on exit; artifacts remain on disk for manual `wideboi cleanup`.
- Documentation (`config.example.toml`, `README.md`) documents `auto_cleanup` and `WIDEBOI_AUTO_CLEANUP`.

## Design decisions

- **Decision:** Enable `auto_cleanup` by default (`auto_cleanup = true`), requiring explicit configuration to disable.
  - **Why:** Keeps the session directory tidy for normal use without requiring manual intervention, while still allowing developers to retain logs if desired.
  - **Rejected:** `auto_cleanup = false` default was rejected per user preference to prioritize disk cleanliness during daily driving.

- **Decision:** Remove the current session's log files on clean exit.
  - **Why:** On a clean, intentional exit, routine debug logs are rarely needed.
  - **Rejected:** Retaining current logs until the next session was considered, but it leaves dangling logs until an unrelated future session runs.

- **Decision:** Skip auto-cleanup on signal termination or server error.
  - **Why:** Preserves forensic evidence in the log files when an unexpected termination occurs (crash, panic, or kill signal).
  - **Rejected:** Cleaning unconditionally on any exit would destroy crash diagnostics right when needed.

- **Decision:** Run auto-cleanup in the server process post-`srv.Run`.
  - **Why:** The server is the authoritative session owner (always present for the session's lifetime, including headless or detached sessions).
  - **Rejected:** Running in client was rejected because clients may detach without ending the session, or sessions may be terminated via `kill-session` without an attached client.

- **Decision:** Clean `filepath.Dir(cfg.Socket)` as well as `config.SessionDir()` (if distinct).
  - **Why:** Supports custom socket paths specified by `WIDEBOI_SOCK` or test harnesses, cleaning up the directory where that socket and its logs actually reside.

## Patterns to follow

- Pointer-based optional boolean in `Config` struct (`*bool` + `BoolEnabled bool`), matching `Mouse` / `MouseEnabled`:
  - `internal/config/config.go:38-39`
  - `internal/config/config.go:221-223`
  - `internal/config/config.go:346`
- Environment variable boolean parsing: check non-empty string, parse standard truthy/falsy values (`"0"`, `"false"`, `"no"`, `"1"`, `"true"`, `"yes"`).
- `runCleanup(w io.Writer, dir string)` in `cmd/wideboi/cleanup.go:24-93`.
- Server exit handling in `cmd/wideboi/main.go:400-413`.

## What we're NOT doing

- We are NOT removing `*.lock` files. As documented in `docs/LESSONS.md`, deleting lock files reopens socket ownership races.
- We are NOT implementing log rotation or size capping; that is separate log management.
- We are NOT adding a CLI flag `--auto-cleanup` (TOML and environment variable are sufficient and match `mouse` configuration).
- We are NOT removing the manual `wideboi cleanup` subcommand; it remains for on-demand cleanup and when auto-cleanup is disabled.

## Open questions

*(None. All design decisions confirmed.)*
