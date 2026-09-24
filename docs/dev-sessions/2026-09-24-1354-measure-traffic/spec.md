# Measure live pane traffic and rendering costs (#179) Spec

**Goal:** Make live pane traffic and rendering cost observable and reproducible,
run representative sessions, and record a decision on which (if any) further
wire or painting optimisation is worth building.

**Source:** https://github.com/lmorchard/wideboi/issues/179 (follow-up to #158 / PR #176; #180 / PR #189 already landed scroll-aware shift patches)

## Current state

See `research.md`. Load-bearing facts:

- Server renders a full frame per changed pane each 33 ms tick and chooses
  full / row patch / shift patch per client (`internal/server/server.go:833-988`,
  `internal/protocol/pane_patch.go:8-72`). A resync is just a full snapshot
  after the baseline is dropped (`server.go:440-451`).
- Encoding happens in the transport write pumps (`internal/transport/socket.go:212-238`,
  `internal/transport/websocket.go:51-96`). Socket framing is a 4-byte length
  prefix; WebSocket is one binary message per envelope, no compression.
- Nothing counts bytes or messages today; no pprof. The only measurements are
  the simulated `TestPaneTrafficWorkloads` and micro-benchmarks
  (`docs/partial-pane-updates.md`).
- Terminal client rewrites every mirror cell per update (`internal/client/client.go:269-307`)
  and composes the whole frame per 16 ms draw; browser repaints every cell per
  rAF (`web/src/renderer.ts:293-420`).
- `wideboi status` sends `MsgStatusRequest` and prints the `MsgLayoutSnapshot`
  it gets back (`cmd/wideboi/status.go`).

## Desired end state

1. **`wideboi status --traffic [--json]`** prints per-session and per-client
   traffic: client number, transport (`socket` / `websocket`), connected
   seconds, updates/s, counts of full snapshots, row patches, shift patches,
   resyncs (full snapshots sent because the client asked), changed rows sent,
   send failures, protobuf payload bytes, transport bytes, and bytes/update.
   With timing enabled, session-level render / patch-build / encode time
   (count, total, max). Output never includes cell contents, titles, input, or
   tokens. Existing `status` output is unchanged.
2. **pprof for live runs:** `WIDEBOI_CPUPROFILE=<path>` and
   `WIDEBOI_MEMPROFILE=<path>` write standard profiles on exit, for both the
   server and the terminal client (distinct paths per component).
3. **Terminal-client benchmark:** apply a row patch / shift patch / full
   snapshot and `Draw`, at 80×24 and 160×48, with `-benchmem`.
4. **Browser `?stats=1`:** the web client records per-patch apply time and
   per-draw time (`performance.now()`), message counts and bytes received, and
   shows a periodic summary (console + small overlay). No content recorded.
5. **`make traffic`** runs `scripts/traffic.py`: a real `wideboi server`,
   terminal clients in ptys, and `go run ./scripts/wssink` as a WebSocket
   client, through four scenarios — typing, busy scrolling, a full-screen TUI,
   and a 160×48 pane with four clients (mixed socket + WebSocket) — then prints
   the `status --traffic --json` results per scenario plus the sink's
   deflate estimate.
6. **Results + decision** appended to `docs/partial-pane-updates.md`: measured
   tables (clearly labelled payload vs transport bytes), CPU/alloc findings,
   the dominant remaining cost, and follow-up issues filed (or an explicit
   "not material" record for sparse cell spans / incremental painting /
   compression).

## Design decisions

- **Counters always on; timing opt-in.** Per-client counters are atomic adds
  in the send path, which is negligible next to rendering a frame. Timing
  (`time.Now` around render, patch build, and encode; wall time only, no
  allocation accounting) is enabled only with `WIDEBOI_TRAFFIC_TIMING=1` at
  server start.
  - **Why:** you can inspect a server that's already running; you pay for timing only when you ask for it.
  - **Rejected:** gating everything behind an env var (can't inspect a live
    server); folding into `MsgLayoutSnapshot` (inflates a message every client
    gets on every layout change).
