# Spec: Pinned Columns (Issue #373)

**Goal:** Allow one or more columns to stay statically pinned to the left edge of the terminal in both scroll and card layout modes, consuming fixed width and allowing the remaining unpinned columns to scroll or fan independently in the remaining viewport space.

**Source:** https://github.com/lmorchard/wideboi/issues/373

---

## Current State

- All columns in a session participate in scroll or card fan layouts.
- Navigating to other columns in wide sessions causes columns on the far left to scroll or collapse into slivers.
- The status dashboard (#379, #380) can render in Drawer mode at 28 columns, but cannot yet stay anchored to the screen edge while working in other panes.

---

## Desired End State

1. **Pinned Column Layout:**
   - Columns marked `Pinned: true` are anchored to the left of the screen:
     - Placed at `x = 0, x = w_0 + 1, ...`
     - Followed by a 1-cell column divider.
   - Pinned columns consume $\text{pinnedWidth} = \sum (\text{col.Width} + 1)$ cells.
   - The remaining viewport space ($\text{unpinnedWidth} = \text{viewportWidth} - \text{pinnedWidth}$) is used by the unpinned columns:
     - In **Scroll mode**: Unpinned columns scroll horizontally within `[pinnedWidth, viewportWidth]`.
     - In **Card mode**: Unpinned columns form the card fan within `[pinnedWidth, viewportWidth]`.
   - If $\text{pinnedWidth} \ge \text{viewportWidth}$, pinned columns consume the entire viewport and unpinned columns are unplaced.

2. **Focus & Navigation:**
   - Pinned columns are fully interactive: they receive focus, keyboard input, and mouse clicks.
   - Focus navigation (`FocusLeft`, `FocusRight`, `colIndex`) traverses pinned columns and unpinned columns in visual left-to-right order.
   - Focusing a pinned column does not scramble or discard the unpinned layout scroll/card state.

3. **Pinning Controls:**
   - **Configuration:** `[[startup]] pinned = true` in `config.toml`.
   - **Commands:** `:pin-pane [id]`, `:unpin-pane [id]`, `:toggle-pin [id]` in command prompt and palette.
   - **Keybinding:** `<prefix> P` (Shift+P) toggles pinning on the focused column.
   - **Server Verb:** `VerbTogglePin` toggles pinning, resizes, and broadcasts updated layout.

4. **Wire Protocol:**
   - `ColumnData` gains `bool pinned = 4;`.
   - `VerbType` gains `VERB_TYPE_TOGGLE_PIN = 15;`.
   - Bump `protocol.Version` to 25.

---

## Verification Plan

- Unit tests in `internal/layout/layout_test.go`:
  - `TestPinnedColumnsInScrollStrategy`: Pinned column at left, unpinned columns scroll in remaining space.
  - `TestPinnedColumnFocusedInScrollStrategy`: Focus in pinned column keeps unpinned scroll position.
- Unit tests in `internal/layout/card_test.go`:
  - `TestPinnedColumnsInCardStrategy`: Pinned column at left, card fan in remaining space.
  - `TestPinnedColumnFocusedInCardStrategy`: Focus in pinned column leaves active unpinned card visible.
- Integration tests in `internal/server/server_test.go`:
  - `TestStartupPinnedColumns`: Startup config with pinned dashboard drawer + shell.
  - `TestVerbTogglePin`: Toggling pin updates layout snapshot and shifts column to pinned section.
- Run `make quick` and ensure clean passes.
