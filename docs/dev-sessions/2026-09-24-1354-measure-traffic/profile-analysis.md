# Profile analysis (#179, Phase 8 input)

Source: `make traffic TRAFFIC_ARGS="--profile"` first full run
(`tmp/traffic/traffic-20260924-150246.json`, Apple M5 Max, darwin/arm64), and
the existing benchmarks. CPU profiles sample at 10 ms. Every profile except
`scroll` server has only 9–56 samples (about ±10 points); `scroll` server has
238 samples. Profiles cover the whole process lifetime (17–26 s), not only the
workload window.

## CPU used (measured)

| scenario | server CPU | client CPU | workload s | server CPU ÷ workload |
|---|---|---|---|---|
| typing | 0.09 s | 0.23 s | 10.5 | 0.9% |
| scroll (`seq 1 200000`) | **2.38 s** | 0.20 s | 3.27 | **~73% of a core** |
| scroll-paced | 0.09 s | 0.28 s | 12.4 | 0.7% |
| tui (vim paging) | 0.12 s | 0.31 s | 12.1 | 1.0% |
| large (160×48, 4 clients) | 0.21 s | 0.56 / 0.55 s | 10.5 | 2.0% |

Outside the burst, both processes are nearly idle.

## Where the server spends CPU in `scroll` (measured)

- The write path (`vtGrid.Write` → emulator `index` → `ScrollUp`/`DeleteLine`)
  is 79.8% cumulative.
  - `uv.Buffer.DeleteLineArea`: 38.7% cumulative.
  - `vt.Scrollback.Push` → `slices.Delete(s.lines, 0, 1)` memmove: about 30%.
    Once the 10,000-line scrollback is full, each new line moves the whole
    slice header array. This is O(scrollback) per line, upstream in
    `charmbracelet/x/vt` `scrollback.go`.
- Render, patch build and encode together: under 1%.
- Allocation: scrollback `slices.Clone` in `Push` is 63% of 201 MB. Render is
  14.6% and encode 13.4%.

In the quiet scenarios, server allocation is dominated by render: 60–80% of
`alloc_space`. That is a fresh `uv.NewScreenBuffer` plus a full `LineData`
grid per render, even when a patch is sent.

## Client (measured, low sample counts)

- CPU is draw/compose/present first (12–32% of samples), with apply and decode
  single digits; the rest is scheduler idle.
- Allocations are split between draw and apply: `uv.NewCell` / `Cell.Clone`
  from rewriting the whole mirror each update.
- The Phase 4 benchmark agrees. A row or shift patch costs the terminal
  client the same as a full snapshot: ~170 µs at 80×24, ~600 µs at 160×48.

## Why render reads ~4.5 ms during the burst (inference, high confidence)

- On-CPU render in `scroll` is about 0.01 s of the 0.32 s timed, so about
  0.14 ms per frame. The rest is waiting.
- `Pane.UpdateMessage` → `vtGrid.Draw` takes `g.writeResizeMu.Lock()`
  unconditionally (`internal/server/term/grid.go:556-558`). `vtGrid.Write`
  holds the same mutex for each ≤4 KiB chunk (`grid.go:330-349`), which costs
  about 5 ms of emulator work during the burst.
- The `writeResizeMu` doc comments (`grid.go:182-186`, `:552-555`) say Draw's
  fast path skips the lock; the code does not. Even without that lock,
  `SafeEmulator.Write` holds its own mutex per chunk.
- `broadcastPaneUpdates` runs on the Run loop, so the wait stalls the frame
  tick and widens the window for a generation change during render.

## Why sustained output never patches (code)

- After a successful send, `server.go` bookkeeping deletes `paneGens` and
  `paneFrames` when `r.pane.Generation() != r.gen`. The next tick therefore
  has no baseline and sends a full snapshot.
- Keeping `r.gen` + `r.frame` as the baseline on that path looks safe.
  - The client stores exactly `r.frame` labelled `r.gen`.
  - `BuildPanePatch` needs `next.Generation > base.Generation`, and the atomic
    counter is monotonic.
  - Patches compare rendered rows, so content newer than its label only
    shrinks the next patch.
  - Recovery paths are unchanged: a rejected send forgets the baseline,
    resync deletes it, and a failed client apply requests a resync.
- It will not help the `seq` burst itself: consecutive frames share no rows.
  It helps sustained moderate output (streaming agent text, spinners).

## Benchmarks (measured, same machine)

```
BenchmarkPaneUpdateRender/80x24     52 µs   327 KB/op
BenchmarkPaneUpdateRender/160x48   216 µs   1.28 MB/op
BenchmarkPaneWirePayload typing_80x24 full 86 µs 13,525 B | rows 3.8 µs 578 B
BenchmarkPaneWirePayload typing_160x48 full 330 µs 53,919 B | rows 7.0 µs 1,139 B
BenchmarkPanePatchBuildAndApply 80x24 build 3.7 µs, apply 0.09 µs
BenchmarkScrollPatchVsSnapshot build_shift 5.5 µs, shift marshal 3.7 µs vs snapshot 81 µs
```
