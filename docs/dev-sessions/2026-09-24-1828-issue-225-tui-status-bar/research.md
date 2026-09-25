# Research: Issue 225 - TUI status bar tweaks

## 1. Status Bar Rendering and Row Budget

- `internal/client/client.go:494` (`drawToScreenLocked`):
  - Calls `composeFrameLocked` (line 511), `drawSelectionLocked` (line 512), `drawStatusBarLocked` (line 513).
- `internal/client/client.go:838` (`drawStatusBarLocked`):
  - Rendered at `y := c.rows - 1` with `budget := c.cols - 1` (reserves rightmost column `c.cols - 1` to prevent autowrap toggles, line 839).
  - Currently, if `c.controlMode` is true, calls `c.statusLineLocked(budget)` and writes with `uv.Style{Attrs: uv.AttrReverse}` across the entire bottom row (lines 845–848).
  - Otherwise, calls `drawNormalStatusBarLocked(scr, budget, y)` (line 851).
- `internal/client/client.go:870` (`drawNormalStatusBarLocked`):
  - Clears `x = 0..budget` with spaces (line 872).
  - Iterates over `c.openPaneIDsLocked()` (line 874).
  - Formats each badge via `FormatBadge(id, isFocus, st)` (line 879), width `runeLen(badge)`.
  - Badges separated by single space (lines 888–889).
  - Writes each badge via `c.writeBadgeLocked(scr, x, y, id, isFocus, st)` (line 891).
  - Appends scroll indicator if `ScrollOffset > 0` (lines 896–905).
  - Right-aligns layout mode & key hint (`c.layoutMode.String() · c.prefixLabel for commands`) using `c.theme.Dim` (lines 907–918).

## 2. Control Mode Tracking, Display, and Styling

- `cmd/wideboi/router.go:98–100`:
  - When prefix key (e.g. `ctrl+b`) is pressed, sets `r.control = true`.
  - Doubled prefix forwards key and resets `r.control = false` (lines 105–110).
  - Single-key actions reset `r.control = false` unless Ctrl chord is used (lines 121–125).
- `cmd/wideboi/main.go:852–853`:
  - Syncs `cli.SetControlMode(rt.control)` and `cli.SetHelpVisible(rt.help)`.
- `internal/client/client.go:1028` (`statusLineLocked`):
  - When `c.controlMode` is true, calls `controlHelp(budget, c.detachable, c.bindings)`.
  - Padded to `budget` and styled in `uv.Style{Attrs: uv.AttrReverse}` (lines 1033–1037).
- `internal/client/client.go:999` (`controlHelp`):
  - Gathers droppable and essential key groups from `keys.BarItemsFor(bindings, detachable)`.
  - Droppable: `hjkl move`, `n new`, `w width`, `x kill`, `a attn`, `? help`, `d detach`.
  - Essential: `q quit`, `esc exit`.
  - Formats as double-space separated text: `"hjkl move  n new  w width  x kill  a attn  ? help  d detach  q quit  esc exit"`.
- `scripts/smoke.py:452`:
  - Tests `b"\x1b[7m" in entered` (asserted inverse video attribute when entering control mode) and `b"q quit" in entered`.

## 3. Top-of-Pane Header Bar Rendering and Styling

- `internal/client/client.go:586–611` (`composeFrameLocked`):
  - Drawn at `Y = 0` spanning `frame := c.frameLocked(*p)` width `headerW := frame.Dx()`.
  - Uses `FormatBadge(p.PaneID, isFocus, st.paneStatuses[p.PaneID])` (line 592).
  - Constructs `header := " " + badge` or `fmt.Sprintf(" %d %s", pos, badge)` (lines 593–596).
  - Appends `title := st.paneTitles[p.PaneID]` (lines 597–600).
  - Pads to `headerW` with spaces (lines 602–604).
  - If `isFocus`:
    - Entire header row styled with `uv.Style{Attrs: uv.AttrReverse}` (line 607) — very harsh contrast.
  - Else:
    - Written plain with `compose.WriteString` (line 609).
  - Badge components (`[● id status]`) in the header are drawn as a raw string without individual colors (unlike bottom bar's `writeBadgeLocked`).

## 4. Badge Components and Theme Styles

- `internal/client/theme.go:28–57`:
  - `FormatBadge`: `[<focus> <id> <status>]`
  - Focus slot: `"●"` if focused, `" "` if not.
  - Status glyphs: `" "` (Idle), `"»"` (Working), `"!"` (NeedsInput), `"✓"` (Done), `"✗"` (Failed).
- `internal/client/theme.go:97–104`:
  - `Working`: Cyan (`ansi.BasicColor(6)`)
  - `NeedsInput`: Yellow + Bold (`ansi.BasicColor(3)` / `uv.AttrBold`)
  - `Done`: Green (`ansi.BasicColor(2)`)
  - `Failed`: Red + Bold (`ansi.BasicColor(1)` / `uv.AttrBold`)
  - `Focus`: Bright Cyan + Bold (`ansi.BasicColor(14)` / `uv.AttrBold`)
  - `Dim`: Faint (`uv.AttrFaint`)
  - `Divider`: Faint (`uv.AttrFaint`)
  - `FocusDivider`: Bright Cyan + Bold
- `internal/client/client.go:921–958` (`writeBadgeLocked`):
  - Paints `[` in `theme.Dim`, `●` in `theme.Focus`, `id` plain, status in `theme.StatusStyle(status)`, `]` in `theme.Dim`.

## 5. Mouse Hit-Testing and Event Handling

- `internal/client/mouse.go:27–40` (`hitTestLocked`):
  - Checks point against frame rectangles: `pt.X >= f.Min.X && pt.X < f.Max.X && pt.Y >= 0 && pt.Y < f.Max.Y`.
  - Bottom row `y = c.rows - 1` has `pt.Y >= f.Max.Y` (since `f.Max.Y <= c.rows - 1`), so returns `nil`.
- `internal/client/mouse.go:130–177` (`HandleMouse` on `uv.MouseClickEvent`):
  - When `p := c.hitTestLocked(pt)` is nil, mouse click is currently ignored / broken.
  - Left click on chrome (header row 0, sliver) changes focus immediately:
    ```go
    c.strip.FocusPaneID(p.PaneID)
    c.focusPaneID = c.strip.FocusedPaneID()
    c.updatePlacementsLocked()
    ```
- Status bar hit-testing:
  - If `pt.Y == c.rows - 1`: checking against badge spans `[badgeX, badgeX + badgeW)` can identify which `paneID` was clicked.
  - Clicking a badge can focus that pane directly using the same focus switch logic.
