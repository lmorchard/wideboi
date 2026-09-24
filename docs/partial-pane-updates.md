# Partial pane updates (#158)

## Wire contract

`MsgPaneUpdate` is a complete pane snapshot with a generation. A new client, a resize, a layout resend, a missed send, or a resynchronization request receives one. `MsgPanePatch` names the exact `BaseGeneration` it extends, a new generation, complete replacement rows, and cursor and mouse state. Empty `ChangedRows` represents a cursor or mouse-only change. The server compares rendered rows instead of trusting the emulator's `Touched()` flags, which have known resize behavior (`docs/LESSONS.md`). When at least half the rows changed, it looks for a whole-pane `ShiftRows` and a contiguous replacement band at the exposed edge. Every retained row must match exactly, including style and wide-cell data. Ambiguous shifts, resize, and unrelated edits use a full snapshot.

The shift field changes how a patch is applied. Protocol version 2 gates it on
Unix sockets and WebSockets, so an older client cannot silently apply only the
replacement edge rows.

The server keeps the last accepted full state for each client and pane. `paneSendMu` orders successive sends. A client applies a patch only if its pane ID, dimensions, and generation match its baseline; otherwise it requests `MsgPaneResync`. A send rejected by the transport invalidates the server's baseline, so the next send is full. If a transport accepted but lost a message, the client detects the mismatch on a later patch and requests a full snapshot. Layout-triggered sends are full because a layout snapshot may replace a client's mirror. Both the terminal and browser clients reconstruct the same `MsgPaneUpdate` state from patches.

## Measurements

The current socket and WebSocket transports encode protobuf envelopes. The
workload check uses the real VT emulator, `broadcastPaneUpdates`, and
`protocol.MarshalServer` for both the delivered message and a hypothetical
full snapshot of the same frame. It runs 30 deterministic 33 ms frame ticks
(0.99 simulated seconds), after one initial full snapshot per client. Typing
an `x` every other tick models a moderate input rate; scrolling writes one
70-character line per tick; the large-pane case types every tick to four
clients. The table excludes the initial snapshots and counts uncompressed
protobuf payload bytes, without socket framing or WebSocket compression.

| Workload | Updates per client per second | Bytes per update | Delivered bytes | Full-snapshot equivalent | Reduction |
| --- | ---: | ---: | ---: | ---: | ---: |
| Typing, 80×24, 1 client | 15.15 | 580 B | 8,698 B (15 patches) | 202,905 B | 95.7% |
| Scrolling, 80×24, 1 client | 30.30 | 1,158 B | 34,740 B (30 shift patches) | 405,810 B | 91.4% |
| Scrollback navigation, 80×24, 1 client | 30.30 | 588 B | 17,625 B (30 shift patches) | 405,810 B | 95.7% |
| Typing, 160×48, 4 clients | 30.30 | 1,141 B | 136,912 B (120 patches) | 6,470,520 B | 97.9% |

The initial full snapshots cost 13,523 B for 80×24 and 215,668 B in aggregate
for four 160×48 clients. An unchanged pane emits nothing on an idle tick.
These are controlled workload rates, not observations of a live shell or
network throughput; real update rates depend on PTY output and frame
coalescing. The full-snapshot comparison uses the same server-rendered frame,
so it isolates the effect of patch delivery. Full frames are still rendered
once per changed pane, and the server retains a baseline per client; patch
comparison is repeated for each client's baseline.

On an Apple M5 Max (darwin/arm64), uncompressed protobuf encoding with
`-benchtime=100x` took about 85 µs and 188 KB allocated for an 80×24 full
snapshot versus 3.1 µs and 8.1 KB for a one-row patch. At 160×48 it took
327 µs and 751 KB versus 6.6 µs and 15.8 KB. A synthetic all-row 80×24
patch is slightly larger than a full snapshot (13,573 B versus 13,525 B),
which supports the server's full-snapshot fallback when no shift matches. Complete
frame construction took 65 µs and 327 KB allocated at 80×24, 289 µs and
1.28 MB at 160×48, and 608 µs and 2.85 MB at 240×72 in the earlier
benchmark. Comparing rows to build a patch added 3.7 µs and 32 B allocated
at 80×24, or 15.3 µs and 32 B at 160×48. Applying it to a Go protocol
snapshot added 0.14 µs and 640 B at 80×24, or 0.30 µs and 1.3 KB at
160×48. The terminal client currently repaints its mirror from the
reconstructed full snapshot, so its total patch handling cost is higher
than the protocol-only apply benchmark.

Repeat the workload and CPU measurements with:

