# wideboi Plan 8 — Card Layout

**Date:** 2026-09-19
**Parent spec:** `docs/BEYOND-V1.md` §2
**Status:** In Progress

## Goal

Implement `CardStrategy` in `internal/layout` as an alternative layout strategy to `ScrollStrategy`.

In `CardStrategy`, off-screen/peripheral columns overlap as "cards" showing a narrow vertical sliver (4 cells wide), with the focused pane displayed on top at full logical width (`Z = 1`).

## Architecture & Design

1. **`CardStrategy`:**
   - Implements `Strategy` interface (`ComputePlacements(s *Strip, vw, vh int) []Placement`).
   - Calculates sliver placements for cards to the left and right of focus.
   - Focused card gets full width at `Z = 1` (or highest Z).
   - Peripheral cards get clipped `Dst` rectangles of width 4 at `Z = 0`.
   - Preserves invariant 4 (`ColumnWidth(paneID)` remains equal to the logical column width).

2. **Strip Strategy Selection:**
   - `Strip` supports setting layout strategy via `SetStrategy(st Strategy)`.
   - Default remains `ScrollStrategy{}` if unconfigured.

3. **Testing:**
   - Property tests for `CardStrategy` using `rapid` in `internal/layout/card_test.go`.