- **New `MsgTrafficRequest` / `MsgTrafficStats` protocol pair** rather than
  reusing status. Added to `wideboi.proto` like other messages. The requesting
  connection is excluded from the reported clients.
- **Transport bytes are counted at the `net.Conn`.** Socket: wrap the accepted
  conn in a counting writer. WebSocket: wrap the hijacked conn (via the
  `http.ResponseWriter` passed to the upgrader) so gorilla's frame headers are
  included. "Transport bytes" = bytes handed to the kernel for that client,
  excluding TCP/IP and TLS; "payload bytes" = `len(proto.Marshal(...))`.
  Pings, handshakes and layout messages count toward transport bytes;
  pane-update payload is also broken out separately.
- **Counting lives with its owner:** message kind/rows/resync/failure counts in
  `broadcastPaneUpdates` (server); payload and wire bytes in the transport write
  pumps (transport exposes a small stats accessor). Server reads both when
  answering the request. No client/server seam crossing.
- **Compression is estimated, not implemented.** `wssink` feeds each received
  payload through one streaming `flate` writer with `Flush` per message (what
  permessage-deflate with context takeover does) and reports the compressed
  total and the compression CPU time. That answers "would compression pay?"
  without turning it on.
- **Scenario inputs are fixed.** Typing: a fixed string at ~15 chars/s.
  Scrolling: `seq 1 200000`. TUI: `vim -u NONE -N` on a generated file with
  scripted paging (`Ctrl-F` ×N, `j` runs). Large: 160×48, four clients (two
  socket ptys, two wssink), typing. The harness pins env the way `ptylib.py`
  does. Wall-clock rates will vary by machine; the report records the machine
  and duration.
- **Browser measurement is manual** with documented steps (open `?stats=1`
  against the traffic server, run a scenario, copy the summary). No headless
  browser dependency.

## Patterns to follow

- Opt-in env diagnostics: read at startup like `WIDEBOI_LOG_LEVEL`
  (`internal/config/config.go:280`) — but profiling vars are process-level
  and belong in `cmd/wideboi/main.go`, not `config.Config`.
- New protocol message: mirror `MsgStatusRequest` end to end —
  `internal/protocol/messages.go:247`, `codec.go:40,73`,
  `wirepb/wideboi.proto:199,212`, `make proto` / `proto-check`, server handling
  at `server.go:335`, CLI at `cmd/wideboi/status.go`.
- Pty harness: `scripts/attachcheck.py` Server/Client classes (:92-152) on
  `scripts/ptylib.py` (`spawn_in_pty`, `settle_output`, `private_run_dir`).
- Benchmarks: `internal/server/paneupdate_bench_test.go` style, with
  `b.ReportMetric` for bytes.
- Seam: `internal/client` and `internal/server` share only `protocol` /
  `layout`; `make seam-check`.

## What we're NOT doing

- Implementing cell-span patches, transport compression, incremental client
  painting, or server render caching — this issue decides; follow-ups build.
- Headless-browser automation (Playwright etc.).
- Adding `make traffic` to `make check` (it is a measurement run, not a gate).
- Exposing stats over HTTP / expvar / Prometheus.
- Client-reported stats flowing back to the server (terminal client cost
  comes from pprof + benchmark; browser from `?stats=1`).
- Changing the existing `status` output or the #158 simulated workload test.
- Fixing anything the measurements reveal beyond recording it and filing issues.

## Open questions

- **Allocation cost in live stats?** Default: no — allocation cost comes from
  pprof memprofiles and `-benchmem`; live timing reports time only.
- **How long per scenario?** Default: ~10 s each (configurable flag), so a
  full `make traffic` run is under a minute or two.
- **`vim` missing on a machine?** Default: skip the TUI scenario with a clear
  message rather than failing.
