# Research — pane update flow (#85)

Documentarian findings, condensed. Paths relative to repo root.

## Server frame loop
- `Server.Run` (internal/server/server.go:219-251): 1s ticker → `pollDescendants` only; 33ms `frameTicker` →
  `if !broadcastLayoutIfStatusChanged(ctx) { broadcastPaneUpdates(ctx) }` (240-246). `broadcastLayout` always ends
  with `broadcastPaneUpdates` (684), so exactly one round of pane updates per tick.
- `broadcastPaneUpdates` (687-701): under `s.mu`, `p.UpdateMessage()` for every pane + copy of `s.transports`;
  then pane-major loop `tp.SendServer(ctx, update)`, return value ignored. No change detection.
- `Pane.UpdateMessage` (internal/server/pane.go:281-327): full-grid render via `grid.Draw` into a fresh
  `uv.ScreenBuffer`, copy every cell into `[]protocol.LineData`, plus cursor pos/visible and mouse tracking.
- `vtGrid.Draw` (internal/server/term/grid.go:538-563) always reads cells via `ScrollbackCellAt`/`CellAt` under
  `writeResizeMu`; the scroll offset shows up here. (Comment at 534-537 describes a fast path the body lacks.)
- `MsgPaneUpdate` (internal/protocol/messages.go:45-59): always a full snapshot, no sequence/generation field.

## Transports
- No per-client struct; `Server.transports []transport.Transport` (server.go:31). Added in `NewServer` (112-114)
  and `ListenSocket` on accept (129-141, *before* MsgAttach). Removed in `dropClient`/`removeTransportLocked`
  (185-216) and `Close` (846-855).
- `InProcChannel.SendServer` (internal/transport/inproc.go:60-67): non-blocking, **drops** and returns false when
  full. Tests only.
- `ServerSocketConn.SendServer` (internal/transport/socket.go:233-247): **blocks** until sent, closed, or ctx done;
  false only when closed/cancelled. 256-slot queue (server.go:129).

## Pane state that changes
- PTY output: reader goroutine (pane.go:102-120) → `vtGrid.Write` (grid.go:335-344) under `writeResizeMu`.
  Cursor position, cursor visibility (callback, grid.go:226), mouse modes (236-237, 395-416), title (232),
  OSC 133/9;4 status all change *inside* Write's parse.
- Resize: `Pane.Resize` (pane.go:227-240) → `vtGrid.Resize` (grid.go:447-505) under `writeResizeMu`, incl. reflow.
- Scroll offset: `vtGrid.SetScrollOffset` (grid.go:523-532), atomic, clamped. Set from `MsgScroll`
  (server.go:341-344), which does not trigger a broadcast — the new view reaches clients via the next tick.
- Status: `Status()` (grid.go:354-366) decays Working→Idle by time with no write. Status is not in MsgPaneUpdate;
  it rides the snapshot via `broadcastLayoutIfStatusChanged`.
- **No existing generation/dirty tracking** on `Pane` (pane.go:27-62) or `vtGrid` (grid.go:151-199).
  Nearest: `lastWriteTime` (grid.go:158), server `lastStatuses`/`lastTitles` (server.go:36-40).
- `Touched()`: never called by wideboi; wideboi doesn't call `em.Draw` either. LESSONS: resize clears Touched.

## broadcastLayout / attach
- `broadcastLayoutIfStatusChanged` (server.go:621-640) compares glyph/title maps vs `lastStatuses`/`lastTitles`.
- `broadcastLayout` (642-685): snapshot to every transport; records lasts only if *all* sends succeeded
  (all-or-nothing, no per-transport record); then `broadcastPaneUpdates`.
- Callers: `handleClientMsg` on `needBroadcast` (349-351: Attach, Resize, every Verb, FocusPane), `SpawnPane`
  (365), `onPaneExit` (414), the frame tick.
- MsgAttach (258-268) → `broadcastLayout` to **all** transports. Nothing targeted at the new client.

## Client
- `HandleServerMsg` MsgLayoutSnapshot (internal/client/client.go ~160-205): creates/regrows mirrors from
  `Placements` (a regrown mirror is a fresh blank `compose.Surface`) and **prunes mirrors not placed** (195-200).
- MsgPaneUpdate (207-245): replaces mirror if size differs, writes every cell, sets cursorInfos, mouseTracking.
- `Client.Draw` (379-404) recomposes every call; returns false when frame equals `lastRenderedScreen`.
- Nothing on the client depends on per-frame updates: motion is client-driven, no cursor blink, scroll state is
  server-side, liveness is channel close.

## Tests / harnesses
- status_test.go: fake `statusGrid` (22-62), InProc transports, delivery-retry tests (136, 264). No test counts
  MsgPaneUpdate. server_test.go `recvLayoutSnapshot` (22-36). cmd/wideboi/main_test.go:17-105 waits for a
  MsgPaneUpdate after reattach over a real socket.
- smoke.py / attachcheck.py observe host-pty bytes, not wire messages.
- Logging: "received MsgPaneUpdate" is client-side trace (client.go:208-210). Server logs nothing per frame.

## Other senders
- `broadcastPaneUpdates` is the only MsgPaneUpdate sender; `UpdateMessage` the only producer.
- `MsgPaneClosed` declared + registered, never sent.
