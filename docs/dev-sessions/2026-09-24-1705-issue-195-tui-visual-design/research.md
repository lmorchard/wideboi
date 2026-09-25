# Research for issue #195: TUI visual hierarchy

## 1. Chrome Rendering Data Flow

Rendering originates in `Client.Draw` and flows into offscreen buffers, composition functions, and `uv.Screen` primitives:

1. **`Client.Draw`** (`internal/client/client.go:453-478`)
   - Acquires `c.mu.Lock()` (`client.go:454`).
   - Ensures an offscreen staging buffer `c.stagingScreen` of size `(c.cols, c.rows)` (`client.go:457-460`).
   - Calls `c.drawToScreenLocked(c.stagingScreen)` (`client.go:462`).
   - Compares staging against `c.lastRenderedScreen` (`client.go:465`); returns `false` if unchanged.
   - Copies cells to `scr` via `copyToHostScreen` (`client.go:469, 57-73`).

2. **`Client.drawToScreenLocked`** (`internal/client/client.go:480-539`)
   - If `c.layerLocked() == layerHelp`, delegates to `drawHelpOverlay` (`internal/client/help.go:77`) and returns (`client.go:481-485`).
   - Reads `st := c.frameStateLocked()` (`client.go:487`); if animating (`c.motion != nil`), interpolates placements via `c.motion.at()` (`client.go:490`).
   - Calls `focusedPlacement := c.composeFrameLocked(scr, st)` (`client.go:497`).
   - Calls `c.drawSelectionLocked(scr)` (`internal/client/mouse.go:356`, `client.go:498`).
   - Calls `c.drawStatusBarLocked(scr)` (`client.go:499`).
   - Positions host cursor on `scr` via `scr.SetCursorPosition(fx, fy)` and `scr.ShowCursor()`, or hides it via `scr.HideCursor()` (`client.go:504-538`).

3. **`Client.composeFrameLocked`** (`internal/client/client.go:554-688`)
   - Copies and sorts placements by ascending `Z` order via `sort.SliceStable` (`client.go:560-564`).
   - Iterates placements `p`:
     - **Pane Headers** (`client.go:572-602`):
       - Computes header bounds `frame := c.frameLocked(*p)` (`client.go:574, 844-850`); `headerW := frame.Dx()`.
       - Formats prefix ` [%d]` or ` %d [%d]` with `pos := st.positions[p.PaneID]` (`client.go:578-581`).
       - Appends glyph `st.paneStatuses[p.PaneID].Glyph()` if non-blank (`client.go:582-584`), title `st.paneTitles[p.PaneID]` (`client.go:585-588`), and ` ★` if focused (`client.go:589-591`).
       - Truncates to `headerW` using `compose.TruncateWidth(dst, header, headerW)` (`client.go:592`).
       - Pads with spaces to `headerW` using `compose.StringWidth` (`client.go:593-595`).
       - Writes row `0`: if focused, calls `compose.WriteStyled(dst, frame.Min.X, 0, header, uv.Style{Attrs: uv.AttrReverse})` (`client.go:598`); if unfocused, calls `compose.WriteString(dst, frame.Min.X, 0, header)` (`client.go:600`).
     - **Card Slivers** (`client.go:605-606`):
       - When `p.Kind == protocol.PlacementSliver`, calls `c.drawSliverLocked(dst, p, st)` (`client.go:705-741`).
       - Writes row `p.Dst.Min.Y` with `glyph + " " + title` via `compose.WriteStyled` (`client.go:720-724`).
       - Draws spine character `▌` for `y := p.Dst.Min.Y + 1` to `p.Dst.Max.Y - 1` at column `p.Dst.Min.X` (`client.go:738-740`).
     - **Pane Content & Scroll Footer** (`client.go:607-634`):
       - Blits mirror surface via `compose.Blit(dst, mirror.Surface, p.Dst)` (`client.go:608-610`).
       - When scrolled (`pu.ScrollOffset > 0`), draws footer bar at `footerY := p.Dst.Max.Y - 1` with `uv.Style{Attrs: uv.AttrReverse}` (`client.go:611-633`).
     - **Column Dividers** (`client.go:636-678`):
       - In scroll mode (`c.layoutMode != protocol.LayoutCards`), draws at `p.Dst.Max.X` from `y = p.Dst.Min.Y` to `p.Dst.Max.Y - 1`: `┃` if adjacent to focus or focused, else `│` (`client.go:638-657`).
       - In card mode (`c.layoutMode == protocol.LayoutCards`), draws at left border `frame.Min.X` (`┃` if focused, else `│`) (`client.go:663-671`), and at `p.Dst.Max.X` with `┃` if focused (`client.go:673-677`).
   - Draws overflow count markers `+N` on row 0 via `c.drawHiddenMarkersLocked(dst, st)` (`client.go:685, 797-812`).

