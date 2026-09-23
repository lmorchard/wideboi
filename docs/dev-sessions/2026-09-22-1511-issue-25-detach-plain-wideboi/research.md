# Research: how lifecycle, detach and teardown work today

Documentarian pass, 2026-09-22. Describes what exists; no proposals.

## Server lifecycle

- `Server.Run` (internal/server/server.go:157-189) returns only on `ctx.Done()`
  (→ `s.Close()`, 174) or `stopCh` closed (176; closed only by `Close`, 733).
  Nothing checks pane count.
- `Server.Close` (729-774), once: closes `stopCh`; under `s.mu` snapshots and
  empties panes, runs `pollDescendantsLocked` (ps walk, 676-721) and copies
  escapees; unlocks; closes all panes concurrently (748-763); then `kill -9`
  each escapee (765-767). Does **not** close transports or the listener.
- `Pane.Close` (pane.go:345-370) → `ptyx.Kill(grace)` (ptyx/reap.go:135-170):
  descendant snapshot, SIGTERM deepest-first + pgid + root, wait grace, close
  master, SIGKILL snapshot, SIGKILL root/pgid if alive. Root-already-exited →
  master close only (136-141; issue #39). `CloseGrace=2s`, `CloseResidual =
  KillResidual = 1.5s`.
- Pane self-exit: `onPaneExit` (320-335) removes, broadcasts layout, closes.
  Last pane gone → server keeps running. `MsgPaneClosed` defined, never sent.
- A later `MsgAttach` with zero panes spawns two fresh panes (200-204).

## `wideboi server` mode (cmd/wideboi/main.go:219-246)

- `NewSocketListener` + `defer sl.Close()`, `NewServer(nil, …)`,
  `ListenSocket`, `Run(ctx)`; `cancel` only deferred.
- **No signal handling, no guard.** SIGTERM takes Go's default disposition, so
  `srv.Close()` never runs on that path (attachcheck stops the server with
  SIGTERM, attachcheck.py:124). Pane shells die because the master closes with
  the process; a nohup'd escapee would not be killed.

## ListenSocket / clients (server.go:97-139)

- Accept loop; each conn → `NewServerSocketConn`, `RunPumps`, appended to
  `s.transports`, `go handleClientConnLoop`. Unlimited concurrent clients;
  every broadcast goes to every transport.
- Disconnect: readLoop EOF → `close(sc.ClientSend)` (socket.go:201-207) →
  `removeTransportLocked` + Close (server.go:122-139). Panes untouched.
- Each `MsgAttach`/`MsgResize` overwrites shared `s.cols/s.rows` (196-213).

## Wire protocol (internal/protocol/messages.go)

- C→S: `MsgAttach{Cols,Rows}`, `MsgVerb{Verb}`, `MsgInput`, `MsgResize`,
  `MsgScroll`. S→C: `MsgPaneUpdate`, `MsgLayoutSnapshot`, `MsgPaneClosed`.
- Verbs (12-23) appended-only because they cross the wire. No detach,
  shutdown, kill-session or quit message/verb; `handleClientMsg` handles
  Attach/Resize/Verb/Input/Scroll only (server.go:195-270).
- gob over net.Conn, concrete types registered in `init` (socket.go:69-81).
  `NewSocketListener` dial-probes ("already listening") and removes a stale
  socket path (101-128); `Close` removes the path (141-151).
- Clean-close classification (`isCleanClose`, socket.go:55-67: EOF,
  UnexpectedEOF, ErrClosed, EPIPE, ECONNRESET, Canceled) → `Err()` nil.
  runAttach uses this to tell "server closed/detached" (exit 0) from protocol
  failure (main.go:301-314).
- InProcChannel `SendServer` is non-blocking and drops on full (inproc.go:60-67).

## Signals and terminal restore

- `hostterm.Guard` (guard.go): `Stop` once; `Arm` samples `signal.Ignored`,
  Notify, on first signal runs Stop, resets, then re-raises (or
  `os.Exit(128+sig)` if inherited-ignored) (57-92).
- Only `run` arms it (main.go:398-417: `srv.Close()` then alt-screen exit +
  `t.Stop()`; SIGINT/TERM/HUP/QUIT). `runAttach` arms nothing.
- `run` sleeps `CloseResidual + signalExitMargin` after a stopped close
  (440-446).

## Detach verb

- keys.go:141-142 `d` → `ActionDetach`, `NeedsDetach: true`; `BarItemsFor`
  drops NeedsDetach when not detachable (196-213).
- router.go:111-113 skips NeedsDetach bindings when `!detachable`;
  `ActionQuit`→`routeQuit`, `ActionDetach`→`routeDetach` (142-149).
- runAttach: `detachable: true`, `SetDetachable(true)` (main.go:290-291);
  quit and detach both just `return nil` — nothing sent, socket close tells
  the server (328-333). **Quit in attach mode = detach today.**
- run: not detachable; quit returns → `guard.Stop` → `srv.Close()`.
- Client: `SetDetachable` (client.go:807-821), `controlHelp` (744-764), help
  overlay (388, help.go:18-58).

## In-process vs attached rendering

- `Client.Draw(scr, drawPane, cursorInfo)` (client.go:359-384).
  `composeFrameLocked` (461-545): with `drawPane` non-nil, reads straight from
  the server-side emulator (`srv.DrawPane`, server.go:646-653); otherwise blits
  `mirrors[id].Surface`, filled from `MsgPaneUpdate` (client.go:193-217).
  Cursor likewise (421-445).
- run passes `srv.DrawPane, srv.CursorInfo` (main.go:484); runAttach `nil,nil`
  (348). Layout arrives via snapshot in both.

## Test harness

- **ptycheck.py** (`make verify-exit`): plain `./bin/wideboi` with a
  never-created `WIDEBOI_SOCK` → in-process path. Plants `nohup sleep 987654 &`,
  signals, asserts died-by-signal, `ESC[?1049l` seen, tracked shells + escapee
  gone within 2s, no stray wideboi. `find_stray_wideboi` (98-138): argv0 ==
  binary, reported if ppid is own pid, 1, or dead.
- **smoke.py**: plain binary, never-socket (96, 120-123). `quit_and_reap`
  SIGTERM + direct-child leak check (216-224, 567-579).
  `case_control_mode_names_every_entry_at_80_columns` asserts "d detach"
  **absent** in-process (596-613). `strays()` over SPAWNED (885-895).
- **golden.py**: plain binary 100x30, never-socket, SIGTERM after 2s, compares
  escape/word summary to `testdata/golden/startup.txt` (61-67). No leak checks.
- **attachcheck.py**: mkdtemp socket; `Server` = `Popen([BIN,"server"])` not in
  a pty (90-110), stop = SIGTERM, `leaked` from `still_alive` (121-132).
  `Client` = `attach` in pty; detach = `\x02`, 0.4s, `d` (182-191). Cases:
  detach leaves session running (268-297), attached bar offers detach
  (247-265), server reaps panes on signal (333-345). No parentage stray scan.
- Go: main_test.go:16 two sequential socket clients; router_test.go:78 control
  table both detachable states; transport_close_test.go:33/73; help tests
  (help_test.go:56,142; help_overlay_test.go:27); keys_test.go:180-191.

## Grep

- "kill-session": none. "owner": only incidental (pane.go:26,
  attachcheck.py:66).
