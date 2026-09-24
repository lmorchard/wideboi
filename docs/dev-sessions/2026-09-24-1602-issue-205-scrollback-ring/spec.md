# vt.Scrollback Ring Buffer and Allocation Optimization Spec

**Goal:** Eliminate the O(scrollback) memmove and excessive heap allocation churn in `vt.Scrollback.Push` when scrollback is full, significantly reducing server CPU and memory pressure during high-throughput terminal scrolling.

**Source:** GitHub Issue #205 (https://github.com/lmorchard/wideboi/issues/205)

## Current state

- `vt.Scrollback` (`github.com/charmbracelet/x/vt/scrollback.go:13-55`) stores lines in a flat `[]uv.Line` slice up to `maxLines` (default 10,000).
- When full (`len(s.lines) >= s.maxLines`), `Push` calls `s.lines = slices.Delete(s.lines, 0, 1)`, which copies all 9,999 remaining slice headers (~240 KB memmove) on every single line pushed (`research.md:1-19`).
- Every pushed line allocates a new backing slice via `cloned := slices.Clone(line[:lastNonEmpty+1])` (`scrollback.go:48`).
- In profiling (#179), scrolling flooded with output used ~73% of a core, of which ~30% was `Scrollback.Push`'s `memmove`, and 63% of burst allocations came from `slices.Clone`.
- Upstream has open PRs (PR 888 for ring-buffer eviction and fast blank check; PR 822 for line buffer reuse) that have not yet been merged into `charmbracelet/x/vt` (`research.md:15-18`).

## Desired end state

- `charmbracelet/x/vt` is maintained in a remote fork under `lmorchard/x` on a dedicated branch (e.g., `vt-scrollback-ring`), incorporating:
  1. O(1) ring-buffer eviction via head index and modular indexing in `Line(i)` and `Lines()` (from upstream PR 888).
  2. Reusable line buffer pooling / in-place slice overwrite to eliminate `slices.Clone` allocation churn when the ring is full (combining PR 888 and PR 822).
- `wideboi` references this fork via a `replace` directive in `go.mod`:
  `replace github.com/charmbracelet/x/vt => github.com/lmorchard/x/vt <commit>`
- Dropping the fork when upstream merges is a simple 1-line removal in `go.mod` and `go mod tidy`.
- A dedicated benchmark and test in `internal/server/pane_traffic_test.go` or `internal/server/term/` verifies:
  - Benchmark comparing throughput/allocations before and after on full scrollback (10,000 lines).
  - All existing scrollback navigation, content pinning, resize, and reflow tests continue to pass with zero regressions.

## Design decisions

- **Decision:** Remote fork under `lmorchard/x` with `go.mod` replace directive.
  - **Why:** Keeps the wideboi repo free of vendored third-party code; provides branch stability compared to pointing at a third-party developer's unmerged PR branch; makes dropping the fork trivial (1 line in `go.mod`) when upstream merges.
  - **Rejected:** Vendoring local files in `wideboi` (pollutes repo tree); waiting for upstream merge (upstream PR has been open since June without action).
- **Decision:** Combine PR 888 (ring buffer) and PR 822 (line buffer recycling).
  - **Why:** Issue #205 specifically identified both the 30% memmove CPU cost AND the 63% allocation churn from `slices.Clone`. Addressing only one leaves the other as a major bottleneck during scrolling bursts.
  - **Rejected:** Ring buffer only (leaves 63% heap churn intact).
- **Decision:** Clean fallback for `Lines()` and `SetMaxLines`.
  - **Why:** While wideboi only accesses `ScrollbackLen()` and `ScrollbackCellAt(x, y)` (which uses `Line(i)`), `Lines()` must linearize correctly if called so external/test contracts remain strictly honored.
  - **Rejected:** Breaking `Lines()` or returning out-of-order slices.

## Patterns to follow

- Pinned dependency practices and Go probes (`docs/LESSONS.md:24-62`).
- Safe emulator access and cell extraction (`internal/server/term/grid.go:704-733`).
- Deterministic traffic workload tests (`internal/server/pane_traffic_test.go:20-65`).

## What we're NOT doing

- We are NOT modifying `term.Grid`'s interface or adding a bespoke scrollback implementation outside `vt.Emulator`.
- We are NOT altering terminal emulator escape parsing, OSC handlers, or reflow logic.
- We are NOT altering how client scroll offsets or unread flags are handled in `internal/server/server.go`.

## Open questions

None. All architectural decisions have been resolved.
