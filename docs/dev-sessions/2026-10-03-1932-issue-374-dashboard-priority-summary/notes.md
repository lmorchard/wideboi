# Notes: Prioritize Attention-Needed Panes & Fleet Summary in Dashboard (Issue #374)

- 2026-10-03: Session started in worktree `/home/lmorchard/devel/wideboi-issue-374` on branch `feat/issue-374-dashboard-priority-summary`.
- Researched `dashboard.go` and `dashboard_test.go`.
- Implemented `statusPriority` urgency sorting: `StatusNeedsInput` (5) -> `StatusFailed` (4) -> `StatusDone` (3) -> `StatusWorking` (2) -> `StatusIdle` (1), secondary sorted by pane ID.
- Added selection tracking across re-sorts so user cursor remains locked to the selected pane ID.
- Added top summary banner row: `  [ wideboi dashboard ]  %s`.
- Updated mouse click calculations in `HandleMouse` for the 2-row header offset.
- Added comprehensive unit tests in `dashboard_test.go` (`TestDashboardPrioritySorting`, `TestDashboardSelectionPreservedAcrossResort`).
- Verified all 9 dashboard tests and `make quick` passed cleanly.
