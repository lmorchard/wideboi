# Partial pane update investigation (#158)

## Measurements

Run on an Apple M5 Max (darwin/arm64), Go benchmark with `-benchtime=100x`:

| Workload | Full JSON | Changed-row prototype | Row reduction |
| --- | ---: | ---: | ---: |
| One changed row, 80×24 | 426,397 B | 17,883 B | 95.8% |
| Scrolling, 80×24 (all rows) | 426,397 B | 426,791 B | none |
| One changed row, 160×48 | 1,705,166 B | 35,643 B | 97.9% |

JSON encoding took about 1.06 ms for an 80×24 full update versus 40 µs for its one-row prototype; at 160×48 it took 3.87 ms versus 78 µs. Full-grid construction alone took 65 µs and 327 KB allocated at 80×24, 289 µs and 1.28 MB at 160×48, and 608 µs and 2.85 MB at 240×72. Run `GOCACHE=/private/tmp/wideboi-go-cache go test ./internal/server -run '^$' -bench 'BenchmarkPane(UpdateRender|JSONPayload)$' -benchmem` to repeat.

These are synthetic, uncompressed JSON messages with default-style cells. The row prototype includes a pane ID, base and new generations, row coordinates and complete cell data for each changed row, plus cursor and mouse state. It does not include diffing cost, client reconstruction cost, WebSocket framing, or compression. The current wire format does not carry generation numbers, so this is a payload comparison, not an implementation of partial delivery. With multiple clients, outgoing bytes scale roughly by client count; rendering and diffing can be shared only if their accepted baselines agree.

## Suggested protocol

Use a full snapshot for attach, reconnect, resize, layout resends, and recovery. Send a row patch only when a client has accepted the exact `BaseGeneration` and the encoded patch is smaller than a full snapshot. Each patch should carry `PaneID`, `BaseGeneration`, `Generation`, complete replacement rows with row indices, and cursor visibility/position and mouse-tracking state. A cursor-only change then has no rows. Complete rows preserve styled cells and wide-glyph continuation cells without defining span boundaries first. Scrolling can touch every row and should normally send a full snapshot.

The server must track the last accepted baseline per client and pane. On a failed `SendServer`, it must keep or invalidate that baseline and retry with a full snapshot. The client must reject a patch whose base does not match its current generation and request a full snapshot. The transport must preserve per-pane order; `paneSendMu` currently serializes broadcasts. A newly attached client has no baseline. Layout snapshots can blank mirrors, so their forced pane resends must remain full snapshots.

Do not use emulator `Touched()` as the only source of dirty rows: its resize behavior is documented in `docs/LESSONS.md`. Comparing consecutive rendered snapshots is reliable but costs a full render and a cell comparison. A later grid-level dirty-region API would need its own mutation and resize tests. Both terminal and browser clients need the same patch application rules and fixtures before enabling patches on the wire. Tests should include one-cell typing, cursor-only moves, scrolling, styled and wide cells, resize, attach/reconnect, and a dropped patch followed by recovery.
