# Research: process teardown as of 8cb5c2f

Documentarian findings. All paths are relative to the repo root.

## Process-table readers (all shell out to `ps`)
- `ptyx.Descendants(pid)`, `internal/server/ptyx/reap.go:37-78`: `ps -axo pid=,ppid=`, walked deepest first. Only caller: `Kill` (:191).
- `ptyx.TTYMates(pid)`, `reap.go:88-123`: `ps -axo pid=,tty=`. Only caller: `Kill` (:192), merged in via `appendNew` (:225-236).
- `server.readProcTable`/`parseProcTable`, `internal/server/server.go:838-872`: `ps -axo pid=,ppid=,lstart=` under `LC_ALL=C`, requiring exactly 7 fields. Callers: `pollDescendantsLocked` (:889), `Close` (:996), and the test helper `startOf`.
- Test-only: `ps` in `reap_test.go:200,215`, and `pkill` at `:73`.

## Background poll
- `Run` has a 1s ticker (`server.go:244`) that calls `pollDescendants` (:263-264), which takes `s.mu` and calls `pollDescendantsLocked` (:878-921).
- That function collects pane root PIDs, prunes stale entries by start time, and adds every transitive child to `s.escapees map[int]string` (field at `server.go:32`, initialised at :124, with test fixtures in `lifecycle_test.go:25`, `status_test.go:80`, `transport_close_test.go:37,77`).

## Signals sent while reaping panes
- `ptyx.(*Pane).Kill(grace)`, `reap.go:183-220`. If `done` is already closed it closes the master and sends nothing. Otherwise:
  1. Take the snapshot (Descendants plus TTYMates).
  2. SIGTERM the snapshot, then the process group and root (`signalRoot`, :250-255).
  3. `waitForExit(grace)`.
  4. Close the master.
  5. SIGKILL the snapshot, unconditionally.
  6. If the root is still alive, SIGKILL the group and root, then wait `killWait`.
  7. Return an error if anything is still alive after `aliveWait` (`anyAlive`/`processAlive`, :271-300).
  - Only non-test caller: `server.(*Pane).Close`, `pane.go:382`.
- `Server.Close` escapee loop, `server.go:990-1002`: after every pane's Close finishes, it runs `kill -9` on each escapee whose start time still matches.
- `Spawn` sets `Setsid` and `Setctty` (`ptyx/pane.go:50-53`) and `PGID = pid` (:88). A single goroutine calls `Wait` and closes `done` (:95-98).

## Exported timing constants
- `KillResidual = killWait (1s) + aliveWait (500ms)`, `reap.go:19-30`.
- It is aliased as `server.CloseResidual` (`pane.go:22`), whose only user is `cmd/wideboi/hangup.go:14`: `shutdownCeiling = CloseGrace + CloseResidual + signalExitMargin` (4s). That ceiling is used at `main.go:326,405,514` and `main_test.go:143`.
- `CloseGrace = 2s` (`pane.go:21`) is read through `graceOrDefault` (:72-77). `SetCloseGrace` is test-only (`export_test.go:11-14`).
- Stale comment references: `reap.go:15` names `client.CloseResidual`, which doesn't exist. `reap.go:158` and `LESSONS.md:77` name `closePanes`, which doesn't exist.

## Pane lifecycle
- **Shell exits:** reader EOF calls `onPaneExit` (`server.go:426-441`), which calls `Pane.Close`, which calls `Kill`. Kill short-circuits because the root is already done.
- **Verb:** `removePaneLocked` (`server.go:443-451`) calls `go p.Close()`.
- **`Server.Close`** (`server.go:942-1025`), in order:
  1. Close `stopCh`.
  2. Close the listener.
  3. Snapshot and clear the panes, and copy the escapees.
  4. Close all panes in parallel and wait for them.
  5. Run the escapee kill -9 loop.
  6. Close the transports. For the client, the hang-up is the acknowledgement.
- **Owner signalled:** the guard sends `MsgShutdown` via `hangUp(..., shutdownCeiling)` (`main.go:403-406`), which calls `Close`.
- **Owner SIGKILLed:** socket EOF calls `dropClient`, which calls `Close` (`server.go:169-175,200-221`).

## Tests and scripts that assert on reaping
- **`reap_test.go`**, planted escapees:
  - `TestKillReapsEscapedGrandchild` (:14-41)
  - `TestKillReapsReparentedEscapee` (:48-76)
  - `TestKillReapsSIGTERMIgnoringEscapee` (:85-111)
  - `TestKillEscalatesEvenWhenRootExitsWithinGrace` (:128-149)
  - `TestKillIsIdempotent` (:151-162)
  - `pane_test.go` uses `Kill` as cleanup and asserts its error (:30-35).
- **`escapee_test.go`**: the #89 identity tests, the prune test, and the parse test.
- **`ptycheck.py`** (`make verify-exit`, `Makefile:100-145`): plants `nohup sleep 987654` (:81-100), which can be turned off with `--no-escapee` (:323). It asserts that every tracked pid (the shells, the escapee and the server) is gone within 2s (:276-289), that the socket was removed, and that no wideboi copy is stray (:103-143).
- **`attachcheck.py`**:
  - `plant_escapee` (:571-596)
  - `case_sigkilled_owner_takes_the_session_with_it` (:493-516), which plants an escapee
  - `case_server_reaps_its_panes_on_signal` (:599-621), which plants an escapee
  - `case_server_reaps_a_reparented_escapee` (:624-657)
  - These cases assert on pane shells without planting: quit (:347-373), kill-session (:665-699), detach and survive (:443-490).
- **`smoke.py`**: `case_quit_restores_and_reaps` (:730-742) asserts the descendants are gone and plants no escapee. `strays()` is at :1075-1083.

## Where the guarantee is written down
- `README.md:25-30`: "the whole session is torn down with it, so a forgotten `wideboi` never leaves agents running".
- `docs/LESSONS.md:72-135`, "Teardown is the load-bearing guarantee": the parked gaps, the two shutdown routes, the #83/#87 reparenting lesson, the #88 tty mechanism, and the #39 exact-parse lesson.
- Specs from earlier sessions: v1 `spec.md:357-390`, #25 `spec.md:3-5,153`, plan-2 `spec.md:174`. These are history and stay as they are.
- Code comments: `reap.go:32-36,80-87,125-182,202-204`; `server.go:90-93,874-877,963-966,990-1007`; `main.go:285-288,355-357,396-402`; `protocol/messages.go:163-165`; `ptycheck.py:25-46`; `Makefile:110-113`.

## Signals unrelated to reaping (keep as they are)
- `hostterm/guard.go:57-92`: the re-raise guard, armed at `main.go:299,418`.
- `spawn.go:49`: `Setsid` for the spawned server.
- SIGWINCH is sent only as a side effect of TIOCSWINSZ. Nothing sends SIGCONT.
