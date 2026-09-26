# Instrument abrupt session exits

**Goal:** When a wideboi session ends, leave a durable record of why and how,
so the next unexplained exit can be diagnosed from files instead of guessed at.

**Source:** Les, 2026-09-26: three sessions exited silently during a
whole-project code review. See `research.md`.

## Current state

Only one of seven server shutdown triggers logs anything (`research.md`,
"Every way the server ends"). The signal guard tears down silently. A clean
exit deletes the session's logs. The owner client can't tell a signal death
from a timeout. When the server vanishes, the client exits 0 without a word.

## Desired end state

1. **`exits.log`** beside the session sockets (`filepath.Dir(socket)/exits.log`).
   Append-only, shared by every session in that dir, one slog text line per
   event, never removed by auto-cleanup or `wideboi cleanup`. Each line carries
   `session` (socket base name), `pid`, `component` (`server` / `client` /
   `sweep`), and the event's own fields. Events:
   - **server exit:** the close reason and its details, plus `err`,
     `signalled`, `startupComplete`, and whether auto-cleanup ran.
   - **server signalled:** signal name, written by the guard before teardown.
   - **client exit:** why the client is exiting (quit, detach, server gone,
     reconnect failed and why, error), plus, for an owner, the server's reaped
     status (e.g. `exit status 0`, `signal: terminated`, `signal: killed`) or
     `not reaped within <ceiling>`.
   - **sweep:** each socket, token or log removed, and by which session.
2. **Close reasons on the server.** Every `Close` trigger records a reason
   before closing: `owner-left`, `shutdown-request`, `last-pane-closed`,
   `last-pane-exited`, `context-cancelled`, `signal`, or `unspecified` for
   callers that didn't say. The first reason wins. Details:
   - `shutdown-request`: requester PID (from its hello) and its process
     ancestry as executable names only, e.g. `wideboi<-zsh<-claude<-zsh`.
   - `last-pane-exited`: pane id, child pid, and wait status.
   The reason also goes to the server log at Info.
3. **Pane exits logged** at Info in the server log: pane id, pid, wait status
   string.
4. **Signal visibility.** `hostterm.Guard` exposes the signal it caught. The
   server logs it and records it to `exits.log` inside the stop function. The
   client logs it to its log and `exits.log`.
5. **Client exit notice.** When the connection ends and reconnecting fails,
   the client prints a one-line notice once the terminal is restored, and still
   exits 0:
   `[wideboi: the session at <socket> ended (<reason>); see <exits.log path>]`.
   Before exiting, an owner waits up to `reapCeiling` for the server's status
   and includes it. That wait is only on this give-up path.
6. **The server's reaped status is recorded wherever it arrives.** The
   spawn reap goroutine logs `ProcessState.String()` to the client log and
   `exits.log`, independent of which client path is running.

## Design decisions

- **One shared `exits.log` per session dir, not per session.**
  - **Why:** survives every cleanup path by construction, and a single `tail`
    shows cross-session patterns (e.g. two sessions dying at once, or a sweep
    removing a live socket).
  - **Rejected:** keeping session logs on "abnormal" exit. That needs deciding
    what is abnormal at exit time, which is the thing we don't know yet.
- **Appends are one `write(2)` of one short line with `O_APPEND`**, opened and
  closed per event. Processes write concurrently; a single short `O_APPEND`
  write keeps lines whole. No locking, no rotation.
- **Ancestry by executable name only (`ps -o comm=`), never full command
  lines.** Command lines here contain secrets (`--websocket-token`). The
  lookup is logging-only and read-only. It walks at most 8 levels and gives up
  quietly on any parse doubt (memory: kill-path parsing mass kill).
- **Peer PID from the hello, kept per transport on the server.** It's already
  on the wire, and the peer is our own binary. `LOCAL_PEERPID` would be
  kernel-verified, but it's platform-specific and buys nothing here.
- **The notice keeps exit 0.** A kill-session legitimately ends attached
  clients through the same path, and `attachcheck.py:748-752` pins exit 0.
  Changing exit codes isn't instrumentation.
- **The signal record is written inside the guard's stop function.** Code
  after `Run` never runs on the signal path (`research.md`, "Signals").
- **Reason plumbing stays in `internal/server` (`closeWith(reason, attrs)`),
  and `cmd/wideboi` writes the `exits.log` line** from `CloseReason()`. The
  server package doesn't learn file paths. `internal/logger` gains an
  `AppendExit` helper, so both sides share one format.

## Patterns to follow

- Log path helper beside the socket: `logger.Path` (`internal/logger/logger.go`).
- Notice after terminal restore: `printDetachNotice` and its call site
  (`cmd/wideboi/main.go:915-919, 1006`), checked by `attachcheck.py:236-246`.
- Token redaction already happens in log attrs. Never pass a token or a
  command line to `AppendExit`.
- Waits are ceilings (`CLAUDE.md`): reuse `reapCeiling`, add no sleeps.
- Tests: prove each new record fails before the change. Unit-test
  `AppendExit` and the ancestry parser. Extend existing e2e cases (kill-session,
  signal, last pane exit in `cmd/wideboi/*_test.go`, `attachcheck.py`) to
  assert the `exits.log` line, rather than adding new harnesses.

## What we're NOT doing

- Not fixing the root cause. It was unknown when this was written; the instrumentation then found it (a test dispatching `quit` to the default socket — see notes.md), and its fix is a separate PR.
- Not fixing the doubled server log lines (tee to stderr, which is the log).
  Recorded for later.
- Not changing any exit code, including the `-1` signal/timeout collision in
  `serverExitCode`. The recorded status string disambiguates it for diagnosis.
- Not changing auto-cleanup or sweep behaviour (e.g. the 100ms dial), only
  recording what it removes.
- Not capturing the sender of a signal (not available from Go on macOS).
- No rotation or size cap on `exits.log`.
- Not instrumenting the desktop app's exit paths.

## Open questions

- *Should `wideboi ls`/`status` surface `exits.log`?* Default: no. The notice
  prints its path, and Les reads it directly.
- *Should pane-exit lines also go to `exits.log`?* Default: only the one that
  ended the session (inside the server-exit record). Every pane exit goes to
  the server log only, to keep `exits.log` about sessions.
