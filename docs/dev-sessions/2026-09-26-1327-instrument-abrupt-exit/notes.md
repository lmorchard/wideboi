# Notes: instrument abrupt session exits

## State (2026-09-26, end of execute)

Phases 1–7 are committed on `instrument-abrupt-exit`, not yet pushed. `make check`
passes, `attach-check` passed 28/28 four times, and `verify-exit` passes. Next:
Les tries a manual repro with this build, then the `pr` phase.

## What the instrumentation records

All of it goes to `$TMPDIR/wideboi-<uid>/exits.log`, one line per event:

- `server started`, and `server exit` with reason, err, startupComplete and
  autoCleanup. The reasons are `owner-left`, `shutdown-request` (with
  `requesterPID` and `requester` ancestry, e.g. `wideboi<-zsh<-claude`),
  `last-pane-closed`, `last-pane-exited` (with `panePID` and `status`),
  `context-cancelled`, `signal`, and `unspecified`.
- `server signalled signal=<name>`, written before teardown.
- `server reaped status="signal: terminated"`, from the owner client. It covers
  SIGKILL too.
- `client exit reason=...`, including the signal case, recorded from the guard.
- `sweep removed what=... by=<session>`.

The server log also gets `pane ended` for every pane. When a session ends under
the client, it prints `[wideboi: the session at … ended (<why>); see …/exits.log]`
after restoring the terminal, and still exits 0.

## Reading an incident

| exits.log shows | Meaning |
|---|---|
| `server signalled` + `server exit reason=signal` | Something signalled the server. Go can't tell us the sender; look at what ran then. |
| `reason=shutdown-request requester=…` | A kill-session, owner quit, or owner teardown. The chain says who. |
| `reason=last-pane-exited status=…` | The last pane's process ended, and this is how. |
| `server started` with no `server exit` for that pid | A hard death (SIGKILL, crash, OOM). The owner's `server reaped` line says which signal. |
| `sweep removed what=removed dead socket X.sock` while X was live | The sweep's 100ms dial wrongly judged a live server dead. |

## Findings during the session

- **Root cause found (revised):** the 15:07 recurrence on this build recorded
  a shutdown request from `wideboi.test<-go<-make<-opencode<-...`: a palette
  test types `quit` with an empty config, and command dispatch falls back to
  the real default socket. Filed separately with its own fix. The signal
  inference below was wrong.
- ~~**Incident inference**~~ (superseded, see above; research.md): the `default` server almost
  certainly died by SIGINT/TERM/HUP/QUIT delivered to its pid. The logs
  survived (so no clean exit), the socket was removed (so no crash or SIGKILL),
  and the server runs in its own session (so not the terminal).
- **Repro: 8/8 no-repro.** `scripts/repro-gotest-in-pane.sh` (unowned, private
  TMPDIR) and `scripts/repro-owner-gotest.py` (owned, websocket, real session
  dir) each ran the review's `go test -cover ./internal/... ./cmd/...` 4 times.
  The session survived every time, and no sweep removals were recorded. The
  trigger needs something neither repro has: the actual review workload, several
  concurrent agents, or something outside `go test` entirely.
- **Pre-existing race** (LESSONS.md, memory `listen-before-run-double-reader`):
  a client admitted between `ListenSocket` and `Run` gets two reader loops.
  A `server started` record placed in that window broke the upgrade e2e test
  5/8 (bisected, then confirmed by experiment). Moving the record before
  `ListenSocket` restored 0/8. The race itself is unfixed and worth an issue.
- **Tests sweep the real session dir.** `TestServerAutoCleanup`'s in-process
  clean exit runs `runAutoCleanupSweep(config.SessionDir(), …)` against the real
  `$TMPDIR/wideboi-<uid>`. Removals it makes there now show up in the real
  exits.log with `by=clean`. That's pre-existing behaviour; left alone.
- Tests whose sockets live directly in `os.TempDir()` (the upgrade e2e tests)
  write an `exits.log` into `$TMPDIR` itself. Harmless, noted.
- The server log's doubled lines are noted in memory `server-log-lines-doubled`
  and left unfixed.

## Deviations from plan

- The `server started` record moved before `ListenSocket` (race above).
- In the `TestKillSessionShutsDownAServer` extension, the check is on
  `srv.CloseReason()`, not exits.log, because that test bypasses `runServer`.
- `describeState` prefers the ProcessState over Wait's error, which is non-nil
  for any non-zero exit.
- The pane attribute is `panePID`, not `pid`: exits records already carry the
  writer's `pid`.
- The client's signal record is written in its guard, not the defer: the
  re-raise ends the process first.
- The give-up path sets `hungUp` before teardown, so the owner's guard doesn't
  try a shutdown over a dead connection.
- Flagged, not changed: an owner whose server exits cleanly but isn't reaped
  before EOF takes the reconnect path and prints the notice with
  `server exit status 0`. This happens after a kill-session. The other owner
  branch stays quiet on a clean exit.

## For Les: manual repro

1. Build: `cd .worktrees/instrument-abrupt-exit && make build`. Run wideboi
   from `bin/wideboi` as usual; that uses the default session.
2. In a terminal **outside** wideboi, run
   `tail -f "$TMPDIR/wideboi-$(id -u)/exits.log"`.
3. Run the code-review workload inside wideboi as before.
4. When it dies, the client prints the end notice, and exits.log holds the
   sequence. Also keep `default.server.log`/`default.client.log` (they survive
   any non-clean exit).
