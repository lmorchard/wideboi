# Research: Mobile Web Viewport, Canvas Rendering, and Panning

## 1. Pane Rendering, Canvas Backing Dimensions, and Font/Cell Sizing

- **Font and Cell Dimensions:**
  - Font is defined as `14px monospace` (`web/src/pane-state.ts:8`, `FONT`).
  - Logical cell height is fixed at `14 * 1.2` = `16.8px` (`web/src/pane-state.ts:7`, `CELL_HEIGHT`).
  - Cell width is measured dynamically at initialization by creating an offscreen `<canvas>`, setting `context.font = FONT`, and measuring `'W'` width: `Math.max(context.measureText('W').width, 1)` (`web/src/pane-state.ts:11-17`, `measureCellWidth()`; called at `web/src/wideboi-app.ts:705`).
  - Cell width is passed as a property down to `<wideboi-pane>` (`web/src/wideboi-app.ts:1440`, `web/src/wideboi-pane.ts:92`).

- **Canvas CSS Styling and Layout:**
  - `<wideboi-pane>` encapsulates a `.viewport` container containing `<canvas>` (`web/src/wideboi-pane.ts:200-206`).
  - Canvas CSS sizing is set inline via template bindings to the full terminal grid: `width: ${pane ? `${pane.cols * cellWidth}px` : '100%'}; height: ${pane ? `${pane.rows * CELL_HEIGHT}px` : '100%'}` (`web/src/wideboi-pane.ts:204`).
  - Canvas styles in CSS: `display: block; outline: none;` (`web/src/wideboi-pane.ts:77-80`).

- **Canvas Backing Store Dimensions:**
  - Managed by `PanePainter.resize(width, height)` (`web/src/pane-painter.ts:71-85`).
  - Multiplies layout width and height by `window.devicePixelRatio || 1` (`web/src/pane-painter.ts:72-74`).
  - Reassignment of `canvas.width` and `canvas.height` is skipped if pixel dimensions match, preventing bitmap clearing (`web/src/pane-painter.ts:75-76, 81-82`, `docs/LESSONS.md:193-200`).
  - Sets context scaling transform: `this.ctx.setTransform(dpr, 0, 0, dpr, 0, 0)` (`web/src/pane-painter.ts:83`).
  - Driven by `ResizeObserver` on `<canvas>` and `.viewport` inside `<wideboi-pane>` (`web/src/wideboi-pane.ts:139-149`).

- **Rendering Loop and Drawing:**
  - Rendering is scheduled via `requestAnimationFrame` upon pane state changes or resize (`web/src/pane-painter.ts:93-109`, `invalidate()`). Suspended when `document.hidden` (`web/src/pane-painter.ts:16-19, 94`).
  - Clears dirty region with `ctx.clearRect(0, 0, this.width, this.height)` (`web/src/pane-painter.ts:112-113`).
  - Sets `ctx.font = FONT` and `ctx.textBaseline = 'top'` (`web/src/pane-painter.ts:116-117`).
  - Iterates over `pane.lines` and `cells` within canvas pixel bounds (`web/src/pane-painter.ts:118, 123`).
  - Renders cell background colors (`web/src/pane-painter.ts:134-137`), text characters with bold/italic font variants or dim alpha (`web/src/pane-painter.ts:138-144`), underline and strikethrough decorations (`web/src/pane-painter.ts:145-152`), text selection bounds (`web/src/pane-painter.ts:153-161`), cursor block and inverted char (`web/src/pane-painter.ts:164-177`), and scrollback footer if scrolled up (`web/src/pane-painter.ts:178-192`).

## 2. Mobile Viewport Panning, Scrolling Containers, and Follow-Bottom Logic

- **Scrolling Containers and Viewport Hierarchy:**
  - The outer pane strip container is `.pane-strip` (`web/src/wideboi-app.ts:110-121, 1428`). In mobile mode, class `.pane-strip.mobile` applies `overflow: hidden;` (`web/src/wideboi-app.ts:232`).
  - In mobile mode, unfocused panes have `display: none;`, while the focused pane is given `width: 100% !important; border: 0;` (`web/src/wideboi-app.ts:233-234`).
  - Each `<wideboi-pane>` has an inner scrolling element: `<div class="viewport">` (`web/src/wideboi-pane.ts:51-57, 201-205`).
  - `.viewport` CSS: `width: 100%; height: 100%; overflow-y: auto; scrollbar-width: thin; overscroll-behavior-y: contain;` (`web/src/wideboi-pane.ts:51-57`).
  - `.viewport` inline horizontal overflow style: `overflow-x: ${pane && pane.cols > displayCols ? 'auto' : 'hidden'}` (`web/src/wideboi-pane.ts:201`).
  - The inner canvas is larger than the viewport when terminal columns or rows exceed the viewport size (`web/src/wideboi-pane.ts:204`).

- **Touch Handling:**
  - Pointer events on `.pane-strip` explicitly ignore mobile mode: `pointerdown`, `pointermove`, and `pointerup` check `if (this.mobile) return;` (`web/src/wideboi-app.ts:1112, 1130, 1144`). Wheel events also check `if (this.mobile) return;` (`web/src/wideboi-app.ts:1176`).
  - Native browser touch scrolling handles panning on `.viewport` in both horizontal and vertical axes (`web/src/wideboi-pane.ts:51-57, 201`).
  - Tapping a pane on mobile fires a `'click'` listener on `.pane-strip` (`web/src/wideboi-app.ts:1166-1173`) which focuses the clicked pane and focuses the mobile compose textarea.

