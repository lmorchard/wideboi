# Research — issue 227

Documentarian findings (paths relative to repo root). Condensed from subagent report.

## Pane exit today
- `ptyx.Spawn` (internal/server/ptyx/pane.go:41-96): reaper does `_ = cmd.Wait(); close(p.done)` at :91-94 — **exit status discarded**. Exposes only `Done()` (:29), `PID()` (:32).
- Exit *detection* is PTY EOF: `Pane.Start(onExit)` reader goroutine (internal/server/pane.go:117-193) calls `onExit()` in its defer (:127-145). `done` is only read by `Hangup`→`waitForExit` (ptyx/hangup.go:18-32).
- `Server.onPaneExit` (internal/server/server.go:842-877): strip.KillPane, delete from s.panes/lastCWD/lastUserVars, resize, dashboard update; after unlock broadcastLayout, `p.Close()`, `s.Close()` if no non-dashboard panes remain (:861-876). Wired at :834-837 and :929-931.
- `removePaneLocked` (:879-903) used by MsgClosePaneRequest (:470-477) and VerbKillPane (:585-589); last-pane close → `s.Close()` after 50ms (:743-749).
- `protocol.MsgPaneClosed{PaneID, ExitCode}` exists (messages.go:275-279, proto :133-136, codec.go:160-161/:264) — **never sent by the server**. Web handles 'paneClosed' (web/src/wideboi-app.ts:625); Go client does not.
- No concept of retaining a dead pane. `Pane.dead` means "pump panicked" only.
- `auto_cleanup` (#119, config.go:41-44, main.go:440-452) is about on-disk session artifacts, unrelated to panes.

## Server spawn / ownership
- `run` (cmd/wideboi/main.go:531-552): dial socket; else `spawnServer` + attach as owner. errSessionTaken (exit 3) → `dialWithin(socket, 5s)` (:560-577).
- `spawnServer` (cmd/wideboi/spawn.go:33-89): socketpair, exec `server --owner-fd 3 <flags>`, Setsid, stderr→server log; reaper goroutine sends exit code on `exited`.
- Readiness = handshake on the socketpair; server binds listener before handshaking (main.go:295 before :325). No polling.
- Shutdown triggers: owner EOF without detach (server.go:289-295, :320-347), MsgShutdown, last terminal pane gone, signals, ctx cancel. **No zero-clients shutdown.** Ownerless server with no panes stays up.
- No panes at server start; created lazily on MsgAttach when `len(s.panes)==0` (:508-530). MsgSplitRequest spawns without attach; rows default `max(s.rows-2,20)`, cols = last width preset (:807-815).

## Control subcommands
- `rpcQuery` (cmd/wideboi/control.go:21-56): dial, handshake, send, skip messages until one type-asserts to Resp, 5s timeout. Never attaches.
- runSplit joins args with " " → `[shell, -c, cmd]` (server.go:817-820). runSend uses only `rest[1]` (control.go:183).
- Server handlers: server.go:436-477. Split does not `resizePanesLocked` nor queue pendingPaneCreated.

## Adding a message type (full list, per d38a438)
messages.go; wirepb/wideboi.proto (ServerMessage oneof last=12, ClientMessage oneof last=15); `make proto` (buf generate → wideboi.pb.go + web/src/gen/.../wideboi_pb.ts); codec.go Marshal/Unmarshal Client/Server; version.go bump (now 8) + history line; web/src/client.ts:37 `wideboi.v8`; version strings in web/src/client.test.ts, lifecycle.test.ts, web/tests/{cards,horizontal-viewport,lifecycle,vertical-viewport}.spec.js; wireTypes in internal/protocol/wire_test.go and TestEveryMessageTypeRoundtrips in internal/transport/wire_test.go; handlers in server.go / client.go / wideboi-app.ts. `proto-check` not in `make check`.

## status --json
- cmd/wideboi/status.go:17-22,86-96. `ColumnData` (messages.go:61-65) has no json tags → `PaneID/Width/Height`. `PaneStatus` int with only Glyph() → 0 idle,1 working,2 needs_input,3 done,4 failed. `MsgPaneMetadata` has snake_case tags; `user_vars` null when empty.
- `statusName` cmd/wideboi/status.go:221-234 (table only; "needs input" with a space).
- Tests: cmd/wideboi/status_test.go:106-179 asserts keys, compares status as int, asserts output unmarshals into MsgLayoutSnapshot.
- Consumers: scripts/attachcheck.py:971-974 (`columns[0]["Height"]`), scripts/traffic.py:206-207,:258,:698,:707 (`Width`/`Height`).

## Pane status computation
- vtGrid (internal/server/term/grid.go): initial Idle; fallback Write→Working, Idle after 3s quiet (:451-458,:503-515); OSC 133 A/B→NeedsInput, C→Working, D→Done/Failed by exit field (:260-306); OSC 9;4 (:308-348). Authoritative sequences latch off the fallback.
- **Process exit never sets Done/Failed** — pane is just removed.
