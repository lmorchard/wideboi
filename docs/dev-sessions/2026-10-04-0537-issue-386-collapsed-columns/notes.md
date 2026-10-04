# Notes: Collapsed Columns (Issue #386)

- 2026-10-04: Session started in worktree `/home/lmorchard/devel/wideboi-issue-386` on branch `feat/issue-386-collapsed-columns`.
- Added `collapsed` boolean to `ColumnData` and `VERB_TOGGLE_COLLAPSE`, `VERB_COLLAPSE_PANE`, `VERB_UNCOLLAPSE_PANE` to `VerbType`.
- Bumped wire `protocol.Version` to 29 and updated version guard golden hash.
- Implemented collapsed column partitioning `[pinned cols] [active cols] [collapsed cols]` in `Strip`.
- Added `CollapseColumn`, `UncollapseColumn`, `ToggleCollapseColumn`, and `IsColumnCollapsed` to `Strip`.
- Updated `ScrollStrategy` and `CardStrategy` to anchor collapsed columns to the right margin with `CollapsedColumnWidth = 3`.
- Wired `VerbToggleCollapse` to `<prefix> C` in Control mode and registered `:collapse`, `:uncollapse`, `:expand`, `:toggle-collapse`.
- Updated `web/src/card-layout.ts` and `wideboi-app.ts` to support collapsed columns in the browser.
- All Go tests and 184 web tests passed cleanly.
