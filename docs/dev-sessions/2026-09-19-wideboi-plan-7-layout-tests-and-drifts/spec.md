# wideboi Plan 7 — Layout Property Tests & Strategy Interface

**Date:** 2026-09-19
**Parent spec:** `docs/BEYOND-V1.md` §7
**Status:** In Progress

## Goal

Address the layout core spec-vs-code drifts recorded in `docs/BEYOND-V1.md` §7:
1. Introduce the `Strategy` interface and `ScrollStrategy` in `internal/layout` so alternative layout algorithms (such as Plan 8's Card Layout) have a clean abstraction to implement.
2. Implement property-based tests using `pgregory.net/rapid` covering all 4 layout core invariants across randomly generated strips and viewports.
3. Refine the width cycle (`CycleWidth`) for panes spawned at non-preset custom widths.

## Design & Scope

### 1. Strategy Interface
Define `Strategy` in `internal/layout`:
```go
type Strategy interface {
    ComputePlacements(s *Strip, viewportWidth, viewportHeight int) []Placement
}
```
`ScrollStrategy` becomes a struct implementing `Strategy`. `Strip.ComputePlacements` delegates to `ScrollStrategy{}` by default.

### 2. Rapid Property Tests
Cover the 4 layout core invariants in `internal/layout/layout_test.go`:
- **Invariant 1 (Focused pane visibility):** For any non-empty strip and positive viewport, the placement for `FocusedPaneID()` is within viewport bounds (`0 <= Dst.Min.X` and `Dst.Max.X <= viewportWidth`).
- **Invariant 2 (Non-overlapping Dst):** For any two placements returned by `ComputePlacements`, `Dst` rectangles do not overlap.
- **Invariant 3 (Crop rectangle sizing):** For every placement, `Src.Dx() == Dst.Dx()` and `Src.Dy() == Dst.Dy()`.
- **Invariant 4 (Logical column width invariant):** `ColumnWidth(paneID)` remains invariant regardless of scrolling or viewport cropping.

### 3. Width Cycle Refinement
Ensure `CycleWidth()` handles any initial width correctly by advancing to the next preset (`40 -> 60 -> 80 -> 40`), transitioning smoothly even if the pane started at an arbitrary spawn width (e.g. 99 cells).