4. **Status Bar** (`internal/client/client.go:821-824, 894-938`)
   - `c.drawStatusBarLocked` calls `c.statusLineLocked(c.cols - 1)` (`client.go:822`).
   - If `c.controlMode`, formats `controlHelp` menu with reverse style `uv.Style{Attrs: uv.AttrReverse}` (`client.go:898-903`).
   - Else calls `c.normalStatusLocked(budget)` with unstyled `uv.Style{}` (`client.go:905, 911-938`).
   - Writes to `y = c.rows - 1` starting at `x = 0` via `compose.WriteStyled(scr, 0, c.rows-1, statusText, statusStyle)` (`client.go:823`).

5. **`compose` package and `uv.Screen`** (`internal/client/compose/surface.go:21-74`)
   - `compose.Blit`: calls `src.Draw(dst, dest)` (`surface.go:26-28`).
   - `compose.WriteString`: calls `WriteStyled(s, x, y, text, uv.Style{})` (`surface.go:44-46`).
   - `compose.WriteStyled`: calls `uv.NewCell(s.WidthMethod(), string(r))` for each rune, sets `cell.Style = style`, invokes `s.SetCell(currX, y, cell)`, and advances `currX` by `max(1, cell.Width)` (`surface.go:59-73`).

---

## 2. Terminal Styling, Attributes, Colors, and Configuration

- **Chrome styles and attributes in use:**
  - Focused pane header: `uv.Style{Attrs: uv.AttrReverse}` (`client.go:598`).
  - Unfocused pane header: zero style `uv.Style{}` (`client.go:600`).
  - Scrollback footer: `uv.Style{Attrs: uv.AttrReverse}` (`client.go:632`).
  - Dividers (`│`, `┃`): zero style `uv.Style{}` (`client.go:655, 669, 675`).
  - Sliver top label: zero style `uv.Style{}` (`client.go:723`).
  - Sliver spine (`▌`): `uv.Style{Attrs: uv.AttrBold}` when active (`glyph == "»"`), else zero style `uv.Style{}` (`client.go:734-739`).
  - Hidden pane markers (`+N`): `uv.Style{Attrs: uv.AttrBold}` (`client.go:800, 810`).
  - Control mode status bar: `uv.Style{Attrs: uv.AttrReverse}` (`client.go:903`).
  - Normal status bar: zero style `uv.Style{}` (`client.go:905`).
  - Help overlay borders and content: `uv.Style{Attrs: uv.AttrReverse}` (`internal/client/help.go:122, 127-128, 138`).
  - Mouse selection: toggles `cc.Style.Attrs ^= uv.AttrReverse` (`internal/client/mouse.go:370`).
- **Colors:** No color fields (`Fg`, `Bg`) are configured or assigned to any chrome elements; all chrome styles set only `Attrs`.
- **Configuration and Themes:**
  - `internal/config` (`internal/config/config.go:21-44`) contains no fields or options for colors or themes.
  - No themes or color configuration files exist in the codebase.

---

## 3. Pane Status Lifecycle and Formatting

- **Definitions** (`internal/protocol/messages.go:31-56`):
  ```go
  const (
      StatusIdle PaneStatus = iota // 0
      StatusWorking               // 1
      StatusNeedsInput            // 2
      StatusDone                  // 3
      StatusFailed                // 4
  )
  ```
- **Glyph mapping** (`messages.go:43-56`):
  - `StatusWorking` -> `"»"`
  - `StatusNeedsInput` -> `"!"`
  - `StatusDone` -> `"✓"`
  - `StatusFailed` -> `"✗"`
  - `StatusIdle` (and default) -> `" "`
