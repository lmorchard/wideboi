# Width Control Session Notes

- Branch: `issue-45-width-control`
- Worktree: `.worktrees/issue-45-width-control`
- Addressed Issue #45 (width presets & general width control)
- Created exploratory follow-up Issue #70: "Explore decoupling card width from child terminal width (horizontal panning/scrolling)"

## Implementation Summary
- Added `protocol.VerbGrowWidth` and `protocol.VerbShrinkWidth`.
- Added `Strip.GrowWidth(delta int)`, `Strip.ShrinkWidth(delta int)` (delta=10, min width floor=20) and `Strip.SetWidthPresets(presets []int)`.
- Updated `CycleWidth()` to step through configurable presets (or wrap back to the first preset).
- Server routes `VerbGrowWidth` and `VerbShrinkWidth` to strip resize and recomputes pane geometries (`resizePanesLocked`).
- Key actions `shrink_width` (`o`) and `grow_width` (`p`) added to control mode keys table. Because they are single letters, they automatically gain `ctrl+o` and `ctrl+p` repeat forms, enabling repeated sizing chords (`Ctrl-B Ctrl-O Ctrl-O ...` / `Ctrl-B Ctrl-P Ctrl-P ...`). Documented in help overlay and `config.example.toml`.
- Config support for `width_presets` in TOML config and plumbed through `cmd/wideboi`.
- Verified across all unit tests, property invariant tests, wire tests, attachcheck, exit contract checks, and pty smoke tests (including a new smoke test case for grow and shrink).
