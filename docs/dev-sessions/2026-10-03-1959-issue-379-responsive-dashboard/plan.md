# Plan: Responsive Compact Layout for Status Dashboard in Narrow Columns (Issue #379)

**Goal:** Implement responsive layout modes (Wide, Compact, Drawer) in `Dashboard.Render` so the dashboard displays clear, legible status information without clipping at narrow widths (20–35 cols).

**Approach:** Break `Render` into width-aware formatters:
- `formatStatusGlyph(st protocol.PaneStatus) string`
- Build width-appropriate banners, headers, row formats, and footers based on `cols >= 60`, `38 <= cols < 60`, and `cols < 38`.

**Tech stack:** Go, standard strings/formatting, ultraviolet VT terminal emulator.

---

## Phase 1: Responsive Layout Implementation

Implement the three layout modes in `internal/server/dashboard.go`.

**Files:**
- Modify: `internal/server/dashboard.go`
- Test: `internal/server/dashboard_test.go`

**Key changes:**
- Add `formatStatusGlyph(st protocol.PaneStatus) string`.
- In `Dashboard.Render()`:
  - If `cols >= 60`: Wide mode (full 4-column layout with CWD, full banner).
  - If `38 <= cols < 60`: Compact mode (3-column layout without CWD, shortened banner).
  - If `cols < 38`: Drawer mode (glyph + ID + title, `[wb] 1! 2▲` banner, compact header).
  - Adaptive footer based on width.

**Verification — automated:**
- [x] `go test -v ./internal/server -run TestDashboard` passes — **verified all existing tests pass with responsive formatters**

---

## Phase 2: Dedicated Responsive Tests & Project Verification

Add tests for all three width modes and edge cases, then verify with `make quick`.

**Files:**
- Modify: `internal/server/dashboard_test.go`

**Key changes:**
- Add `TestDashboardWideMode`.
- Add `TestDashboardCompactMode`.
- Add `TestDashboardDrawerMode`.
- Add `TestDashboardUltraNarrowMode`.

**Verification — automated:**
- [x] `go test -v ./internal/server -run TestDashboard` passes — **all 15 dashboard tests passed**
- [x] `make quick` passes cleanly — **`go vet`, `seam-check`, Go test suite, and 182 Vitest web tests passed**
