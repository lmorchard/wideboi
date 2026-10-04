# Research: Collapsed Columns (Issue #386)

## 1. Context & Motivation

Wideboi introduced pinned columns anchored to the left of the screen (#373) and responsive layout modes (#379).
Issue #386 requests the inverse capability: collapsing background columns into narrow slivers anchored on the right edge of the terminal, not participating in normal layout width calculations.

This is ideal for long-running processes (tests, watchers, passive agents) where the user wants to monitor progress and status at a glance without having them take up 80 cells of horizontal screen space.

## 2. Layout Design

### Column Partitioning
A `Strip` maintains columns in three partitions:
`[pinned columns (left)] [active columns (middle)] [collapsed columns (right)]`

1. **Pinned Columns (Left):**
   - Anchored at `x = 0 .. pinnedWidth`.
2. **Collapsed Columns (Right):**
   - Anchored at `x = viewportWidth - collapsedTotalWidth .. viewportWidth`.
   - Each collapsed column takes `CollapsedColumnWidth = 3` cells (`dstW = 3`).
   - In 3 cells, the top header row renders the pane's badge/capsule (`●2`, `✔3`, `?4`, `!1`).
   - Content blits column 0..3 of the terminal buffer.
3. **Active Columns (Middle):**
   - Occupy `remainingWidth = viewportWidth - pinnedWidth - collapsedTotalWidth`.
   - Pan/scroll in `ScrollStrategy` and fan in `CardStrategy` across the middle area.

### Interaction & Verbs
- Verb: `VerbToggleCollapse` (key binding: `<prefix> C` in Control mode).
- Commands: `:collapse [pane-id]`, `:uncollapse [pane-id]`, `:expand [pane-id]`, `:toggle-collapse [pane-id]`.
- Partition isolation: `MoveLeft` and `MoveRight` do not allow regular columns to cross into pinned or collapsed partitions.
- Mutual exclusivity: Pinning a collapsed column uncollapses it; collapsing a pinned column unpins it.
