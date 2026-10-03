# Spec: Prioritize Attention-Needed Panes & Fleet Summary in Dashboard (Issue #374)

**Goal:** Sort dashboard rows by status urgency so panes needing human attention appear at the top, preserve selected pane identity across re-sorts, and display a high-level fleet summary banner at the top of the dashboard.

**Source:** https://github.com/lmorchard/wideboi/issues/374

---

## Current State

- `Dashboard.Render()` renders panes in the raw slice order provided by the caller.
- Attention-needed panes (`StatusNeedsInput`, `StatusFailed`) can be buried beneath idle or working panes.
- No aggregate summary of session health is displayed.
- Mouse click maps row index assuming 1 header row (`idx := ev.Y - 1`).

---

## Desired End State

1. **Priority Sorting:**
   - Panes are sorted by status urgency before display:
     `StatusNeedsInput` (5) &rarr; `StatusFailed` (4) &rarr; `StatusDone` (3) &rarr; `StatusWorking` (2) &rarr; `StatusIdle` (1).
   - Secondary sort by `Pane ID` ascending.
   - Selected pane identity is preserved across re-sorts: if Pane 3 was selected, `d.selected` updates to Pane 3's new index.

2. **Fleet Summary Banner:**
   - Display a banner above the column headers:
     `  [ wideboi ]  1 needs input  •  2 working  •  1 done  •  3 idle`
   - Omit counts that are zero (e.g. if 0 failed, omit failed; if all idle, show `4 idle`).
   - If no active panes, show `no active panes`.

3. **Mouse Interaction:**
   - Mouse click respects the 2-row header offset: clicking headers is ignored, clicking data rows selects and jumps to that pane.

---

## Design Decisions

- **Decision:** Sort panes inside `Dashboard.Render()` before updating `d.panes`.
  - **Why:** Keeps server caller (`server.go`) simple while ensuring any render updates row ordering consistently.
- **Decision:** Selection tracking by Pane ID.
  - **Why:** Status updates happen in real time via the 33ms server broadcast ticker. If selection index were static, a re-sort would unexpectedly move the user's cursor to a different pane.

---

## What We're NOT Doing

- We are not adding interactive filtering/searching shortcuts in this slice (focus is on sorting and summary banner).
- We are not modifying wire protocols; `Dashboard` is entirely an internal ANSI rendering mechanism.

---

## Verification Plan

- Unit tests in `internal/server/dashboard_test.go`:
  - Verify panes are sorted by priority: `StatusNeedsInput` first, then `StatusFailed`, `StatusDone`, `StatusWorking`, `StatusIdle`.
  - Verify secondary sort by pane ID.
  - Verify selection tracks pane ID across re-sorts.
  - Verify summary banner formats counts accurately.
  - Verify mouse click navigation with the 2-row header offset.
- `make quick` and full suite passes cleanly.
