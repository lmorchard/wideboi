# Notes: Issue 205 - vt.Scrollback Ring Buffer

## Summary

Addressed Issue #205 ("vt.Scrollback.Push is O(scrollback) per line once full") by forking `charmbracelet/x` under `lmorchard/x`, creating branch `vt-scrollback-ring` with ring-buffer eviction (from upstream PR 888) and line buffer recycling (from upstream PR 822), and wiring it into `wideboi` via a single `replace` directive in `go.mod`.

## What Changed

1. **Forked `charmbracelet/x`**:
   - Repository: `https://github.com/lmorchard/x`
   - Branch: `vt-scrollback-ring`
   - Commit: `cacc71cdcc0f`
2. **Upstream changes in `vt/scrollback.go`**:
   - Added `head int` field to `vt.Scrollback` for ring buffer indexing.
   - Fast-path for structurally blank cell scan (`*c == uv.EmptyCell || *c == (uv.Cell{})`), avoiding interface-unwrapping color equality checks for blank trailing cells.
   - When the ring buffer is full (`len(s.lines) >= s.maxLines`), in-place reuse of the evicted line buffer (`s.lines[s.head]`) when capacity allows, completely eliminating slice allocation churn.
   - Eviction is O(1): in-place overwrite at `s.head` and `s.head = (s.head + 1) % len(s.lines)`. Eliminates `slices.Delete(s.lines, 0, 1)` memmove of 9,999 slice headers (~240 KB per line).
   - `Line(i)` provides O(1) modular indexing without allocations.
   - `Lines()` linearizes into an allocated slice in ring order when wrapped (`s.head != 0`), or returns `s.lines` directly when not wrapped.
   - `Clear()` resets lines and head to 0.
   - `SetMaxLines(maxLines)` re-linearizes and resets head to 0 whenever wrapped (`s.head != 0`), preserving chronological ordering if capacity grows or shrinks.
   - Added unit tests and benchmarks in `vt/scrollback_test.go`.
3. **`wideboi/go.mod`**:
   - Added `replace github.com/charmbracelet/x/vt => github.com/lmorchard/x/vt v0.0.0-20260924233645-cacc71cdcc0f`.
   - Dropping this fork when upstream merges is a single-line deletion in `go.mod`.
4. **`wideboi/internal/server/term/grid_bench_test.go`**:
   - Added `BenchmarkGridScrollbackFull` measuring continuous writing through the terminal emulator and scrollback push when the 10,000-line scrollback buffer is completely full.

## Benchmark Results

- **Standalone `Scrollback.Push` when full (`BenchmarkScrollbackPushFull` in `vt`)**:
  - `303.6 ns/op`
  - `0 B/op`
  - `0 allocs/op`
  - Completely zero heap allocations and sub-microsecond execution per line once scrollback is full.
- **End-to-end `Grid.Write` with full 10,000-line scrollback (`BenchmarkGridScrollbackFull` in `wideboi`)**:
  - `8234 ns/op` across the entire ANSI parsing and terminal emulator pipeline.

## Verification

- `go test ./...` in `vt`: 100% pass (all 11 `TestScrollback` cases pass, including growth after wrapping).
- `internal/server/term/grid_test.go`: `TestGridDrawAtWrappedScrollback` verifies full 10,000-line wrapped scrollback history navigation and row rendering at offsets 0, 5000, and 10000.
- `make quick`: passed cleanly.
- `make check`: passed cleanly:
  - `fmt-check`: clean
  - `lint`: clean
  - `seam-check`: clean
  - `test`: all Go unit tests pass
  - `web-test`: all 7 test files (21 tests) pass
  - `web-accept`: Playwright browser lifecycle and layout tests pass
  - `race`: all race-detector tests pass with `-count=1`
  - `verify-exit`: all process signal teardown checks pass
  - `smoke`: all 37 smoke tests pass
  - `attach-check`: all 25 socket/pty attach tests pass
- `make traffic`: all 5 live workloads run cleanly with zero errors.