- **Server-side updates** (`internal/server/term/grid.go`):
  - OSC 133 sequences (`grid.go:278-295`):
    - `133;A`, `133;B` -> `StatusNeedsInput`
    - `133;C` -> `StatusWorking`
    - `133;D`, `133;D;0` -> `StatusDone`
    - `133;D;<nonzero>` -> `StatusFailed`
    - Latches `g.sawAuthoritativeStatus.Store(true)` (`grid.go:294`).
  - OSC 9;4 sequences (`grid.go:330-344`):
    - `9;4;0` -> `StatusDone`
    - `9;4;1`, `9;4;3` -> `StatusWorking`
    - `9;4;2` -> `StatusFailed`
    - `9;4;4` -> `StatusNeedsInput`
    - Latches `g.sawAuthoritativeStatus.Store(true)` (`grid.go:343`).
  - Fallback heuristic (`grid.go:455-457, 503-512`):
    - When `sawAuthoritativeStatus` is false, any `Grid.Write()` sets `StatusWorking` (`grid.go:456`).
    - After 3 seconds without writes, `Grid.Status()` decays `StatusWorking` to `StatusIdle` (`grid.go:504-511`).
- **Client storage and display**:
  - Received in `Client.HandleServerMsg` under `MsgLayoutSnapshot.PaneStatuses` and stored in `c.paneStatuses` (`client.go:189`).
  - **Headers**: Appends `" " + glyph` after pane ID if `glyph != "" && glyph != " "` (`client.go:582-584`).
  - **Slivers**: Prepends `glyph` to title on top row (`client.go:711-720`); if `glyph == "»"`, applies `uv.AttrBold` to the vertical spine `▌` (`client.go:735-739`).
  - **Status bar**: Formats each non-idle pane as `  [%d %s]` (e.g. `  [2 ✓]`) (`client.go:920-924`).

---

## 4. Test Assertions on Chrome

#### `scripts/smoke.py` (`scripts/smoke.py`)
- **Regexes & wire patterns**:
  - `CUP = re.compile(rb"\x1b\[(\d+);(\d+)H")` (`smoke.py:35`)
  - `DIVIDER = "[│┃]".encode()` (`smoke.py:36`)
  - `DIVIDER_CUP = re.compile(rb"\x1b\[(\d+);(\d+)H(?:\x1b\[[0-9;]*m)*" + DIVIDER)` (`smoke.py:44`)
  - `FOCUS_LITERAL = re.compile(rb"focus: (?:pane|\[pane) (\d+)")` (`smoke.py:51`)
  - `_focus_digit_re(status_row) = re.compile(rb"\x1b\[" + str(status_row).encode() + rb";(?:13|14)H(?:\x1b\[[0-9;]*m)*(\d+)")` (`smoke.py:54-57`)
  - `CURSOR_VIS = re.compile(rb"\x1b\[\?25(h|l)")` (`smoke.py:79`)
- **Column & position assumptions**:
  - Status row is at line `status_row = s.rows` (`smoke.py:60, 296`).
  - Diffed focus digit re-render addresses column `13` or `14` (`smoke.py:47-48, 56`).
  - Mouse click on column 95, row 5 (`"\x1b[<0;95;5M\x1b[<0;95;5m"`) (`smoke.py:297`).
  - Divider movements tracked via `divider_columns` parsing column numbers from `DIVIDER_CUP` (`smoke.py:69-71, 385-390, 399-405`).
- **Exact literals**:
  - Control mode SGR reverse check: `b"\x1b[7m"` on entry (`smoke.py:448`), `b"focus: "` and `b"\x1b[?25h"` on exit (`smoke.py:456-461`).
  - Status line substrings: `b"C-b for commands"` (`smoke.py:588`), `b"C-a for commands"` (`smoke.py:604, 769`), `b"cards"` (`smoke.py:776`), `b"scroll"` (`smoke.py:718`), `b"k kill"` (`smoke.py:608`), `b"hjel move"` (`smoke.py:610`).
  - Exclusions: asserts absence of `b"$mod"` (`smoke.py:770`) and `b"alt+"` (`smoke.py:778`).

#### `scripts/attachcheck.py` (`scripts/attachcheck.py`)
- Sets dimensions `COLS, ROWS = 80, 24` (`attachcheck.py:49`).
- Uses `focus_pane_id(out, ROWS)` from `smoke.py` (`attachcheck.py:46`).
- Asserts focus IDs match exact integers (`1`, `2`, `3`) on the status bar across process detach/reattach cycles (`attachcheck.py:261, 378, 562, 589, 702, 848-862`).

