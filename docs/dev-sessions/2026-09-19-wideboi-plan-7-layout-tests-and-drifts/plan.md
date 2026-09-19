# Plan 7 Implementation Plan — Layout Property Tests & Strategy Interface

**Goal:** Introduce the `Strategy` interface and `ScrollStrategy` in `internal/layout`, implement `rapid` property-based tests for the 4 layout invariants, and refine the width cycling logic.

## Tasks

### Task 1: Strategy interface and `ScrollStrategy`
- Define `type Strategy interface` in `internal/layout/layout.go`.
- Define `type ScrollStrategy struct{}` implementing `Strategy`.
- Update `Strip.ComputePlacements` to delegate to `ScrollStrategy{}`.
- Verify `go test ./internal/layout/...`.

### Task 2: Rapid property tests for layout invariants
- Write property tests in `internal/layout/layout_test.go` using `pgregory.net/rapid`.
- Assert Invariant 1 (Focused pane fully visible).
- Assert Invariant 2 (Non-overlapping `Dst` rectangles).
- Assert Invariant 3 (`Src` and `Dst` dimensions match).
- Assert Invariant 4 (`ColumnWidth` invariant).
- Verify tests pass with `go test ./internal/layout/ -count=1`.

### Task 3: Refine `CycleWidth` logic
- Update `CycleWidth()` so any custom width (e.g. 99) cycles predictably through presets (`40 -> 60 -> 80 -> 40`).
- Add unit and property tests covering `CycleWidth`.

### Task 4: Full verification & PR creation
- Run `make check`.
- Create branch `plan-7-layout-tests-and-drifts`, commit, push, and open PR.
