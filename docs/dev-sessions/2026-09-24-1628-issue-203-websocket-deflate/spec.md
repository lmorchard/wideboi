# Enable permessage-deflate for WebSocket clients Spec

**Goal:** Reduce WebSocket wire byte consumption for browser and WebSocket clients by negotiating RFC 7692 `permessage-deflate` on WebSocket upgrades, drastically cutting full snapshot bandwidth without changing local Unix socket transport.

**Source:** Issue #203 (follow-up to #179)

## Current state

- Today `Server.ListenWebSocket` in `internal/server/server.go:1563-1570` constructs a `websocket.Upgrader` without `EnableCompression`. Gorilla's default is `false`, so neither the server nor browser negotiate `permessage-deflate`.
- Full snapshots (13.5 KB at 80×24, 53.9 KB at 160×48) are sent uncompressed over the WebSocket connection. Live measurements (#179 in `docs/partial-pane-updates.md`) found full snapshots are the dominant remaining byte cost across typing, scrolling, and paging scenarios.
- `scripts/wssink` estimated permessage-deflate without context takeover at level 1 (BestSpeed) would save 84–98% of payload bytes on full snapshots for ~13–19 µs per message.
- `CountingResponseWriter` in `internal/transport/stats.go:103-131` already counts bytes at the hijacked `net.Conn`, so wire counts reflect actual wire bytes written by the transport.

## Desired end state

1. `Server.ListenWebSocket` sets `EnableCompression: true` on the `websocket.Upgrader`.
2. Clients that request `permessage-deflate` (including web browsers and `scripts/wssink`) negotiate permessage-deflate and receive compressed frames.
3. `scripts/wssink` enables compression on its dialer so `make traffic` exercises live compression.
4. Server wire bytes reported in `make traffic` for WebSocket clients drop by roughly the estimated amount (80–98% on scenarios with full snapshots).
5. The browser client continues to work seamlessly and all web tests pass.
6. Server CPU / timing and wire measurements are recorded in `docs/partial-pane-updates.md`.

## Design decisions

- **Decision:** Set `EnableCompression: true` on `websocket.Upgrader` in `Server.ListenWebSocket`.
  - **Why:** Browsers natively offer and negotiate `permessage-deflate; client_max_window_bits`. gorilla/websocket v1.5.3 automatically negotiates no-context-takeover at level 1 (`flate.BestSpeed`).
  - **Rejected:** Custom deflate layer on top of protobuf or context takeover library. gorilla's built-in no-context-takeover requires zero new dependencies, standard browser compatibility, and minimal CPU cost (13–19 µs/msg).

- **Decision:** Leave Unix sockets uncompressed.
  - **Why:** Unix domain sockets are local in-memory IPC. Compressing local IPC adds CPU cost with no bandwidth benefit.

- **Decision:** Keep default compression level 1 (`flate.BestSpeed`).
  - **Why:** Level 1 achieves 84–98% reduction on snapshots at minimal CPU time (~15 µs). Higher levels add substantial CPU time for diminishing compression gains.

- **Decision:** Enable `EnableCompression: true` in `scripts/wssink/main.go` dialer.
  - **Why:** `wssink` uses gorilla/websocket client, which defaults to no compression. Enabling it allows `make traffic` to measure actual compressed wire traffic.

## Patterns to follow

- `internal/server/server.go:1563-1570`: Upgrader initialization pattern.
- `internal/transport/websocket_test.go:46-75`: WebSocket round-trip testing pattern.
- `internal/server/websocket_auth_test.go:17-48`: Server WebSocket integration test pattern.

## What we're NOT doing

- Context takeover (RFC 7692 sliding window across messages). Gorilla does not support context takeover, and the marginal gain (1–8%) is not worth replacing the transport library.
- Compressing Unix domain socket messages.
- Modifying protobuf schemas or application message payloads.

## Open questions

None.
