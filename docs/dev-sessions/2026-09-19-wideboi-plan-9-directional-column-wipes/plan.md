# Plan 9 Implementation Plan — Directional Column Wipes

**Goal:** Implement `WipeTransition` in `internal/client/wipe.go` and integrate transition stepping into `Client`.

## Tasks

### Task 1: `WipeTransition` type
- Create `internal/client/wipe.go`.
- Implement `WipeTransition` with left-to-right and right-to-left directional column reveals over 8 steps.
- Hide cursor during active transition.

### Task 2: Client integration
- Integrate transition trigger on focus change in `internal/client/client.go`.
- Draw active transition in `Client.Draw`.

### Task 3: Unit & Smoke Tests
- Write `internal/client/wipe_test.go`.
- Verify with `make check`.

### Task 4: PR creation
- Push branch `plan-9-directional-column-wipes` and open PR.
