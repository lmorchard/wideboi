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

The shift benchmark on an Apple M5 Max measured 5.1 µs and 2.2 KB allocated to build a shift patch. Protobuf encoding took 3.4 µs and 8.0 KB for the shift versus 78.9 µs and 186 KB for the full frame; protocol application took 0.09 µs and 640 B. Gzip reduced the synthetic full frame to 242 B and the shift to 71 B, but cost about 80 µs and 1 MB allocated per message with a fresh compressor. This is a comparison of application payloads and a standalone compressor, not actual socket or WebSocket bytes. Issue #179 still calls for live session traffic and client painting measurements.

Applying a fresh gzip compressor to every message in the emulator scrolling
workload produced 2,724 B for 30 shift patches versus 8,798 B for 30 full
snapshots, a 69.0% reduction even with compression. Socket and WebSocket
transports do not currently enable this compressor; these figures are a
comparison, not actual transport bytes.

Tests cover one-row edits, styled and wide cells, cursor-only changes, whole-pane shifts in both directions, unrelated edits and resize fallback, new clients, a missed patch followed by a full snapshot, and a client generation mismatch followed by resynchronization. The deterministic server workload reconstructs every delivered patch against a full render. The server concurrency test also runs input, resize, pane close, and frame delivery while one transport does not read.
