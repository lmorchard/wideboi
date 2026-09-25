# Notes: Dev Session 2026-09-24-1705-issue-195-tui-visual-design

## Context & Objectives
- Addressed GitHub Issue #195: "TUI: Refine status bars, dividers, and visual hierarchy".
- Established a unified, scannable visual language across the status bar, pane headers, column dividers, and card slivers.
- Reinforced meaning with standard ANSI 16 basic colors (adapting to both light and dark terminal backgrounds) and `NO_COLOR` environment support, with optional `[theme]` configuration in TOML.

## Key Decisions & Architecture
1. **Bracketed Fixed-Slot Badge Grammar:**
   - Format: `[<focus_slot> <id> <status_slot>]`
   - Focus indicator: `●` when focused, ` ` (space) when unfocused.
   - Status indicators: `»` (Working), `!` (Needs Input), `✓` (Done), `✗` (Failed), or ` ` (space when Idle).
   - Invariant width: Empty spaces represent absent indicators so that badge widths never jump or jitter on focus moves or state transitions (e.g. `[● 1  ]` -> `[  1  ]` or `[● 1 »]`).
2. **Restrained Semantic Colors (ANSI 16):**
   - Working (`»`): Cyan
   - Needs Input (`!`): Yellow (Bold)
   - Done (`✓`): Green
   - Failed (`✗`): Red (Bold)
   - Focus (`●`): Bright Cyan (Bold)
   - Dividers: Focused boundaries get `┃` with `FocusDivider` (bright cyan bold); inactive boundaries get `│` with `Divider` (`uv.AttrFaint` / dim).
   - Slivers: Spine `▌` is Cyan and bold when working, and dim when idle.
3. **Status Bar Hierarchy:**
   - Left side: Segmented pills for all open terminals in column order (`[● 1  ] [  2 »] [  3 ✓]`), acting as a clean, stable tab/task-switcher bar where pills do not swap positions on focus changes. Followed by scroll indicator `[scroll +N]` if the focused pane is scrolled.
   - Right side: Layout mode and key hints (`cards · C-b for commands`) styled as dim secondary cues.
   - Control mode: Full-row inversion (`uv.AttrReverse`) maintained for unambiguous mode signaling.
   - Autowrap boundary protection: Bounded to `c.cols - 1` to avoid ultraviolet autowrap toggle escapes.
4. **Theme Configuration:**
   - Added `ThemeConfig` to `internal/config/config.go` with TOML `[theme]` support.
   - `internal/client/theme.go` parses color names (`"cyan"`, `"yellow"`, `"green"`, `"red"`, `"dim"`, `"bold"`, etc.) and ANSI numbers 0-255.
   - Automatically disables colors when `NO_COLOR` is set or `TERM=dumb`, preserving glyphs and text formatting.
5. **Harness Alignment:**
   - Updated `scripts/smoke.py` (`FOCUS_LITERAL` and `_focus_digit_re`) to match the new `[● <id>` badge format.
   - Re-generated `testdata/golden/startup.txt` (`make golden`), reflecting the refined status bar words.

## Verification
- Automated checks:
  - Unit tests: `go test -v ./internal/client/...` and `go test -v ./internal/config/...` green.
  - Smoke tests: `python3 scripts/smoke.py` (37/37 passed).
  - Attach checks: `python3 scripts/attachcheck.py` (25/25 passed).
  - Golden comparison: wire output matches golden snapshot.
  - Full suite: `make check` passed 4 consecutive runs with zero flakes or races.
