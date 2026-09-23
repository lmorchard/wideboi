# Detach from plain wideboi Spec

**Goal:** Make `C-b d` work from a plain `wideboi` session, the commonest way
to start one, without breaking the guarantee that nothing wideboi spawned
outlives it unless you explicitly detached.

**Source:** https://github.com/lmorchard/wideboi/issues/25, plus decisions
made with Les on 2026-09-22.

## Current state

See `research.md` for the file:line detail. The load-bearing facts:

- Plain `wideboi` either attaches to a server already on the socket, or runs
  server and client in one process with no socket (`cmd/wideboi/main.go`
  `run`). In-process, detach is hidden and swallowed (router.go:111-113,
  keys.go:196-213), and quit or any armed signal runs `srv.Close()` before
  the terminal is restored and the signal re-raised (main.go:398-417).
- The in-process client reads pane content straight from the server
  (`srv.DrawPane`/`srv.CursorInfo`, main.go:484). An attached client reads it
  from `MsgPaneUpdate` mirrors (client.go:193-217, 461-545).
- `wideboi server` arms **no signal handling** (main.go:219-246). SIGTERM
  kills it with Go's default behaviour, so `Server.Close` never runs. The
  shells die because the PTY masters close with the process; a `nohup`'d
  escapee does not.
- There is no detach, shutdown or kill message on the wire (messages.go). An
  attached client's quit *and* detach both just close the socket
  (main.go:328-333), so `q` detaches when attached, despite being labelled
  "quit wideboi and close every pane" (keys.go:155-156).
- `verify-exit`, `smoke` and `golden` all run the in-process path, using a
  socket path that is never created. Only `attach-check` exercises the wire.

## Desired end state

- **Plain `wideboi`, no server running:** spawns a background `wideboi
  server` and attaches to it as that session's **owner**. It is one binary,
  but under the hood the session is always a client talking to a server.
- **Plain `wideboi`, server already running:** attaches as a non-owner (as it
  does today).
- **`C-b d`** is offered and works in every client. It sends `MsgDetach`, then
  exits 0. If the owner detaches, the session becomes ownerless **for good**,
  and a later attach never takes ownership back.
- **`C-b q`** ends the session from any client, owner or not. It sends
  `MsgShutdown`; the server reaps everything, closes every connection, removes
  the socket and exits.
- **A signal (INT/TERM/HUP/QUIT) to an owning client** ends the session. The
  guard sends `MsgShutdown` and waits, up to a ceiling, for the server to reap
  its panes and close the connection. Only then does it restore the terminal
  and re-raise the signal. That keeps the current order, where panes are
  reaped before the client dies.
- **An owning client that dies without a word** (for example SIGKILL) is
  handled as a shutdown: the server sees EOF on the owner connection with no
  `MsgDetach` before it.
- **A non-owner's EOF** counts as a detach (as it does today).
- **A signal to a non-owner client** restores the terminal and exits, which
  detaches it. The session is left running.
- **The server arms a signal guard.** INT/TERM/HUP/QUIT run `srv.Close()`
  (the full reap, escapees included), then it exits. This covers ownerless
  sessions and explicit `wideboi server` runs.
- **`wideboi kill-session`** dials the socket, sends `MsgShutdown`, and waits
  for the server to close the connection. It errors clearly if nothing is
  running.
- **Once the last client has left an ownerless session,** the server keeps
  running until `kill-session`, a `q` from some later client, or a signal. No
  idle timeout.
- **The in-process path is gone.** `run` becomes: dial, and spawn a server if
  the dial fails.

## Design decisions

- **Plain `wideboi` spawns a server (route A).**
  - **Why:** one code path for every mode, and detach works everywhere.
    Every session now goes over the real wire, which closes the gap
    LESSONS.md describes: smoke couldn't see the wire because it only ran
    in-process.
  - **Rejected:** re-exec on detach and hand the live PTY master fds to a
    new server. That is hard, and it fails as orphaned shells, the exact
    leak the teardown guarantee exists to prevent.
- **The owner's connection is an inherited socketpair.** Plain `wideboi`
  creates a `socketpair`, then execs `os.Executable() server` with the same
  resolved config flags, a hidden flag naming the inherited fd, `Setsid`,
  one end in `ExtraFiles`, and stdio on `/dev/null`. The client then talks
  over the other end. The server treats that fd as its owner connection, and
  still listens on the socket for later attaches.
  - **Why:** no race and nothing to forge. The owner never goes through the
    listener. The client doesn't have to wait for the socket, because the
    socketpair works immediately.
  - **Rejected:**
    - *First client to connect owns the session:* another `wideboi` racing
      the spawner could win.
    - *A token passed in the environment:* adds a secret to the env and the
      wire for no gain over fd inheritance.
