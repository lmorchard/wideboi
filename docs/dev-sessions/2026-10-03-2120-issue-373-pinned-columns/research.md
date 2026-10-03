# Research: Pinned Columns (Issue #373)

## 1. Motivation & Problem

Wideboi currently supports two layout modes (`ScrollStrategy` and `CardStrategy`).
In both modes, every column participates in horizontal scrolling or card fanning.
When a session contains monitoring panes, dashboards (like our newly added 28-column drawer from #379/#380), or status overviews, the user has to either:
- Keep the pane focused (which consumes the whole screen in cards or centers it in scroll mode), or
- Let it scroll or fan off-screen when working in other columns.

Issue #373 proposes **Pinned columns**:
- Columns that stay anchored to the left edge of the terminal.
- Pinned columns consume terminal width statically (`pinnedWidth := sum(col.Width + 1)`).
- The remaining columns scroll (in scroll mode) or fan (in card mode) within the remaining width (`viewportWidth - pinnedWidth`).

## 2. Code Locations

- `internal/layout/layout.go`:
  - `Column`: struct with `PaneID`, `Width`, `Height`. Needs `Pinned bool`.
  - `Strip`: manages `columns []Column`, `focusIndex`. Needs pin manipulation methods (`PinColumn`, `UnpinColumn`, `TogglePinColumn`, `IsColumnPinned`, `reorderPinnedColumns`).
  - `ScrollStrategy.ComputePlacements`: needs to place pinned columns on the left and unpinned columns in the remaining viewport.
  - `HiddenCounts`: counts unplaced columns; pinned columns are always placed so they never count as hidden.
- `internal/layout/card.go`:
  - `CardStrategy.ComputePlacements`: places pinned columns at the left and fans unpinned columns within `viewportWidth - pinnedWidth`.
- `internal/protocol/`:
  - `wirepb/wideboi.proto`: Add `VERB_TYPE_TOGGLE_PIN = 15;` to `VerbType`, and `bool pinned = 4;` to `ColumnData`.
  - `messages.go`: Add `VerbTogglePin` to `VerbType`, add `Pinned bool` to `ColumnData`.
  - `codec.go`: Wire marshal/unmarshal for `Pinned` and `VerbTogglePin`.
  - `version.go`: Bump `protocol.Version` to 25.
- `internal/config/config.go`:
  - Add `Pinned bool \`toml:"pinned"\`` to `StartupPane`.
- `internal/server/`:
  - `server.go`: Add `Pinned bool` to `server.StartupPane`.
  - `handlers.go`: In `handleAttachLocked`, pin columns where `spec.Pinned == true`. In `handleVerbLocked`, handle `VerbTogglePin`.
- `internal/commands/registry.go`:
  - Add `:pin-pane`, `:unpin-pane`, `:toggle-pin` commands.
- `internal/keys/keys.go`:
  - Add `ActionNameTogglePin` bound to `"P"` (Shift+P).

## 3. Invariants & Layout Mathematics

1. **Non-overlapping destinations:** Pinned columns occupy `[x, 1, x+w, 1+availHeight]`. Unpinned columns start at `pinnedWidth` and are clipped to `[pinnedWidth, 1, viewportWidth, 1+availHeight]`. The destinations never overlap.
2. **Stable Column Partitioning:** Pinned columns are kept at the front of `s.columns` (`0..numPinned-1`). Visual left-to-right order always matches index order `0..N-1`.
3. **No-pinned fallback:** When `numPinned == 0`, `pinnedWidth == 0` and all computations are bit-for-bit identical to existing unpinned layouts.
