# TUI: Refine Status Bars, Dividers, and Visual Hierarchy Spec

**Goal:** Give wideboi's native terminal UI a clearer, more expressive visual hierarchy using consistent bracketed pill badges, restrained ANSI color accents, and distinct focus boundaries across both scrolling and card layouts.

**Source:** GitHub issue #195

## Current state

- Chrome rendering is centralized in `internal/client/client.go:480-688` (`composeFrameLocked`, `drawStatusBarLocked`, `drawSliverLocked`, `statusLineLocked`).
- Status bar (`internal/client/client.go:908-938`) outputs unstyled plain text (`focus: [pane N ★]  [2 »] ... cards · C-b for commands`). Only control mode inverts the row (`uv.AttrReverse`).
- Pane headers (`internal/client/client.go:572-602`) use ` [%d]` or ` %d [%d]`, optionally with status glyph, title, and trailing `★` if focused. The focused header row is inverted (`uv.AttrReverse`), while unfocused headers are plain unstyled text.
- Dividers (`internal/client/client.go:636-678`) draw `┃` adjacent to or at the focused pane and `│` between inactive panes, all with default unstyled attributes (`uv.Style{}`).
- Card slivers (`internal/client/client.go:705-741`) draw `glyph + " " + title` on row 0 and `▌` for the spine (bold if working `»`, plain otherwise).
- No colors (`Fg`, `Bg`) or themes are currently configured anywhere in `internal/config/` or `internal/client/` (`research.md: Section 2`).

## Desired end state

1. **Unified Badge Vocabulary across Chrome:**
   - Every pane indicator (status bar badges, header tags, sliver headers) shares a consistent compact bracketed pill format with fixed slot width (using space padding for missing indicators so pill widths never jump on state transitions):
     - Slot structure: `[<focus_slot> <id> <status_slot>]`
     - Focus slot: `●` when focused, ` ` (space) when unfocused.
     - Status slot: `»`, `!`, `✓`, `✗`, or ` ` (space) when idle.
     - Examples (for single digit ID `1`):
       - Unfocused idle: `[  1  ]` (fixed width 7)
       - Focused idle: `[● 1  ]` (fixed width 7)
       - Unfocused active: `[  1 »]`, `[  1 !]`, `[  1 ✓]`, `[  1 ✗]` (fixed width 7)
       - Focused active: `[● 1 »]`, `[● 1 !]`, `[● 1 ✓]`, `[● 1 ✗]` (fixed width 7)
   - Scroll indicator remains attached when scrolled: `[▲ +<offset>/<len>]` and `[▲ +<offset>/<len> ⤓]` when unread output exists.
2. **Restrained Color Accents (ANSI 16):**
   - Status glyphs carry semantic ANSI basic colors:
     - Working (`»`): Cyan (ANSI 6)
     - Needs Input (`!`): Yellow (ANSI 3)
     - Done (`✓`): Green (ANSI 2)
     - Failed (`✗`): Red (ANSI 1)
     - Focus dot (`●`): Bold / Bright Cyan (ANSI 14 or Bright White)
   - Dividers & Boundaries:
     - Focused boundary (`┃`): Bold / Accent color.
     - Inactive boundary (`│`): Faint / Dim (`uv.AttrFaint`).
   - Slivers:
     - Spine (`▌`): Cyan (and bold) when working, faint/dim when idle.
3. **Status Bar Hierarchy:**
   - Normal mode: Displays segmented compact pills for all open terminals in strip order across the session (e.g. `[● 1  ] [  2 »] [  3 ✓]`), followed by right-aligned layout mode and key hint separated by `·` (e.g. `cards · C-b for commands`).
   - Control mode: Full-row inversion (`uv.AttrReverse`) with essential and droppable verb hints, preserved for high mode visibility.
   - Truncation / budgeting respects the ultraviolet autowrap boundary (stopping 1 cell short of `c.cols`).
4. **Theme Configuration & Accessibility:**
   - Default: Standard ANSI 16 palette adapting automatically to light and dark terminal backgrounds.
   - `NO_COLOR` / `TERM=dumb`: When `NO_COLOR` environment variable is set, strip color escape sequences and rely strictly on glyphs, brackets, and `AttrBold`/`AttrFaint`/`AttrReverse`.
   - Optional `[theme]` section in `config.toml` allowing color overrides (e.g. `working = "cyan"`, `done = "green"`, etc.).
