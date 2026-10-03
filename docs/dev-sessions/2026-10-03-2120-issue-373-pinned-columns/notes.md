# Notes: Pinned Columns (Issue #373)

- 2026-10-03: Session started in worktree `/home/lmorchard/devel/wideboi-issue-373` on branch `feat/issue-373-pinned-columns`.
- Updated wire protocol: added `VerbTogglePin` to `VerbType` enum, added `Pinned` boolean to `ColumnData` (protobuf field 4), bumped `protocol.Version` to 25.
- Implemented pinned column layout in `internal/layout/`:
  - `ScrollStrategy.ComputePlacements`: anchored pinned columns at the left (`0..pinnedWidth`) and scrolled unpinned columns within the remainder (`[pinnedWidth, viewportWidth]`).
  - `CardStrategy.ComputePlacements`: anchored pinned columns at the left and fanned unpinned columns within the remaining space.
  - Added `PinColumn`, `UnpinColumn`, `TogglePinColumn`, `reorderPinnedColumns`, and partitioned boundary checks in `MoveLeft`/`MoveRight`.
- Added configuration and server support:
  - `StartupPane.Pinned` in `config` and `server`.
  - `VerbTogglePin` handler in server `handleVerbLocked`.
  - `:pin-pane`, `:unpin-pane`, `:toggle-pin` commands in `commands.DefaultRegistry`.
  - `<prefix> P` (Shift+P) keybinding for `ActionNameTogglePin`.
- Added automated test coverage in `layout_test.go`, `card_test.go`, `config_test.go`, and `server_test.go`.
- All tests and `make quick` passed cleanly.