```sh
go test ./internal/server -run '^TestPaneTrafficWorkloads$' -count=1 -v
go test ./internal/server -run '^$' -bench 'BenchmarkPane(UpdateRender|WirePayload)$' -benchmem
go test ./internal/protocol -run '^$' -bench BenchmarkPanePatchBuildAndApply -benchmem
go test ./internal/protocol -run '^$' -bench BenchmarkScrollPatchVsSnapshot -benchmem
```

The shift benchmark on an Apple M5 Max measured 5.1 µs and 2.2 KB allocated to build a shift patch. Protobuf encoding took 3.4 µs and 8.0 KB for the shift versus 78.9 µs and 186 KB for the full frame; protocol application took 0.09 µs and 640 B. Gzip reduced the synthetic full frame to 242 B and the shift to 71 B, but cost about 80 µs and 1 MB allocated per message with a fresh compressor. This is a comparison of application payloads and a standalone compressor, not actual socket or WebSocket bytes. The live measurements below (#179) replace it for decisions.

Applying a fresh gzip compressor to every message in the emulator scrolling
workload produced 2,724 B for 30 shift patches versus 8,798 B for 30 full
snapshots, a 69.0% reduction even with compression. Socket and WebSocket
transports do not currently enable this compressor; these figures are a
comparison, not actual transport bytes.

Tests cover one-row edits, styled and wide cells, cursor-only changes, whole-pane shifts in both directions, unrelated edits and resize fallback, new clients, a missed patch followed by a full snapshot, and a client generation mismatch followed by resynchronization. The deterministic server workload reconstructs every delivered patch against a full render. The server concurrency test also runs input, resize, pane close, and frame delivery while one transport does not read.

## Live measurements (#179)

### Reproducing

```sh
make traffic                                  # five scenarios, ~2 minutes
make traffic TRAFFIC_ARGS="--profile"         # also writes pprof files per process
make traffic TRAFFIC_ARGS="--only scroll --seconds 3"
wideboi status --traffic [--json]             # any running session
go test ./internal/client -run '^$' -bench BenchmarkClientApplyAndDraw -benchmem
```

`scripts/traffic.py` starts a real `wideboi server` on a private socket.
Terminal clients attach in ptys, and `scripts/wssink` clients attach over
WebSocket. The script drives fixed inputs, then reads the server's counters
through a sink's own connection. It prints per-client tables and writes raw
JSON under `tmp/traffic/`. Each scenario checks that its workload really ran;
for example, `seq` must finish and vim must page deep into its file.
`WIDEBOI_TRAFFIC_TIMING=1` adds server render, patch-build and encode timing.
`WIDEBOI_CPUPROFILE` / `WIDEBOI_MEMPROFILE` write pprof files when the
process exits. In the browser, `?stats=1` adds a per-5-second console line
and an overlay with decode, per-kind apply and draw timings.

The byte columns mean different things:

- **Payload:** protobuf envelope bytes.
- **Wire:** bytes written to the `net.Conn`, which adds the socket length
  prefix or the WebSocket frame headers, the 101 response, and pings.
- **Deflate:** an estimate made on payload bytes. The sink runs `compress/flate`
  at level 1 in two ways:
  - without context takeover, which is what gorilla/websocket v1.5.3 would
    send if `EnableCompression` were turned on;
  - with context takeover, which gorilla cannot do.

### Results

Apple M5 Max, darwin/arm64, 10 s scenarios, run 2026-09-24. Every client in a
scenario received the same stream. The socket and WebSocket clients had
identical payload counts, and the sinks' own counts matched the server's.
"Active" is the time from workload start to the last pty output. The forced
full snapshot when the pane goes idle falls in the quiet tail after it and is
included in the counts.

| Scenario | Active | Updates/s | Full × B | Patch × B | Pane payload | Wire overhead | Deflate saved (as-is / ctx) |
|---|---:|---:|---:|---:|---:|---:|---:|
| Typing, 80×24, socket + WS | 10.0 s | 15.1 | 2 × 13,528 | 149 × 468 | 96,862 B | 0.6% | 84.5% / 92.9% |
| `seq 1 200000`, 80×24 | 1.8 s | 30.8 | 56 × 13,529 | 0 | 757,644 B | 0.0% | 98.0% / 98.1% |
| Paced scroll (15 lines/s), 80×24 | 11.4 s | 13.4 | 2 × 13,528 | 151 × 1,075 | 189,320 B | 0.3% | 92.1% / 97.4% |
| vim paging, 80×24 | 11.6 s | 15.5 | 38 × 13,528 | 141 × 1,024 | 658,440 B | 0.1% | 93.9% / 97.9% |
| Typing, 160×48, 2 socket + 2 WS | 10.0 s | 15.2 | 2 × 53,922 | 149 × 915 | 244,205 B per client | 0.2–0.3% | 92.8% / 96.5% |

Deflate costs about 13–19 µs per message as-is, or 5–13 µs with context
takeover. Server encode is 12–36 µs per message, rising to about 110 µs for
fulls. Rendering takes about 100 µs per frame at 80×24 and 320 µs at 160×48
when output is moderate. Patch build takes 5–18 µs.

During the `seq` burst, a render reads about 3.9 ms, but only about
0.14 ms of that is CPU. The rest is waiting for the emulator to finish each
≤4 KiB `Write` chunk.

In the first run, `vtGrid.Draw` took `writeResizeMu` on every call. Since
per-client scrollback (#200), the live-view fast path skips that lock, and
#209 (#204) made the comments match. The post-rebase run still read about
3.9 ms, because `SafeEmulator.Write` holds the emulator's own mutex for
each chunk.

During the burst the server uses about 73% of a core, and the terminal
client about 6%. About 80% of the server's CPU is the VT emulator's write
path. Roughly a third of that is `vt.Scrollback.Push`: once scrollback is
full, it `slices.Delete`s the first element and so moves the whole history
on every line. Render, patch build and encode together are under 1%. In the
other scenarios both processes use about 1–2% of a core.

The terminal client costs the same to apply a patch as a full snapshot:
170–180 µs per update at 80×24 and 580–610 µs at 160×48. It rewrites the
whole mirror and composes the whole frame either way. At 30 updates/s this
is under 2% of a core even at 160×48.

After rebasing onto per-client scrollback (#200), a short
`make traffic TRAFFIC_ARGS="--seconds 3"` run showed the same traffic pattern:
- patches of about 465–1,030 B;
- full snapshots the same size as before;
- `seq` still sent only full snapshots.

Server render at 160×48 rose from about 320 µs to about 840 µs per frame. That
is one short run, not re-profiled.

The browser was measured with `?stats=1` by hand: [pending — see below].

### What the numbers say

**Full snapshots are the dominant remaining cost.** Patches work when they are
used: a keystroke costs about 470 B at 80×24 and about 900 B at 160×48. Full
snapshots, however, make up 28% of the typing bytes, 44% of the 160×48 typing
bytes, 78% of vim, and all of `seq`. They come from three sources, none of
which is the wire format:

1. **Sustained output never patches.** When a pane's generation moves while
   its frame is being rendered or sent, `broadcastPaneUpdates` drops that
   client's baseline. Under steady output that happens on nearly every tick,
   so every update goes out as a full snapshot at the 30/s frame cap. The
   lock wait described above widens the window. The client did apply the
   frame it was sent, so keeping that frame as the baseline would let the
   next tick patch.
2. **Unrelated connections force a full resend.** Every broadcast of the
   layout ends with a forced full of every pane to every client. That
   includes a connection that never attached, such as `wideboi status`,
   `status --traffic` or `kill-session`, closing. So each such command costs
   every attached client a full snapshot per pane.
3. **Status flips force a full resend.** A pane changing between working and
   idle triggers the same forced full. Typing and stopping costs two fulls
   per pane per client.

**Framing is negligible** at 0.0–0.6%.

**Compression would pay for WebSocket clients.** Even as gorilla sends it,
without context takeover, it saves 84–98% for about 15 µs per message. Almost
all of the saving comes from full snapshots, which compress from 13.5 KB to a
few hundred bytes. It does little for small patches that context takeover
could not also shrink.

**Client CPU is not the bottleneck** in the terminal client.

### Decision

- **Sparse cell spans: not material, no change.** A one-character patch
  resends one full row, about 470 B. Spans might bring that to about 100 B,
  which saves around 5 KB/s at typing speed. The forced and dropped-baseline
  fulls cost an order of magnitude more, and compression would take most of
  the rest.
- **Incremental client painting: not material, no change** for the terminal
  client (under 2% of a core at 30 updates/s at 160×48). The browser verdict
  waits on the manual `?stats=1` run.
- **Transport compression: worth doing for WebSocket only.** Turn on
  `EnableCompression` in the upgrader. Browsers negotiate permessage-deflate
  themselves. Unix sockets are local, so it does not help them. Follow-up:
  #203.
- **New, and larger than any of the three above:**
  - Keep the sent frame as the baseline when a pane changes mid-send
    (follow-up: #206).
  - Stop forcing a full resend of every pane when an unattached connection
    closes or only a status changes (follow-up: #202).
- **Server CPU under bursts:** the O(scrollback) `Scrollback.Push` in
  `charmbracelet/x/vt` (follow-up: #205). Renders still wait on the
  emulator's per-chunk write lock. The `vtGrid.Draw` lock/comment mismatch
  was resolved in #209 (#204).