#### Unit tests in `internal/client/`
- **`cards_test.go`** (`internal/client/cards_test.go`):
  - Sliver count: checks `p.Kind == protocol.PlacementSliver` via `sliverCount` (`cards_test.go:30-34, 52, 77, 95`).
  - Header inspection: reads `image.Rect(p.Dst.Min.X, 0, p.Dst.Max.X, 1)` via `regionText`; asserts `strings.Contains(headerText, "deploy")` and `strings.Contains(headerText, protocol.StatusDone.Glyph())` (`cards_test.go:151-158`).
  - Sliver inspection: reads `image.Rect(p.Dst.Min.X, 1, p.Dst.Min.X+4, p.Dst.Max.Y)`; asserts `strings.Contains(sliverText, "CONT")` (`cards_test.go:162-165`).
  - Truncation boundaries: asserts cells outside `[20..35)` remain blank (`cards_test.go:342-351, 383-392`).
  - Overflow markers: reads row 0 `image.Rect(0, 0, cols, 1)`; asserts `"+"` presence (`cards_test.go:451-454`), and right margin offset check `header[len(header)-1-len(want) : len(header)-1] == want` for `want := fmt.Sprintf("+%d", right)` (`cards_test.go:501-504`).
  - Dividers: asserts presence of `"│"` or `"┃"` in screen dumps (`cards_test.go:595, 614`).
- **`focus_column_test.go`** (`internal/client/focus_column_test.go`):
  - Asserts header string prefixes `" 1 [5]"` and `" 2 [2]"` on row 0 (`focus_column_test.go:60-65`).
- **`screen_test.go`** (`internal/client/screen_test.go`):
  - Asserts scroll footer literals `"[▲ scroll +7/50]"` and `"[▲ scroll +7/50  ▼ new output]"`, and status line tags `"[scroll +7]"` and `"[scroll +7 ⤓]"` (`screen_test.go:332-355`).
- **`help_test.go` & `layout_tag_test.go`** (`internal/client/help_test.go`, `internal/client/layout_tag_test.go`):
  - Asserts `style.Attrs&uv.AttrReverse != 0` on control status line (`help_test.go:97`).
  - Asserts status line string literals `"cards · C-b for commands"`, `"cards"`, `"scroll · C-b for commands"`, `"scroll"` (`layout_tag_test.go:29-63`), and `"q quit"`, `"esc exit"`, `"d detach"` (`help_test.go:19-20, 60, 147`).
  - Help overlay: asserts box characters `┌`, `┐`, `└`, `┘`, `│` and `uv.AttrReverse` (`help_test.go:97, 125-139`).

---

## 5. Cell Width Budgeting, Truncation, and Autowrap Handling

- **Display Width Measurement**:
  - `compose.StringWidth(s, text)` (`surface.go:110-122`): iterates runes, obtains `uv.NewCell(s.WidthMethod(), string(r)).Width`, and sums `max(1, cw)`.
  - `runeLen(s)` (`client.go:1043`): returns `utf8.RuneCountInString(s)`.
- **Truncation Implementations**:
  1. `compose.TruncateWidth(s uv.Screen, text string, budget int) string` (`surface.go:87-108`):
     - Calculates cell widths using `s.WidthMethod()`.
     - Advances width by `max(1, cw)`.
     - Drops characters if `used + w > budget`; drops multi-column runes entirely if they would straddle the boundary.
     - Used in headers (`client.go:592`), slivers (`client.go:723`), and scrollback footers (`client.go:628`).
  2. `truncateRunes(s string, n int) string` (`client.go:1048-1057`):
     - Slices by rune indices (`[]rune(s)[:n]`).
     - Used in status bar formatting (`client.go:899, 934, 937`) and help overlay lines (`internal/client/help.go:133`).
- **Padding**:
  - Space padding (`strings.Repeat(" ", width - used)`) is appended up to the budgeted width for headers (`client.go:593-595`), footers (`client.go:629-631`), control status bar (`client.go:902`), normal status bar (`client.go:933-934`), and help overlay lines (`help.go:135-137`).
- **Autowrap and Last-Column Handling**:
  - Writing to column `cols - 1` triggers Ultraviolet's autowrap toggle sequence (`\x1b[?7l` / `\x1b[?7h`), splitting output across escape sequences (`client.go:803-805, 816-820; help.go:74-76`).
  - Chrome components budget to stop 1 cell short of `cols`:
    - Status bar: `statusLineLocked` is called with budget `c.cols - 1` (`client.go:822`), leaving column `cols - 1` untouched.
    - Right hidden marker: positioned at `x := c.cols - 1 - runeLen(s)` (`client.go:806-810`), ending at column `cols - 2`.
    - Help overlay: clamped to `maxW := cols - 1; if boxW > maxW { boxW = maxW }` (`help.go:95-97`).
