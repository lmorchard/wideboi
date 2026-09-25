# TUI Status Bar Tweaks Spec

**Goal:** Improve the TUI status bar and pane headers with non-harsh styling, keep the bottom status bar visible during control mode with hints displayed above it, and support clicking status badges to switch pane focus.

**Source:** GitHub Issue #225 and user feedback

## Current state

- **Bottom status bar & Control mode hints:**
  - Located at bottom row `y = c.rows - 1` (`internal/client/client.go:838-852`).
  - When in control mode (`c.controlMode == true`), `statusLineLocked` overwrites the entire status bar with full-width inverse text (`uv.Style{Attrs: uv.AttrReverse}`) containing the verb menu (`internal/client/client.go:845-848, 1028-1038`), hiding the pane badges and layout indicator completely.
  - Normal status bar renders open pane badges `[● id status]` and right-aligned layout mode / key hints (`internal/client/client.go:870-919`).
- **Top-of-pane header bar:**
  - Rendered at `y = 0` across each pane frame (`internal/client/client.go:586-611`).
  - When a pane is focused (`isFocus == true`), the entire 1-row header bar is styled with `uv.Style{Attrs: uv.AttrReverse}` (`internal/client/client.go:607`), which creates harsh high-contrast black-on-white text across the pane top.
  - The badge string `FormatBadge` (`[● id status]`) is drawn as plain unstyled text without the distinct status and focus colors used by the bottom bar's `writeBadgeLocked` (`internal/client/client.go:592, 921-958`).
- **Mouse clicks on status bar:**
  - `HandleMouse` (`internal/client/mouse.go:130-177`) performs `hitTestLocked(pt)` on clicks. Any click on row `c.rows - 1` fails hit testing because `pt.Y >= f.Max.Y` (`internal/client/mouse.go:35`), and the click is discarded.

## Desired end state

1. **Control mode hints positioned above status bar:**
   - When `c.controlMode == true`, control mode hints are displayed on row `y = c.rows - 2` (when `c.rows >= 3`).
   - The bottom status bar at `y = c.rows - 1` remains visible with its normal content (pane badges, scroll indicators, layout mode).
   - If `c.rows < 3` (extremely short viewport), hints fall back to row `c.rows - 1`.
2. **Visual styling of control mode hints:**
   - Displayed as a continuous bar on a subtle charcoal gray background (`Bg: ansi.IndexedColor(236)`).
   - Keycaps (e.g., `hjkl`, `n`, `w`, `x`, `a`, `?`, `d`, `q`, `esc`) are rendered with accent/bold styling (`c.theme.Focus`, e.g. bright cyan bold).
   - Action descriptions (e.g., `move`, `new`, `width`, `kill`, `attn`, `help`, `detach`, `quit`, `exit`) are rendered in dim/faint text (`uv.AttrFaint`).
   - The entire hints row is padded across the budget (`cols - 1`) with the charcoal background so it renders as a clean banner.
3. **Top-of-pane header bar styling & status capsule:**
   - Replaces the harsh full-line `AttrReverse` with a subtle charcoal gray background (`Bg: ansi.IndexedColor(236)`) for the focused pane header.
   - Unfocused pane headers use the default background with dimmed/plain title text.
   - Carries over the colored status capsule from the bottom status bar: `[` and `]` in Dim, `●` in Focus (bright cyan), `id` in normal text, and status glyph (`»`, `!`, `✓`, `✗`) in `theme.StatusStyle(status)` (cyan, yellow bold, green, red bold).
   - Pane title is displayed after the capsule, maintaining clear legibility.
4. **Clickable bottom status indicators:**
   - Left-clicking on a pane status badge `[● id status]` in the bottom status bar (`y = c.rows - 1`) switches focus to that pane immediately (`c.strip.FocusPaneID(id)`).
   - Clicking on the already-focused pane or clicking on empty space / non-badge area in the status bar is a no-op.

## Design decisions

- **Decision:** Place control mode hints on row `c.rows - 2` as a floating overlay banner while keeping row `c.rows - 1` visible.
  - **Why:** Keeps context intact while entering commands; the user can see which pane is focused and the status of other panes while deciding what command to execute.
  - **Rejected:** Replacing the status bar in place (the old behavior) which hid all pane status information.
- **Decision:** Use charcoal gray (`ansi.IndexedColor(236)`) for both the control hints row background and the focused pane top header background.
  - **Why:** Provides a subtle, modern contrast that clearly indicates active focus and mode without the visual glare/harshness of terminal inverse video.
  - **Rejected:** Full reverse video (`AttrReverse`), which is harsh and washes out color highlights.
- **Decision:** Render the individual badge components in the top header using the same colored glyph styles as `writeBadgeLocked`.
  - **Why:** Consistency across top header and bottom status bar, making pane status (idle, working, needs input, done, failed) immediately identifiable at a glance in both locations.
  - **Rejected:** Plain unstyled text or monochrome badge.
- **Decision:** Hit-test the status bar during `uv.MouseClickEvent` in `HandleMouse` without changing the pane placement rects.
  - **Why:** The status bar is chrome, not a pane placement. Keeping it separate in `HandleMouse` preserves the invariant that placements represent pane rectangles while adding a dedicated check for status bar badge hit areas.
  - **Rejected:** Adding a dummy placement for the status bar.

## Patterns to follow

- Badge component decomposition: `BadgeComponents(id, isFocus, status)` in `internal/client/theme.go:37-57`.
- Styled badge rendering: `writeBadgeLocked` in `internal/client/client.go:921-958`.
- Focus switching: `c.strip.FocusPaneID(id)`, `c.focusPaneID = c.strip.FocusedPaneID()`, `c.updatePlacementsLocked()` in `internal/client/mouse.go:172-176`.
- Terminal row and width budgeting: `budget := c.cols - 1` in `internal/client/client.go:839`.

## What we're NOT doing

- Not changing keyboard routing or binding actions in `cmd/wideboi/router.go` or `internal/keys/keys.go`.
- Not changing mouse behavior in web client (`web/`); this issue is strictly about the Go TUI client.
- Not altering the Help overlay modal (`internal/client/help.go`), which remains centered and modal.
- Not modifying scrollback or PTY data structures.

## Open questions

None. All design requirements have been clarified and verified against the codebase.
