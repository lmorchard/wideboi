# Mobile Web Terminal Viewport Zoom Spec

**Goal:** Enable narrow-view (mobile) web users to zoom the terminal view in and out per-pane with pinch gestures and visible controls, allowing wide or dense panes to be scaled comfortably while keeping PTY geometry and other clients untouched.

**Source:** GitHub Issue #236 (https://github.com/lmorchard/wideboi/issues/236)

## Current state

- On mobile (`max-width: 480px`), the web client renders one focused pane at a time (`web/src/wideboi-app.ts:233-234`).
- Cells are rendered at a fixed size: `FONT = '14px monospace'`, `CELL_HEIGHT = 16.8px`, and `cellWidth` is dynamically measured via offscreen canvas `measureText('W')` (`web/src/pane-state.ts:7-17`).
- `<wideboi-pane>` contains a `.viewport` scroll container holding the `<canvas>` (`web/src/wideboi-pane.ts:200-206`). Native touch scrolling handles horizontal and vertical panning.
- `sendResizeIfChanged()` explicitly checks `if (this.mobile) return;` (`web/src/wideboi-app.ts:681`). PTY dimensions are uncoupled from client viewport size.
- Cursor reveal (`revealCursor()`) and programmatic panning (`panCells()`) assume fixed unzoomed cell pixel metrics (`web/src/wideboi-pane.ts:104-116`).
- Cell coordinate mapping (`WideboiPane.cellAt()`) assumes `this.cellWidth` and `CELL_HEIGHT` are unscaled (`web/src/wideboi-pane.ts:186-194`).
- No zoom state or controls exist.

## Desired end state

- **Per-pane ephemeral zoom:**
  - Each pane has its own zoom level, bounded between `0.5x` and `2.0x`.
  - Default zoom is `1.0x` (100%).
  - Zoom level is ephemeral (held in client memory; resets to `1.0x` on page reload).
- **Visible Controls in `.mobile-bar`:**
  - Placed on the right side of `.mobile-bar` after the pane switcher (`‹ [select] ›`):
    - Zoom Out (`−`): decreases zoom by `0.25x` (clamped to `0.5x`). Disabled when at `0.5x`.
    - Zoom Reset (e.g. `100%`, `75%`, `125%`): clicking resets the pane to `1.0x`.
    - Zoom In (`+`): increases zoom by `0.25x` (clamped to `2.0x`). Disabled when at `2.0x`.
- **Pinch-to-zoom on Viewport:**
  - Two-finger pinch gesture on `.viewport` smoothly zooms the pane in and out within `[0.5, 2.0]`.
  - Pinch zoom anchors around the midpoint between the two touch points.
  - Two-finger touch events prevent default browser page zoom.
- **Button zoom focal behavior:**
  - When zooming via buttons, if the pane is at the bottom and following live output (`this.followBottom === true`), the bottom anchor is maintained.
  - If the pane is scrolled up / deliberately positioned, zooming preserves the center point of the visible viewport.
- **Rendering & sharpness:**
  - Canvas layout CSS size scales with zoom (`cols * cellWidth * zoom` px, `rows * CELL_HEIGHT * zoom` px).
  - Canvas backing store size scales with layout size (`width * dpr`, `height * dpr`), and `PanePainter` renders with context transform `dpr * zoom`, ensuring font glyphs are drawn crisp and sharp at any scale.
  - No blank canvas during or after zoom adjustments or viewport resizes.
- **Cell coordinate mapping & cursor reveal:**
  - `cellAt(clientX, clientY)` scales denominator by `zoom` so logical cell coordinates remain exact under any zoom and pan offset.
  - `revealCursor()` and `panCells()` account for `zoom` in pixel calculations.
- **PTY & session isolation:**
  - Zoom is entirely client-side. No `MsgResize` or session size changes are ever triggered by zooming.

## Design decisions

- **Decision: Per-pane ephemeral zoom**
  - **Why:** Confirmed with Les. Different panes may run different tools (e.g. a wide htop or diff vs a shell prompt); per-pane zoom lets the user inspect a wide pane zoomed out without shrinking their shell pane. Ephemeral storage keeps state clean across reloads without stale zoom values.
  - **Rejected:** Global shared zoom across all panes; persisting zoom in `localStorage`.
- **Decision: Zoom controls in `.mobile-bar` alongside pinch gesture**
  - **Why:** Confirmed with Les. The mobile top bar already houses pane navigation and has available horizontal space since the pane `<select>` is flexible. Visible buttons provide an accessible, single-handed alternative when pinch gestures are awkward.
  - **Rejected:** Floating pill overlay (which could obscure terminal text) or pinch-only without buttons.
- **Decision: Range `0.5x – 2.0x` with `0.25x` button steps**
  - **Why:** Confirmed with Les. 0.5x allows doubling the visible column/row density for wide tables; 2.0x provides large, readable text on high-DPI phone screens. 0.25x steps give quick, predictable adjustments (0.5x, 0.75x, 1.0x, 1.25x, 1.5x, 1.75x, 2.0x).
- **Decision: Crisp Canvas 2D transform (`setTransform(dpr * zoom, ...)`) with scaled canvas backing store**
  - **Why:** CSS transform (`transform: scale(...)`) blurs canvas bitmaps when zoomed in and pixelates them. Scaling canvas CSS dimensions and backing store while applying 2D context transform renders true vector fonts directly to the high-res bitmap, maintaining crisp text.
  - **Rejected:** CSS `transform: scale()` on `<canvas>`.
- **Decision: Follow-bottom preserved on zoom; center focal point when scrolled up**
  - **Why:** Confirmed with Les. When following active shell output, zooming should not displace the user away from new output. When inspecting earlier scrollback or specific lines, keeping the viewport center stationary keeps the content in view.

## Patterns to follow

- **Mobile bar buttons:** Follow existing button styles in `.mobile-bar button` (`web/src/wideboi-app.ts:196-204`).
- **Mobile detection & layout:** Follow `NARROW_VIEW` and `this.mobile` pattern (`web/src/wideboi-app.ts:18, 404, 464, 725-730`).
- **Canvas sizing & DPR:** Follow `PanePainter.resize()` and `ResizeObserver` pattern (`web/src/pane-painter.ts:71-85`, `web/src/wideboi-pane.ts:139-149`).
- **Follow-bottom & keyboard resilience:** Follow `followBottom` and `viewportHeight` checks in `handleScroll` and `scrollLiveToBottom` (`web/src/wideboi-pane.ts:122-135`).

## What we're NOT doing

- Not changing PTY rows/cols or sending server `MsgResize` or `claimSize` on zoom.
- Not implementing desktop toolbar zoom (desktop users resize browser or configure font).
- Not persisting zoom levels to `localStorage`.
- Not adding smooth CSS transition animations to zoom (instant responsive scaling prevents lag and canvas blurs).
- Not modifying server-side protocol or Go code.

## Open questions

None.
