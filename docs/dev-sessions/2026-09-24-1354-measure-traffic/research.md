# Research: pane traffic path (HEAD 6e37942)

Documentarian findings; descriptive only.

## Server: PTY → message

- `Pane.Start` pty reader (4 KiB) → `grid.Write` (`internal/server/pane.go:103-123`); `vtGrid.Write` bumps generation (`internal/server/term/grid.go:330-350`). Also bumped by Resize (:468), SetScrollOffset (:537-548), mouse/cursor paths.
- `Server.Run` 33 ms `frameTicker` (`internal/server/server.go:306-323`) → `broadcastLayoutIfStatusChanged` (:733-754) or `broadcastPaneUpdates(ctx,false)`. `broadcastLayout` (:756-825) ends with `broadcastPaneUpdates(ctx,true)` (force = full to all).
- Resync: client `MsgPaneResync` → drop `paneGens`/`paneFrames` for that pane under `paneSendMu`, rebroadcast (`server.go:333-334`, `:440-451`). No distinct server-side resync message; a resync is a full `MsgPaneUpdate`.
- Per-client state: `paneGens` (:57), `paneFrames` (:60, last full accepted frame = patch baseline), `paneSendMu` (:73). Cleared in `removeTransportLocked` (:279-295).
- `broadcastPaneUpdates` (`server.go:833-988`): target selection under `s.mu` (:850-867); render once per changed pane via `Pane.UpdateMessage` (:872-881, `pane.go:286-338`, full `uv.ScreenBuffer` draw + copy every cell); per target default full, `BuildPanePatch` if baseline (:903-911); `tp.SendServer`; bookkeeping/invalidation on reject or gen race (:934-963); prune exited (:965-978).
- `BuildPanePatch` (`internal/protocol/pane_patch.go:8-72`): row patch if < half rows changed; else shift search (`ShiftRows` + edge band), ambiguous → full. `ApplyPanePatch` (:77-130). `protocol.Version = 2` (`internal/protocol/version.go:18`).

## Encoding and transports

- `SendServer` enqueues Go struct (chan 256); encoding happens in the write pump via `protocol.MarshalServer` (`internal/protocol/codec.go:79-120`).
- Unix socket: `ServerSocketConn.writeLoop` (`internal/transport/socket.go:212-238`) → `writeFrame` (4-byte BE length + payload, one write; `internal/transport/frame.go:18-53`). Payload length available at `socket.go:224`. Full queue: blocks (`socket.go:265-279`).
- Owner socketpair (fd 3) uses same framing (`cmd/wideboi/spawn.go:32-51`).
- WebSocket: `WebSocketServerConn.writeLoop` (`internal/transport/websocket.go:51-96`) → `conn.WriteMessage(Binary, payload)` (:87); one envelope per WS message; no permessage-deflate anywhere (`EnableCompression` never set; upgrader `server.go:1079-1126`). Full queue: closes conn (`websocket.go:147-167`).
- InProc (tests): Go values, no encoding (`internal/transport/inproc.go`).
- No byte counters or wrapped conns exist. Client read-side: `socket.go:354-373`, `web/src/client.ts:65`.

## Existing measurement

- `docs/partial-pane-updates.md` — contract, workload table, rerun commands, shift/gzip figures; :65 says #179 still needs live traffic and client painting data.
- `TestPaneTrafficWorkloads` (`internal/server/pane_traffic_test.go:20-165`): real VT, InProc, 30 simulated ticks; typing/scroll/scrollback/160x48×4; marshal + gzip sizes; reconstructs mirrors.
- Benchmarks: `BenchmarkPaneUpdateRender`, `BenchmarkPaneWirePayload` (`internal/server/paneupdate_bench_test.go`), `BenchmarkPanePatchBuildAndApply`, `BenchmarkScrollPatchVsSnapshot` (`internal/protocol/pane_patch_test.go`). No Makefile target or script for any.
- Diagnostics: `internal/logger` (trace level, per-component log files `logger.go:50-72`); client trace log per `MsgPaneUpdate` (`internal/client/client.go:271`); `Pane.dropped` counter (`pane.go:51`). No pprof, expvar, metrics, or stats env/flag.

## Clients

- Terminal: `HandleServerMsg` (`internal/client/client.go:135`); patch → `ApplyPanePatch` → `applyPaneUpdateLocked` (:269-307) rewrites every cell of the mirror; failure → `MsgPaneResync` (:252-265). 16 ms draw ticker in `runClient` (`cmd/wideboi/main.go:743`, :827-835); `Client.Draw` (:440-465) composes the whole frame, skips present if unchanged; `present` (`cmd/wideboi/present.go:25-31`).
- Web: `wideboi-app.ts:272-298` dispatch; `GridRenderer.handlePaneUpdate/handlePanePatch` (`web/src/renderer.ts:83-132`); `invalidate()` → one rAF (:60-66); `draw()` repaints all placements, every cell (:293-~420). No dirty tracking.

## Config and harness

- Flags `cmd/wideboi/main.go:63-127`; env vars in `config.Load` (`internal/config/config.go:262-280`): WIDEBOI_LAYOUT, _WEBSOCKET, _WEBSOCKET_TOKEN, _PREFIX, _SESSION/_SOCK, _SHELL, _LOG_LEVEL. No diagnostics knobs besides log level.
- `scripts/ptylib.py`: `spawn_in_pty` pins env, strips WIDEBOI_* then overlays caller env (:140-236); `settle_output` (:84-116). `scripts/attachcheck.py` runs real `wideboi server` + attach clients with `WIDEBOI_SOCK`. No script drives WebSocket or browser; web deps are lit + protobuf + vitest only (no headless browser).
- Local TUIs available: vim, nvim, less, top, htop.
