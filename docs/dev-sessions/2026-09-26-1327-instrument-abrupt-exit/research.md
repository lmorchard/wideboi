# Research: why a wideboi session can end without a trace

## The incident (2026-09-26, `default` session)

Les started `wideboi` three times while a Claude session ran a whole-project
code review inside it; each time it exited silently after a while. Evidence
from the third, in `$TMPDIR/wideboi-501/`:

- `default.server.log`: starts 13:13:18, last line 13:13:26 (TLS handshake
  noise from the web server on :8089). No panic, no error, no shutdown line.
  Every line appears twice (see "Logging today").
- `default.client.log`: 13:15:27 `connection dropped, attempting to
  reconnect...`, 13:15:29 `reconnect failed ... connect: no such file or
  directory`. Then nothing; the client exited 0.
- The server process is gone and the socket file was removed.
- The review's subagent ran `go test -cover ./internal/... ./cmd/...` from the
  main checkout at 13:15:13, 14s before the drop. Nothing else in that window
  touched processes (transcript
  `~/.claude/projects/-Users-lmorchard-devel-mine-wideboi/8a7f0e9d-...`).
- `zoo` (older build, started 12:14) survived throughout.
- `zoo-evals` ended at 13:03 with only `server closed the connection` in its
  client log.
- Crash reports in `~/Library/Logs/DiagnosticReports/` from that day are
  `wideboi-desktop` code-signing kills of a `/tmp` dev bundle: unrelated.

### What the evidence rules in and out

- Clean exit (kill-session, last pane exit, owner left) with auto-cleanup on
  (the default) **deletes both session logs** (`cmd/wideboi/main.go:483-495`).
  Both survived, so it was not one of those paths.
- `srv.Run` returning an error would print `wideboi: ...` via `fatal`
  (`main.go:315`) to stderr, which `spawnServer` points at the server log
  (`spawn.go:65-74`). Absent.
- A crash or SIGKILL leaves the socket file behind; reconnect would then get
  ECONNREFUSED, not ENOENT.
- The server runs with `Setsid` (`spawn.go:62-63`), so the host terminal's
  SIGHUP or ^C cannot reach it.
- An upgrade exec would log `starting wideboi server` again. Absent.
- **Revised (15:07 recurrence, instrumented build): this inference was wrong.**
  `exits.log` recorded `reason=shutdown-request
  requester=wideboi.test<-go<-make<-opencode<-zsh<-wideboi<-wideboi`.
  `TestPaletteFilterAndRender` runs the palette with an empty
  `config.Config{}` and types `quit`; `commands.resolveSocket` falls back to
  `config.DefaultSocketPath()`, the live `default` session. The first
  incident's surviving logs remain unexplained, but its timing (the review's
  `go test ./cmd/...` 14s earlier) fits the same hole. Original text follows.
- Remaining fit: SIGINT/TERM/HUP/QUIT delivered to the server process. The
  guard (`main.go:416-426`) closes the server and listener (unlinking the
  socket) and re-raises, **logging nothing**. That is an inference, not a
  proof.

### Repro attempt

`scripts/repro-gotest-in-pane.sh` runs the same `go test` in a pane of a
throwaway unowned session under a short private `TMPDIR`. The server survived
a fully green run. Differences from the incident: no attached owner client, no
web server, far less load (no three concurrent subagents).

Gotcha found on the way: a `mktemp -d` TMPDIR pushes test socket paths past
macOS's 104-byte `sun_path`; the e2e tests then fail with `bind: invalid
argument` instead of running. The script uses `/tmp/wbr.XXXX`.

## Logging today

- `internal/logger/logger.go`: slog text handler. `Path(socket, component)`
  puts `<name>.server.log` / `<name>.client.log` beside the socket. `Init`
  appends; the server tees to stderr (`main.go:327`).
- Server stderr is *also* the server log (`spawn.go:65-74`), so every
  server line is written twice when spawned by an owner. Known, out of scope.
- `wideboi cleanup` removes only `*.server.log` / `*.client.log` of dead
  sessions (`cleanup.go:60-83`); any other `*.log` is skipped.

## Every way the server ends

`Server.Close` (`internal/server/server.go:2111`) is idempotent via
`closeOnce`. Callers, and whether they log:

| Trigger | Site | Logs? |
|---|---|---|
| Owner's connection EOF without detach | `server.go:350-354` | yes |
| `MsgShutdown` (kill-session, owner quit, owner's signal teardown, desktop, split cleanup) | `server.go:356-363` | **no** |
| Run's ctx cancelled | `server.go:473-474` | **no** |
| Close-pane request removed the last terminal pane | `server.go:942-952` | **no** |
| Last terminal pane exited on its own | `server.go:1142-1182` (`onPaneExit`) | **no** |
| Signal guard | `main.go:417-426` | **no** |
| Upgrade exec | `server.go:365-389` | only on failure |

After `Run` returns, `runServer` (`main.go:471-496`) decides on auto-cleanup
and returns, with no summary line.

- `MsgShutdown` senders: `main.go:576` (kill-session), `main.go:725` (owner
  guard teardown), `main.go:923` (owner quit), `control.go:226` (split that
  auto-spawned and failed), `desktop.go:487`.
- The peer's PID arrives in the handshake (`transport.Hello.PID`,
  `internal/transport/handshake.go:34-37`). `admitSocketConn`
  (`server.go:314-341`) uses it only in a warning and does not keep it.

## Signals

- `hostterm.Guard` (`internal/hostterm/guard.go:57-`) takes the signal off
  its channel, runs `Stop`, then resets and re-raises. The stop function is
  not told which signal arrived.
- Code after `srv.Run` in the signal case sleeps `signalExitMargin` while the
  re-raise kills the process, so anything written after that point never
  runs. A signal record must be written inside the guard's stop function.
- Go's `os/signal` does not expose the sender's PID.

## Panes

- `ptyx.Spawn` (`internal/server/ptyx/pane.go:97-155`) reaps each child in
  one goroutine; `exitStatus` maps a signal death to `128+sig`. `cmd.Process.Pid`
  and `cmd.ProcessState` (with `String()`, e.g. `signal: killed`) are available.
- No pane exit is logged today.

## Client side

- `spawnServer` (`cmd/wideboi/spawn.go:79-88`) reaps the server and sends
  `ProcessState.ExitCode()` on a `chan int`. That is `-1` for a signal death,
  the same value `serverExitCode` (`main.go:642`) returns on timeout.
- The owner's EOF branch (`main.go:798-835`) checks `serverExit`
  non-blockingly. If the server has not been reaped yet, it assumes an upgrade,
  tries to reconnect, and on failure `return nil // Just exit cleanly`, with
  nothing printed. That is the silent exit Les saw.
- The same reconnect-then-give-up path is how any attached non-owner client
  ends after an ordinary kill-session. `scripts/attachcheck.py:748-752`
  requires that client to exit 0.
- There is already a pattern for a notice printed once the terminal is
  restored: `printDetachNotice` (`main.go:1006`, called at `main.go:915-919`),
  checked by `attachcheck.py:236-246`.

## Auto-cleanup sweep

`runAutoCleanupSweep(config.SessionDir())` (`main.go:494`, `cleanup.go`) runs
on every clean exit and dials each `*.sock` in the real session dir with a
100ms timeout, removing any that do not answer. Nothing records what it
removed. Test children in `cmd/wideboi/cleanup_test.go` run `runServer` with
auto-cleanup on, so a `go test` run sweeps the real session dir too.
