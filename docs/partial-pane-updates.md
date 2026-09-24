# Partial pane updates (#158)

## Wire contract

`MsgPaneUpdate` is a complete pane snapshot with a generation. A new client, a resize, a layout resend, a missed send, or a resynchronization request receives one. `MsgPanePatch` names the exact `BaseGeneration` it extends, a new generation, complete replacement rows, and cursor and mouse state. Empty `ChangedRows` represents a cursor or mouse-only change. The server compares rendered rows instead of trusting the emulator's `Touched()` flags, which have known resize behavior (`docs/LESSONS.md`). When at least half the rows changed, it sends a full snapshot; this includes typical scrolling.

The server keeps the last accepted full state for each client and pane. `paneSendMu` orders successive sends. A client applies a patch only if its pane ID, dimensions, and generation match its baseline; otherwise it requests `MsgPaneResync`. A send rejected by the transport invalidates the server's baseline, so the next send is full. If a transport accepted but lost a message, the client detects the mismatch on a later patch and requests a full snapshot. Layout-triggered sends are full because a layout snapshot may replace a client's mirror. Both the terminal and browser clients reconstruct the same `MsgPaneUpdate` state from patches.

## Measurements

Synthetic uncompressed JSON, Apple M5 Max (darwin/arm64), `-benchtime=100x`:

| Workload | Full snapshot | Row patch | Reduction |
| --- | ---: | ---: | ---: |
| One changed row, 80×24 | 426,412 B | 17,927 B | 95.8% |
| Scrolling, 80×24 (all rows) | 426,412 B | 426,835 B | none; full snapshot selected |
| One changed row, 160×48 | 1,705,181 B | 35,688 B | 97.9% |

JSON encoding took about 0.91 ms for an 80×24 full update versus 37 µs for its one-row patch; at 160×48 it took 3.48 ms versus 73 µs. Complete frame construction took 65 µs and 327 KB allocated at 80×24, 289 µs and 1.28 MB at 160×48, and 608 µs and 2.85 MB at 240×72 in the earlier benchmark. Comparing rows to build a patch added 3.8 µs and 32 B allocated at 80×24, or 14.7 µs and 32 B at 160×48. Applying it to a Go protocol snapshot added about 0.14 µs and 640 B at 80×24, or 0.15 µs and 1.3 KB at 160×48. The terminal client currently repaints its mirror from the reconstructed full snapshot, so its total patch handling cost is higher than the protocol-only apply benchmark.

The server ticks every 33 ms, giving a sustained changed pane an upper bound of about 30 updates/s. At that rate, one 80×24 pane with a one-row change would send about 12.8 MB/s in full JSON per client versus 0.54 MB/s in row patches; four clients would receive about 51 MB/s versus 2.2 MB/s in aggregate. These are calculated upper bounds, not measured live workload rates. Actual rates depend on PTY output and frame coalescing. These benchmarks omit WebSocket framing and compression. Full frames are still rendered once per changed pane, and the server retains a baseline per client; patch comparison is repeated for each client baseline.

Repeat the payload and CPU measurements with:

```sh
go test ./internal/server -run '^$' -bench 'BenchmarkPane(UpdateRender|JSONPayload)$' -benchmem
go test ./internal/protocol -run '^$' -bench BenchmarkPanePatchBuildAndApply -benchmem
```

Tests cover a one-row edit, styled and wide cells, cursor-only changes, scrolling fallback, resize fallback, new clients, a missed patch followed by a full snapshot, and a client generation mismatch followed by resynchronization. The server concurrency test also runs input, resize, pane close, and frame delivery while one transport does not read.