- **Follow-Bottom Logic:**
  - Follow-bottom state is maintained per-pane via `this.followBottom = true` (`web/src/wideboi-pane.ts:101`).
  - Detected on `.viewport` scroll: `this.followBottom = this.viewport.scrollHeight - this.viewport.clientHeight - this.viewport.scrollTop < 2` (`web/src/wideboi-pane.ts:130`).
  - Guard across viewport height resizes: if `this.viewport.clientHeight !== this.viewportHeight`, the scroll event was triggered by an element height change (e.g. mobile keyboard toggling) rather than user scroll. It updates `this.viewportHeight` and calls `this.scrollLiveToBottom()` without clobbering `this.followBottom` (`web/src/wideboi-pane.ts:122-129`).
  - `scrollLiveToBottom()`: if `this.followBottom` is true, sets `this.viewport.scrollTop = this.viewport.scrollHeight` (`web/src/wideboi-pane.ts:133-135`). Called during `firstUpdated` (line 153), `updated` (line 159), and `ResizeObserver` callbacks (line 145).

- **Horizontal and Vertical Pan:**
  - Vertical pan: native scroll of `.viewport` (`web/src/wideboi-pane.ts:54`).
  - Horizontal pan: native scroll of `.viewport` (`web/src/wideboi-pane.ts:201`).
  - Manual programmatic horizontal pan: `panCells(cells)` sets `this.viewport.scrollLeft = Math.max(0, this.viewport.scrollLeft + cells * this.cellWidth)` (`web/src/wideboi-pane.ts:104-106`).
  - Cursor reveal: `revealCursor()` checks if `pane.cursorX * cellWidth` is outside `[scrollLeft, scrollLeft + clientWidth]` and adjusts `scrollLeft` to bring the cursor into view (`web/src/wideboi-pane.ts:108-116`). Triggered after keyboard input, paste, or sending mobile drafts (`web/src/wideboi-app.ts:618-622, 1034-1035, 1094-1095, 1253-1254, 1277-1278`).

## 3. Mobile/Narrow View Mode Detection and UI Control Structure

- **Detection:**
  - Query string: `const NARROW_VIEW = '(max-width: 480px)'` (`web/src/wideboi-app.ts:18`).
  - MatchMedia instance: `private readonly narrowMedia = window.matchMedia(NARROW_VIEW)` (`web/src/wideboi-app.ts:404`).
  - State property: `@state() private mobile = this.narrowMedia.matches` (`web/src/wideboi-app.ts:464`).
  - Listener: `narrowMedia.addEventListener('change', syncWidth)` toggles `this.mobile` and calls `this.syncVisibleHeight()` (`web/src/wideboi-app.ts:725-730`).
  - CSS media query: `@media (max-width: 480px)` hides `.toolbar` and `.title`, and displays `.mobile-bar` and `.mobile-dock` as `flex` (`web/src/wideboi-app.ts:235-240`).

- **Control Structure:**
  - **Top Navigation Bar (`.mobile-bar`):**
    - Rendered conditionally in template when connected (`web/src/wideboi-app.ts:1413-1423`).
    - CSS styling: `padding: 0.35rem; background: #252526;` (`web/src/wideboi-app.ts:187-204`).
    - Previous pane button (`‹`): `@click=${() => this.moveMobilePane(-1)}`, disabled when at first pane (`web/src/wideboi-app.ts:1414-1415, 1242-1246`).
    - Pane `<select>` dropdown: lists all active panes, bound to `handlePaneSelect` (`web/src/wideboi-app.ts:1416-1420, 1234-1240`).
    - Next pane button (`›`): `@click=${() => this.moveMobilePane(1)}`, disabled when at last pane (`web/src/wideboi-app.ts:1421-1422, 1242-1246`).
  - **Desktop Toolbar (`.toolbar`):** Hidden via `@media (max-width: 480px)`.
  - **Bottom Dock (`.mobile-dock`):**
    - Draft textarea and Send button (`web/src/wideboi-app.ts:1516-1520`).
    - 6-column mobile key grid (`web/src/wideboi-app.ts:1522-1538`).

## 4. Pointer/Mouse Events and Coordinate Mapping

- **Coordinate Mapping to Terminal Cells (`cellAt`):**
  - Implemented in `WideboiPane.cellAt(clientX, clientY)` (`web/src/wideboi-pane.ts:186-194`):
    ```ts
    const rect = this.canvas.getBoundingClientRect();
    const cols = this.pane?.cols ?? Math.max(Math.floor(rect.width / this.cellWidth), 1);
    const rows = this.pane?.rows ?? Math.max(Math.floor(rect.height / CELL_HEIGHT), 1);
    return {
      x: Math.max(0, Math.min(cols - 1, Math.floor((clientX - rect.left) / this.cellWidth))),
      y: Math.max(0, Math.min(rows - 1, Math.floor((clientY - rect.top) / CELL_HEIGHT))),
    };
    ```
  - Note: `rect.left` and `rect.top` are from `this.canvas.getBoundingClientRect()`. If zoom alters canvas scale or pixel size, the effective cell width in client coordinates becomes `this.cellWidth * zoom` (or `rect.width / cols`), and cell height becomes `CELL_HEIGHT * zoom` (or `rect.height / rows`).

## 5. Client State Persistence, Reconnection, and Resizing Without MsgResize

- **Persisted State:** Only `wideboi.prefix` in `localStorage`. Zoom level is currently not tracked or persisted.
- **MsgResize Guard:** `sendResizeIfChanged()` explicitly guards `if (this.mobile) return;`. It never transmits `MsgResize` when in mobile view (`web/src/wideboi-app.ts:681`).
- Visual viewport resizes (`window.visualViewport`) only adjust `--app-height` CSS custom property (`web/src/wideboi-app.ts:731-742`).
