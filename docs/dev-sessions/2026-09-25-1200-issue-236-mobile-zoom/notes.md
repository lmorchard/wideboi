# Notes: Issue 236 - Mobile web zoom the terminal viewport while panning

- Worktree: `.worktrees/issue-236-mobile-zoom` (branch `issue-236-mobile-zoom`)
- Base commit: `dd0563f`

## Completed Work

- **Phase 1: Canvas Painter and Pane Component Zoom Scaling**
  - Added `zoom` support to `PanePainter`: scaling canvas context transform (`ctx.setTransform(dpr * zoom, 0, 0, dpr * zoom, 0, 0)`) for crisp vector font rendering directly to the high-DPI backing store.
  - Added `@property({ type: Number }) zoom = 1.0` to `<wideboi-pane>`.
  - Scaled canvas style width/height by `zoom`.
  - Updated `WideboiPane.cellAt` to correctly map touch/pointer coordinates to logical terminal cells across any zoom level.
  - Preserved follow-bottom when at the live bottom, and preserved viewport center when zoomed while scrolled up.
  - Updated `revealCursor()` and `panCells()` to account for `zoom`.

- **Phase 2: Mobile Bar Zoom Controls and Per-Pane Ephemeral Zoom State**
  - Added `paneZooms` in-memory map to `WideboiApp`. Zoom is per-pane and ephemeral (resets to 100% on reload).
  - Added `−`, `100%` (reset), and `+` buttons to `.mobile-bar`.
  - Bounded zoom between `0.5x` (50%) and `2.0x` (200%) in `0.25x` steps, disabling buttons when at limits.
  - Verified zero `MsgResize` messages are sent to server when zooming.

- **Phase 3: Two-Finger Pinch-to-Zoom on Viewport**
  - Added two-finger pinch touch event listeners (`touchstart`, `touchmove`, `touchend`, `touchcancel`) on `.viewport` with `{ passive: false }`.
  - Handled pinch gestures anchoring around touch midpoint, calling `e.preventDefault()` to stop browser page zoom while leaving single-finger panning unaffected.
  - Dispatches `zoom-change` event to update `wideboi-app` state.
  - Verified touch cleanup on `disconnectedCallback`.

- **Phase 4: Full Verification Gate**
  - `make quick` passed.
  - `make web-accept` passed 4 consecutive runs without flakiness (22/22 Playwright tests).
  - `make check` passed (all smoke, attach, exit-code, and race tests).

## Copilot Review Rounds

- **Round 1:**
  - Cleared canvas before returning when `pane` is undefined in `PanePainter.draw()`, preventing stale output when a patch fails.
  - Updated horizontal overflow condition to `this.pane.cols * this.zoom > this.displayCols ? 'auto' : 'hidden'` so zoomed-in content remains scrollable.
- **Round 2:**
  - Re-registered touch pinch listeners in `connectedCallback()` so detaching and re-attaching panes does not disable gestures.
  - Added test coverage in `mobile.spec.js` asserting viewport center anchoring when scrolled up vs bottom-anchoring when at live output.
  - Copilot reported 0 findings / all comments resolved.

## Review Tuning & Bug Fixes

- **Capped Zoom-Out Floor:**
  - Replaced arbitrary 0.5x minimum zoom with `minZoomForPane(paneId)` dynamically computed to cap zoom-out to revealing the entire terminal (`Math.min(viewWidth / termWidth, viewHeight / termHeight)`).
  - Stops zoom-out as soon as all terminal rows and columns fit on screen.
- **Pinch-to-Zoom Zero-Blanking:**
  - During active 2-finger touchmove gestures, applied GPU-accelerated CSS `transform: scale()` directly on the canvas without reallocating backing store dimensions or clearing the bitmap on every touchmove event.
  - On `touchend`, commits the final zoom and re-rasterizes at the crisp final resolution.
  - In `PanePainter.resize`, synchronously invokes `this.draw()` immediately when canvas dimensions change so the buffer is never displayed blank.
- **Top Cut-Off Elimination:**
  - Added vertical centering (`py + 1`) to character rendering in `PanePainter.draw()` to give ascenders breathing room from the top edge.
  - Removed inset box-shadow from `.pane-strip.mobile wideboi-pane` so no 2px focus shadow covers row 0.
  - In `WideboiPane.updated()`, when canvas height/width fits in the viewport, automatically sets `scrollTop = 0` and `scrollLeft = 0` so the top of the terminal is never cut off.

- **Default Zoom-Out on Mobile & Zero Vertical Scroll:**
  - Fresh terminals on mobile now default to `minZoom` (zoomed all the way out), immediately revealing the full terminal grid, row 0, and prompt without arbitrary centering into blank canvas.
  - Reset button toggles between `minZoom` (overview) and `100%`.
  - Replaced `Math.round` with `Math.floor` and a 2px margin in `minZoomForPane` so canvas height is strictly `<= viewport.clientHeight`, eliminating the ~1 line of vertical scroll when fully zoomed out.
- **Pinch-Release Jump Elimination:**
  - Stored unzoomed content coordinates under the touch midpoint (`pinchOriginContentX`, `pinchOriginContentY`) across the pinch gesture.
  - On `touchend`, sets `pendingScrollAfterZoom` targeting those exact coordinates under the fingers, eliminating any canvas jumping when lifting fingers.
- **Merge Conflicts Resolution:**
  - Rebased cleanly onto `origin/main` commit `da6e6e3` (protocol v12, mobile macros, direct input mode).

- **Controls Placement & Order:**
  - In `.mobile-bar`, moved the Next pane button (`›`) to the right of the zoom controls: `‹ [select] [−] [100%] [+] ›`.
  - Moved `.mobile-bar` and desktop `.toolbar` to the bottom of the browser, making the terminal canvas completely flush with the top of the browser window.
  - Moved the mobile keystroke buttons (`.mobile-keys` and `.mobile-ctrl-palette`) from under the input entry row into a dedicated section in the slide-up macros sheet, saving ~85-130px of vertical space in the mobile dock.
  - Tapping terminal keystroke buttons inside the macros sheet sends input without dismissing the sheet.
  - Made Edit and ✕ (close) buttons in `.mobile-macros-header` larger touch targets (40px min-height, 44px min-width).
  - Decoupled horizontal and vertical focal anchoring in `WideboiPane.updated()` so horizontal centering is never blocked by vertical fit.
  - Reset zoom action resets to max zoomed out (`minZoom` overview) from any zoomed-in level, and toggles to 100% when clicked from `minZoom`.

