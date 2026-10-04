# Spec: Collapsed Columns (Issue #386)

**Goal:** Allow users to collapse background columns into narrow vertical slivers anchored to the right margin of the viewport, freeing active layout width while keeping agent status visible.

**Source:** https://github.com/lmorchard/wideboi/issues/386

---

## Requirements

### 1. Protocol & Schema
- `ColumnData`:
  - Add `Collapsed bool` (`bool collapsed = 5;` in proto).
- `VerbType`:
  - Add `VerbToggleCollapse = 25;` (`VERB_TOGGLE_COLLAPSE = 25;` in proto).
- Wire `protocol.Version` bumped to 29.
- TypeScript `CardColumn` in `web/src/card-layout.ts` gains `collapsed?: boolean`.

### 2. Layout Engine (`internal/layout/`)
- `Column.Collapsed bool` field.
- Strip methods:
  - `CollapseColumn(paneID int) bool`
  - `UncollapseColumn(paneID int) bool`
  - `ToggleCollapseColumn(paneID int) bool`
  - `IsColumnCollapsed(paneID int) bool`
- Partitioning order:
  `[pinned cols] [active cols] [collapsed cols]`
- `ScrollStrategy.ComputePlacements`:
  - Computes `collapsedTotalWidth = len(collapsedCols) * (CollapsedColumnWidth + 1)`.
  - Places `collapsedCols` on the right: `dstX := viewportWidth - collapsedTotalWidth + ...`.
  - Middle unpinned area gets `max(0, viewportWidth - pinnedWidth - collapsedTotalWidth)`.
- `CardStrategy.ComputePlacements`:
  - Deducts `collapsedTotalWidth` from `remainingViewport`.
  - Places `collapsedCols` on the right; fans unpinned cards across the middle.

### 3. Server & Upgrade State
- Handle `VerbToggleCollapse` in `handleVerbLocked`.
- Preserve `Collapsed` state across server upgrades in `internal/server/upgrade.go`.
- Support `Collapsed` on `StartupPane`.

### 4. Keybindings & Commands
- Bind `<prefix> C` (uppercase C / shift+c) to `VerbToggleCollapse`.
- Register `:collapse`, `:uncollapse`, `:expand`, `:toggle-collapse` in `internal/commands/registry.go`.

### 5. Web Client
- Update `web/src/card-layout.ts` to support `collapsed?: boolean`.
- Update `web/src/wideboi-app.ts` to pass `collapsed` and style collapsed columns.