5. **Test Harness Compatibility:**
   - `scripts/smoke.py` and `scripts/attachcheck.py` updated to track focus via the new `[● <id>]` badge format, keeping wire tests green.

## Design decisions

- **Decision:** Use bracketed pills `[<focus> <id> <status>]` for pane identity and status with space padding for absent indicators.
  - **Why:** Self-contained, visually clean, and unambiguous. Reserving a space cell for missing focus (`●` vs ` `) or missing status (`»`/`!`/`✓`/`✗` vs ` `) ensures every badge keeps a fixed width, preventing the status bar or header from jumping horizontally on state transitions or focus shifts.
  - **Rejected:** Omitting spaces when indicators are absent (causes layout jitter as panes become active or change focus); powerline arrow glyphs (requires non-standard patched fonts); raw dot notation without brackets (lower contrast and less scannable in dense agent grids).
- **Decision:** Use standard ANSI 16 colors as defaults.
  - **Why:** Terminal emulators map ANSI 0-15 to the user's chosen palette, ensuring appropriate contrast on both dark and light terminal themes without hardcoding RGB values that may clash.
  - **Rejected:** Hardcoded 24-bit RGB truecolor as default (can be illegible on mismatched light/dark terminal backgrounds).
- **Decision:** Provide a `[theme]` configuration section in `config.toml`.
  - **Why:** Allows power users to customize colors or align wideboi with specific custom terminal aesthetics.
  - **Rejected:** Omitting config entirely (inflexible) or building a complex multi-file theming engine (overengineered).
- **Decision:** Update `smoke.py` and `attachcheck.py` regexes to accommodate the new status bar format.
  - **Why:** The test harness's regex was a historical convenience checking `focus: [pane N`; updating it to match `\[● (\d+)` reflects the intentional UI design improvement.
  - **Rejected:** Contorting the user-facing status bar UI just to keep legacy test regexes unchanged.

## Patterns to follow

- **Cell width & truncation:** Follow `compose.TruncateWidth` and `compose.StringWidth` at `internal/client/compose/surface.go:87-122` for all variable-width strings.
- **Autowrap boundary safety:** Follow the `budget := c.cols - 1` convention at `internal/client/client.go:806-810, 816-824`.
- **Config parsing:** Follow TOML unmarshaling and validation patterns in `internal/config/config.go:21-44, 71-195`.
- **Ultraviolet style composition:** Follow `uv.Style` with `ansi.BasicColor` as seen in `internal/protocol/wire.go:94-96`.

## What we're NOT doing

- **Not taking cells from child PTY content:** Placement geometry and grid sizes remain completely unchanged.
- **Not building an interactive status dashboard:** Live in-app navigation dashboard is tracked separately under #196.
- **Not adding agent metadata inspection:** OSC 7 CWD and OSC 1337 metadata display is tracked under #197.
- **Not changing mouse click hitboxes:** Clicking headers or slivers continues to route focus per `internal/client/mouse.go:78-170`.

## Open questions

- **Theme color naming in TOML:** Support standard color names (`"red"`, `"green"`, `"yellow"`, `"blue"`, `"magenta"`, `"cyan"`, `"white"`, `"default"`) and numeric ANSI indices (0-255).
  - *Default:* Parse basic color names case-insensitively with fallback to default terminal colors.
- **Dim attribute support:** `uv.AttrFaint` is standard ANSI SGR 2. If a terminal does not support faint, it renders as normal text without visual corruption.
  - *Default:* Use `uv.AttrFaint` for inactive dividers and idle badges.

## Readiness checklist

1. **Placeholder scan:** No TBD or TODO markers. Open questions have concrete defaults.
2. **Internal consistency:** Layout mode, slivers, headers, dividers, and status bar follow a single unified badge and color grammar.
3. **Scope bounded:** Explicitly separated from #196 (dashboard) and #197 (metadata); placement geometry is immutable.
4. **No load-bearing ambiguity:** Color palette, fallback behavior, badge templates, and test harness changes are explicitly detailed.
