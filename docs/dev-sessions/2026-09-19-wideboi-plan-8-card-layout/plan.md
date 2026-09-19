# Plan 8 Implementation Plan — Card Layout

**Goal:** Implement `CardStrategy` in `internal/layout/card.go`, wire strategy selection on `Strip`, and add property-based tests.

## Tasks

### Task 1: Implement `CardStrategy`
- Create `internal/layout/card.go`.
- Implement `CardStrategy` with 4-cell slivers for peripheral cards and `Z` layering.
- Add `Strip.SetStrategy` / `Strip.Strategy()` methods.

### Task 2: Property & Unit Tests for `CardStrategy`
- Create `internal/layout/card_test.go`.
- Write unit tests for card placement geometries and z-ordering.
- Write `rapid` property tests verifying focused pane visibility and column width invariance under `CardStrategy`.

### Task 3: Verification & PR creation
- Run `make check`.
- Commit, push branch `plan-8-card-layout`, and open PR.
