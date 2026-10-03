# Plan: Prioritize Attention-Needed Panes & Fleet Summary in Dashboard (Issue #374)

**Goal:** Sort dashboard rows by status urgency so panes needing human attention appear at the top, preserve selected pane identity across re-sorts, and display a high-level fleet summary banner at the top of the dashboard.

**Approach:** Implement `statusPriority` helper and sort panes using `sort.SliceStable` in `internal/server/dashboard.go`. Generate the summary banner line. Update selection preservation and mouse click row calculation.

**Tech stack:** Go, standard sorting, ultraviolet VT terminal emulator.

---

## Phase 1: Priority Sorting & Selection Tracking

Implement status urgency sorting and selection preservation in `internal/server/dashboard.go`.

**Files:**
- Modify: `internal/server/dashboard.go`
- Test: `internal/server/dashboard_test.go`

**Key changes:**
- Add `statusPriority(st protocol.PaneStatus) int`:
  - `StatusNeedsInput`: 5
  - `StatusFailed`: 4
  - `StatusDone`: 3
  - `StatusWorking`: 2
  - `StatusIdle`: 1
- In `Dashboard.Render()`:
  - Record previously selected pane ID.
  - Sort `d.panes` by priority descending, then ID ascending.
  - Re-find previously selected pane ID to maintain stable user selection.

**Verification — automated:**
- [x] `go test -v ./internal/server -run TestDashboard` passes — **verified TestDashboardPrioritySorting, TestDashboardSelectionPreservedAcrossResort**

---

## Phase 2: Fleet Summary Banner & Header Offset

Implement the fleet summary banner and update mouse click calculations.

**Files:**
- Modify: `internal/server/dashboard.go`
- Test: `internal/server/dashboard_test.go`

**Key changes:**
- Count panes by status category.
- Render top summary banner: `  [ wideboi dashboard ]  %s`
- Update `HandleMouse` to account for the 2-row header offset (`idx := ev.Y - 2`).

**Verification — automated:**
- [x] `go test -v ./internal/server -run TestDashboard` passes — **all 9 dashboard tests passed**
- [x] `make quick` passes cleanly — **`go vet`, `seam-check`, Go test suite, and 182 Vitest web tests passed**
