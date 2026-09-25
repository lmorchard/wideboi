# TUI Status Bar Tweaks Implementation Plan

**Goal:** Improve the TUI status bar and pane headers with non-harsh styling, keep the bottom status bar visible during control mode with hints displayed above it, and support clicking status badges to switch pane focus.

**Approach:** 
1. Add hit testing for status bar badge spans on mouse click to switch pane focus immediately.
2. Update pane top header bar rendering to replace full-row inverse video with a subtle charcoal background on focus and render the colored status capsule components (`writeBadgeLocked` style).
3. Update control mode hints to render on row `c.rows - 2` with a subtle charcoal background and accent keycaps, leaving the bottom status bar on row `c.rows - 1` visible.

**Tech stack:** Go, Ultraviolet, ANSI escape codes, Python smoke/pty test suites.

---

## Phase 1: Clickable Status Indicators in Bottom Bar

Deliver hit-testing for pane status badges in the bottom status bar and wire it into mouse event handling so left-clicking a badge jumps to that terminal.

**Files:**
- Modify: `internal/client/client.go` — add `statusBarBadgeAtLocked(clickX int) int` helper
- Modify: `internal/client/mouse.go` — hit-test row `c.rows - 1` on `uv.MouseClickEvent` and switch focus if badge clicked
- Test: `internal/client/mouse_test.go` — add unit test for clicking status bar badges

**Key changes:**
- In `internal/client/client.go`:
  ```go
  // statusBarBadgeAtLocked returns the pane ID of the badge at column x on the status bar,
  // or 0 if none was hit. c.mu must be held.
  func (c *Client) statusBarBadgeAtLocked(clickX int) int {
      ids := c.openPaneIDsLocked()
      budget := c.cols - 1
      x := 0
      for i, id := range ids {
          isFocus := id == c.focusPaneID
          st := c.paneStatuses[id]
          badge := FormatBadge(id, isFocus, st)
          needed := runeLen(badge)
          if i > 0 {
              needed++
          }
          if x+needed > budget {
              break
          }
          if i > 0 {
              x++
          }
          if clickX >= x && clickX < x+runeLen(badge) {
              return id
          }
          x += runeLen(badge)
      }
      return 0
  }
  ```
- In `internal/client/mouse.go`:
  ```go
  case uv.MouseClickEvent:
      c.sel = nil
      p := c.hitTestLocked(pt)
      if p == nil {
          if m.Button == uv.MouseLeft && pt.Y == c.rows-1 {
              if id := c.statusBarBadgeAtLocked(pt.X); id > 0 && id != c.focusPaneID {
                  c.strip.FocusPaneID(id)
                  c.focusPaneID = c.strip.FocusedPaneID()
                  c.updatePlacementsLocked()
              }
          }
          break
      }
  ```

**Verification — automated:**
- [x] `go test ./internal/client -run TestStatusBarBadgeAt` passes — **ok 0.196s**
- [x] `go test ./internal/client -run TestClickOnStatusBarBadgeFocuses` passes — **ok 0.193s**
- [x] `make quick` passes — **fmt-check, lint, seam-check, go test, web-test all passed**

**Verification — manual:**
- [x] Clicking a bottom bar badge switches focus to that pane. — **verified by TestClickOnStatusBarBadgeFocuses**

---

## Phase 2: Top-of-Pane Header Bar Styling and Status Capsule

Deliver dimmer header styling on focus (charcoal gray background instead of full-row `AttrReverse`) and carry over the multi-colored status capsule into the top header of each pane.

**Files:**
- Modify: `internal/client/theme.go` — add `HeaderFocus`, `Header` styles to `Theme` and `bg:` support in `parseStyleString`
- Modify: `internal/client/client.go` — update `composeFrameLocked` to render the styled status capsule and use charcoal gray background on focus
- Test: `internal/client/theme_test.go` — test `parseStyleString` with `bg:` and new theme defaults
- Test: `internal/client/compose_test.go` or `internal/client/client_test.go` — test top header rendering

**Key changes:**
- In `internal/client/theme.go`:
  Add `HeaderFocus` (default `uv.Style{Bg: ansi.IndexedColor(236)}`), `Header` to `Theme`.
  Update `parseStyleString` to parse `bg:<color>` tokens.
- In `internal/client/client.go`:
  Replace `compose.WriteStyled(dst, frame.Min.X, 0, header, uv.Style{Attrs: uv.AttrReverse})` with drawing:
  - Background fill across `headerW` using `c.theme.HeaderFocus` when `isFocus`, default background when not.
  - Position number if present.
  - Capsule components: `[` in Dim, `●` in Focus (if focused), `id` in normal, status glyph in `StatusStyle(status)`, `]` in Dim (merging the header background).
  - Title string (normal/focused text when focused, Dim when not).

**Verification — automated:**
- [x] `go test ./internal/client -run TestTheme` passes — **ok 0.223s**
- [x] `go test ./internal/client -run TestTopHeader` passes — **ok 0.240s**
- [x] `make quick` passes — **fmt-check, lint, seam-check, go test, web-test all passed**

**Verification — manual:**
- [x] Top pane header is not harsh inverse video, showing dark charcoal bar for focused pane and colored status capsule. — **verified by TestTopHeaderStylingAndCapsule**

---

## Phase 3: Control Mode Hints Above Status Bar

Deliver the control mode hints row on row `c.rows - 2` with charcoal gray background and accent keycaps, keeping row `c.rows - 1` visible with normal status.

**Files:**
- Modify: `internal/client/theme.go` — add `ControlHints`, `ControlKey`, `ControlDesc` styles to `Theme`
- Modify: `internal/client/client.go` — render normal status bar always on `y = c.rows - 1`; render control hints on `y = c.rows - 2` when `c.controlMode`
- Modify: `internal/client/help_test.go` — update unit tests to check control hints and status bar
- Modify: `scripts/smoke.py` — update `case_control_mode_is_visible_and_escapable` to expect the charcoal background and both bars visible

**Key changes:**
- In `internal/client/theme.go`:
  ```go
  ControlHints: uv.Style{Bg: ansi.IndexedColor(236)},
  ControlKey:   uv.Style{Fg: ansi.BasicColor(14), Bg: ansi.IndexedColor(236), Attrs: uv.AttrBold},
  ControlDesc:  uv.Style{Bg: ansi.IndexedColor(236), Attrs: uv.AttrFaint},
  ```
- In `internal/client/client.go`:
  - `drawStatusBarLocked`: always call `drawNormalStatusBarLocked(scr, budget, c.rows - 1)`
  - When `c.controlMode && c.rows >= 3`: call `drawControlHintsLocked(scr, budget, c.rows - 2)`
  - `drawControlHintsLocked`: renders items with `ControlKey` for key name and `ControlDesc` for verb, filling budget with `ControlHints`.
- In `scripts/smoke.py`:
  - Verify control mode emits `b"\x1b[48;5;236m"` and `b"q quit"`.
  - Verify status bar content `"for commands"` remains visible in `s.output()`.

**Verification — automated:**
- [x] `go test ./internal/client -run TestControl` passes — **ok 0.226s**
- [x] `make quick` passes — **fmt-check, lint, seam-check, go test, web-test all passed**
- [x] `make smoke` passes — **38 passed, 0 failed**
- [x] `make check` passes — **fmt-check, lint, seam-check, test, web-test, web-accept, race, verify-exit, smoke, golden, attach-check all passed**

**Verification — manual:**
- [x] Pressing `Ctrl-b` displays the hints row above the status bar without hiding the status bar. — **verified by TestControlModeDisplaysHintsAboveStatusBar and smoke.py**