- **An explicit shutdown message, with EOF as the fallback.**
  - **Why:** the message keeps the "reap before the client dies" order
    that `verify-exit`'s 2s window relies on (pane grace alone is 2s). EOF
    catches a client that dies without running any code.
  - **Rejected:**
    - *EOF only:* the client dies first and reaping happens after.
    - *Message only:* a SIGKILLed owner would leak the session.
- **`q` always ends the session.**
  - **Why:** that's what its label says. Now that detach has its own key,
    quit no longer has to double as detach.
  - **Rejected:** having `q` end the session for the owner but detach for
    other clients. The label would have to change with the mode.
- **Ownership is permanent once given up.**
  - **Why:** after a detach, nobody's terminal is tied to the session.
    Letting whoever reattaches first become the owner would make a stray
    `wideboi attach` a way to kill agents by accident.
  - **Rejected:** having a reattach take ownership back.
- **No idle timeout.**
  - **Why:** silently killing someone's agents after N minutes is a worse
    surprise than a server that is still running. `kill-session` is the
    explicit way out. Listing forgotten sessions belongs to #27.
- **Remove the in-process path.**
  - **Why:** two paths to keep honest, and the in-process one is the one
    the tests can't see into. Frames then come from the 33ms
    `MsgPaneUpdate` tick, which feels the same as `attach` does today.
  - **Rejected:** keeping it behind a flag, which keeps the blind spot.

## Patterns to follow

- **Signal guard:** reuse `hostterm.Guard` as `run` does
  (main.go:398-417). The server gets one armed the same way, with a teardown
  callback that calls `srv.Close()`. The client guard's teardown sends
  `MsgShutdown` and waits for EOF.
- **New wire messages:** add `MsgDetach` and `MsgShutdown` to
  messages.go and register them in socket.go:69-81. Follow LESSONS.md
  ("wire types get concrete mirrors, and a test enforces it"); the wire
  tests are in protocol/wire_test.go and transport/wire_test.go. These are
  new message types, not new `Verb` values.
- **Server transports:** the owner fd wraps the same way as an accepted conn
  (`NewServerSocketConn` + `RunPumps`, server.go:97-115). The server keeps
  a record of which transport is the owner. `handleClientConnLoop` EOF
  handling is at server.go:122-139.
- **Clean close vs failure:** use `ClientSocketConn.Err()` and
  `isCleanClose` (socket.go:33-67) the way runAttach does
  (main.go:301-314).
- **Tests:**
  - Extend `scripts/ptycheck.py` rather than writing a new harness. Its
    stray scan (98-138) already reports a wideboi reparented to pid 1,
    which is exactly how a leaked spawned server would show up. Waiting for
    panes has to follow grandchildren now.
  - Extend `scripts/attachcheck.py` for the detach, reattach and
    kill-session cycle.
  - Every wait is a ceiling, not a sleep. Run any timing change four times.

## What we're NOT doing

- The reconnect handshake (#26) and a session directory or multiple sessions
  (#27). There is still one default socket.
- An idle timeout, or taking ownership back on reattach.
- The fd-handoff route.
- A root that exits before `Kill` (#39), or re-deriving the wedge chain
  (#43).
- Any change to what happens when the last *pane* exits. The server keeps
  running, and the next attach spawns two panes, as today.
- De-duplicating server-side placements (#47), and the exported-surface audit
  (#44). If removing `drawPane`/`cursorInfo` leaves dead server API such as
  `DrawPane` or `CursorInfo`, remove it only if it has no other callers;
  otherwise note it for #44.
- Tuning render latency. If the wire path feels slower than in-process did,
  that becomes its own issue.

## Open questions

- **If the spawned server fails to start** (it can't bind the socket, or it
  exits early): the client sees EOF on the socketpair before any snapshot and
  reports "wideboi server exited during startup; see <log path>", exiting
  non-zero. *Default: that message.*
- **Plain `wideboi` loses a race** (two start at once and one server fails
  `NewSocketListener`'s "already listening"): it reports the failure. It
  doesn't retry as an attach. *Default: no retry. #27 changes this anyway.*
- **The ceiling on the owner's shutdown wait:** `CloseGrace + CloseResidual +
  signalExitMargin`, the same budget `run` sleeps today (main.go:440-446).
  If the ceiling runs out, the client restores the terminal and re-raises
  anyway, and the EOF fallback still reaps. *Default: that budget.*
- **Golden snapshot drift:** content now arrives through mirrors, so
  `testdata/golden/startup.txt` may change. *Default: regenerate with `make
  golden` and review the diff in the PR. Don't hand-edit it to pass.*
