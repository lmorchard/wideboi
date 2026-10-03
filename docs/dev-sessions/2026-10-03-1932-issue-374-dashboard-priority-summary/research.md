# Research: Prioritize Attention-Needed Panes & Fleet Summary in Dashboard (Issue #374)

## 1. Context & Problem

The in-app status dashboard (`internal/server/dashboard.go`, `<prefix> s`, #196) renders an in-memory VT table of active panes:
- Panes are currently listed in whatever order they were created / passed into `Render()`.
- When many panes exist, panes that have failed (`StatusFailed`) or require human input/permission (`StatusNeedsInput`) can be buried down the list.
- There is no high-level fleet summary banner at the top of the dashboard summarizing total counts (`1 needs input • 2 working • 1 done • 3 idle`).
- When panes re-render on status changes, row selection index is not preserved by pane ID.

## 2. Codebase Touchpoints

- `internal/server/dashboard.go`:
  - `PaneInfo`: Struct containing `ID`, `Status`, `Title`, `CWD`, `Width`, `Height`, `Focused`.
  - `Dashboard`: Struct managing `selected int`, `panes []PaneInfo`.
  - `Render(panes []PaneInfo, cols, rows int) []byte`: Renders header, rows, and footer as ANSI bytes into VT grid.
  - `HandleKey(ev uv.KeyEvent)`: `j`/`k`/arrow keys for moving selection; `Enter` for jumping.
  - `HandleMouse(ev uv.MouseEvent)`: Left click jumps to selected pane; scroll wheel moves selection.
- `internal/server/dashboard_test.go`:
  - Unit tests for rendering, selection, mouse clicks, and keyboard navigation.

## 3. Design & Architecture

1. **Urgency Sorting Priority**:
   - `StatusNeedsInput`: 5 (top priority)
   - `StatusFailed`: 4
   - `StatusDone`: 3
   - `StatusWorking`: 2
   - `StatusIdle`: 1
   - Secondary sort: `ID` ascending (stable and deterministic).
2. **Selection Tracking Across Re-sorts**:
   - Save previously selected pane ID before sorting.
   - Restore `selected` to the index of that pane ID after sorting.
3. **Fleet Summary Banner**:
   - Render a dedicated top banner row above the column headers:
     `[ wideboi ] 1 needs input • 2 working • 1 done • 3 idle`
   - Adjust mouse click row mapping for the new header row offset.
