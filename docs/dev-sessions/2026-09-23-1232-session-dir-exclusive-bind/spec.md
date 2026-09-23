# Named sessions and an exclusive bind Spec

**Goal:** Let one user run several wideboi sessions side by side, by name. Make "who owns this socket" a single atomic decision, so two plain `wideboi` launched together end up in one session instead of one error or two sessions.

**Source:** #27, #86 (scoped together 2026-09-23; #26 deferred with a comment)

## Current state

See `research.md` for the refs.

- One session per machine: the path is `$TMPDIR/wideboi-<uid>/default.sock` (`config.DefaultSocketPath`, config.go:63-67). Precedence: default < TOML `socket` < `WIDEBOI_SOCK` < `-s`. `defaultSocketPath()` in main.go:32-43 is dead code.
- Plain `wideboi` either dials (and attaches) or spawns a server (`run`, main.go:318-327). `NewSocketListener` does probe, remove, listen with no lock (socket.go:105-132). The race in #86 is between those three steps.
- If a spawned server fails for any reason, its owner client gets EOF before any message and prints "exited during startup" (main.go:444-445). The client is the server's parent (`spawnServer`, spawn.go:25-63, `go cmd.Wait()`), but it throws the exit status away.
- Every server and client appends to `$TMPDIR/wideboi-<uid>/{server,client}.log` (logger.go:47-57), whatever the socket.
- No locking and no session listing anywhere.

## Desired end state

- `wideboi -L work` starts or attaches the session `work` at `$TMPDIR/wideboi-<uid>/work.sock`. `attach`, `kill-session` and `server` all honour `-L`/`--session`, `WIDEBOI_SESSION`, and TOML `session = "..."`. With no name, the session is `default`, exactly as today.
- `wideboi ls` (alias `list-sessions`) prints the names of the live sessions in the uid directory, one per line, sorted. It prints nothing and exits 0 when there are none.
- The detach notice names the session as the user would type it: nothing for `default`, `-L <name>` for other sessions in the uid dir, `-s <path>` otherwise.
- A server holds an exclusive `flock` on `<socket>.lock` for its whole life. A second server for the same socket exits with code 3 (session taken), and its message still contains "already listening". It never touches the socket.
- Two plain `wideboi` started at once on one socket: exactly one server survives, both clients end up in the same session, and the socket file stays in place.
- Logs sit beside the socket: remove `.sock` from the socket path and add `.server.log` / `.client.log` (for example `$TMPDIR/wideboi-<uid>/default.server.log`). The startup-failure message points at that file.

## Design decisions

- **A name is shorthand for a path.** `<uid dir>/<name>.sock`. A valid name matches `^[A-Za-z0-9_][A-Za-z0-9_.-]*$`; anything else is an error.
  - **Why:** everything downstream already keys off `cfg.Socket`, so one resolution step covers attach, kill-session, server and plain `wideboi`.
  - **Resolution:** each layer (TOML, env, flags) can set a name or a path. A later layer overrides an earlier one. Setting both a name and a path in the same layer is an error.
- **`-L` / `--session`.**
  - **Why:** it matches tmux's `-L` (socket name). In wideboi, one server = one socket = one session.
  - **Rejected:** `-t` (tmux uses it for a session within a server, which wideboi doesn't have), `-n`, and a positional name (it clashes with subcommand detection in `parseCLI`).
- **`ls` lists live sessions only.** It dials each `*.sock` and prints the ones that answer. It never deletes anything.
  - **Why:** stale sockets get reclaimed on the next bind. Cleanup belongs under the lock, not in a read-only command.
  - **Rejected:** a `(stale)` marker (noise), and client counts (needs a protocol message).
- **The flock is held for the server's whole life, taken before the socket is touched.** `LOCK_EX|LOCK_NB` on `<socket>.lock`, created 0600. The file is never deleted.
  - **Why:** a held lock then means a live server; the kernel drops it on SIGKILL. A socket whose lock we hold is stale by definition, so removing it is safe. That closes the second variant in #86 by construction, and the probe dial goes away. Deleting the lock file would reopen the race: a waiter can lock an unlinked inode while a newcomer locks a fresh file.
  - **Rejected:** locking only across probe, remove and listen, which still depends on the dial probe to decide whether a server is alive.
- **The owner learns "taken" from the exit status.** A server that can't get the lock exits with code 3. `spawnServer` returns the wait result as well as the connection. When the owner gets startup EOF with status 3, it dials the socket with a ceiling wait (the winner holds the lock and is about to listen) and attaches as a non-owner. Any other status keeps today's error.
  - **Why:** the client is already the parent, so there's no guessing and no protocol change.
  - **Rejected:** a message on the owner fd (a one-off pre-pump protocol message), and redialling on every failure (real failures would wait out the ceiling, and it guesses at the cause).
- **Per-session logs beside the socket.**
  - **Why:** each path is unique by construction, so test harnesses with private dirs get private logs. `logger.Init`/`Path` take a base path instead of deriving the uid dir.
  - **Rejected:** the uid dir by basename, because attachcheck's private `default.sock` would then write into the real default session's log.

## Patterns to follow

- Precedence layering in `config.Load` (config.go:71-195). Add `Session` next to `Socket` in `fileConfig`, `ConfigFlags` and env.
- Flag and subcommand plumbing in `parseCLI` (main.go:64-119). `-L`/`--session` joins the value-taking `skipNext` list (81-88). `ls`/`list-sessions` join the subcommand list (76).
- The error-message style in `runAttach`/`runKillSession` (main.go:293-314).
- Waits are ceilings: the redial uses the existing ceiling style (`shutdownCeiling`/`detachCeiling`), not a sleep.
- Isolated sockets in tests: `MkdirTemp` for darwin's 104-byte limit (main_test.go:163). attachcheck's private `RUNTIME_DIR` (attachcheck.py:72-104).
- Keep the "refuse to remove a non-socket" guard (socket.go:111-118).

## Tests

- Go (transport): a second `NewSocketListener` on the same path fails with a taken error while the first is alive, and leaves the first's socket file in place. After the first closes, a new one binds. A stale socket file left behind (no lock held) is reclaimed. flock conflicts between two opens in the same process, so this works in-process.
- Go (config/cli): name validation; name vs path precedence across layers; same-layer conflict error; `-L` parsing before and after subcommands; the three forms of the detach notice; log path derivation.
- attachcheck: `-L` sessions are independent (two names → two servers; `ls` lists both; kill one, the other survives). Two plain `wideboi` launched together via a pipe release: one server, both clients in one session, socket present. Update "second server refuses to steal" to assert exit code 3.
- Prove each new race test fails on the current code before trusting it. Run the timing-sensitive cases four times.

## What we're NOT doing

- **#26 reconnect handshake**, and any protocol versioning.
- **Client counts or attached/detached status in `ls`.** No new protocol messages.
- **Deleting stale sockets or `.lock` files from `ls`,** or any cleanup sweep.
- **Choosing a session automatically** (tmux's "most recent"). No name still means `default`.
- **Nesting detection** (a `$WIDEBOI` env var in panes, like `$TMUX`).
- **Listing `-s` sessions outside the uid dir.**
- **Log rotation,** and migrating or removing old `server.log`/`client.log`.
- **Anything to do with the untracked websocket transport work** in the main checkout.

## Open questions

- *Renaming the logs changes the path in the README for existing users.* Default: accept it, and update README.md:128,181-194 and the attachcheck hint (attachcheck.py:179).
- *Exit code 3 might clash with something.* Default: 3 is free (1 = error, 128+n = signals). Name it as a constant in `cmd/wideboi`.
