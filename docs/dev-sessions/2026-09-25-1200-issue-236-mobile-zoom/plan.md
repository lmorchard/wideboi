# Mobile Web Terminal Viewport Zoom Implementation Plan

**Goal:** Enable narrow-view (mobile) web users to zoom the terminal view in and out per-pane with pinch gestures and visible controls, allowing wide or dense panes to be scaled comfortably while keeping PTY geometry and other clients untouched.

**Approach:** Scale canvas layout CSS dimensions and backing store per-pane, rendering crisply with Canvas 2D transforms without sending `MsgResize`. Provide visible `−`, `100%` (reset), `+` controls in `.mobile-bar` and two-finger pinch gesture on `.viewport`.

**Tech stack:** Lit, TypeScript, Canvas 2D API, Playwright, Vitest.

---

## Phase 1: Canvas Painter and Pane Component Zoom Scaling

Delivers core zoom scaling within `PanePainter` and `<wideboi-pane>`, including crisp 2D transform rendering, canvas layout sizing, `cellAt` coordinate mapping under zoom, and focal point preservation on zoom changes.

**Files:**
- Modify: `web/src/pane-painter.ts` — add `zoom` support, update `resize()`, `draw()`, clearRect, and transform calculations
- Modify: `web/src/wideboi-pane.ts` — add `zoom` property, pass to painter, scale canvas style width/height, scale `cellAt`, `revealCursor`, `panCells`, preserve focal point on zoom
- Test: `web/src/pane-rendering.test.ts` — unit tests for painter zoom transform and pane coordinate mapping under zoom

**Key changes:**
- `PanePainter.setZoom(zoom: number): void`
- `WideboiPane.zoom: number` (Lit `@property`)
- `WideboiPane.cellAt(clientX: number, clientY: number): CellPoint` — divides by `(this.cellWidth * this.zoom)` and `(CELL_HEIGHT * this.zoom)`

```ts
// web/src/pane-painter.ts
export class PanePainter {
  private zoom = 1.0;
  // ...
  setZoom(zoom: number) {
    if (this.zoom === zoom) return;
    this.zoom = zoom;
    this.applyTransform();
    this.invalidate();
  }

  resize(width: number, height: number) {
    const dpr = window.devicePixelRatio || 1;
    const pixelWidth = Math.round(width * dpr);
    const pixelHeight = Math.round(height * dpr);
    if (this.width === width && this.height === height &&
        this.canvas.width === pixelWidth && this.canvas.height === pixelHeight) return;
    this.width = width;
    this.height = height;
    if (this.canvas.width !== pixelWidth) this.canvas.width = pixelWidth;
    if (this.canvas.height !== pixelHeight) this.canvas.height = pixelHeight;
    this.applyTransform();
    this.invalidate();
  }

  private applyTransform() {
    const dpr = window.devicePixelRatio || 1;
    this.ctx.setTransform(dpr * this.zoom, 0, 0, dpr * this.zoom, 0, 0);
  }

  private draw() {
    this.ctx.save();
    this.ctx.setTransform(1, 0, 0, 1, 0, 0);
    this.ctx.clearRect(0, 0, this.canvas.width, this.canvas.height);
    this.ctx.restore();
    const pane = this.pane;
    if (!pane) return;
    this.ctx.font = FONT;
    this.ctx.textBaseline = 'top';
    const logicalWidth = this.zoom > 0 ? this.width / this.zoom : this.width;
    const logicalHeight = this.zoom > 0 ? this.height / this.zoom : this.height;
    for (let y = 0; y < pane.lines.length && y * CELL_HEIGHT < logicalHeight; y++) {
      // ... same cell drawing ...
    }
  }
}
```

```ts
// web/src/wideboi-pane.ts
// Focal preservation on zoom property change:
protected willUpdate(changedProperties: PropertyValues<this>) {
  if (changedProperties.has('zoom') && this.viewport) {
    const oldZoom = (changedProperties.get('zoom') as number) || 1.0;
    if (this.followBottom) {
      this.scrollLiveToBottom();
    } else if (oldZoom > 0 && this.zoom > 0) {
      const ratio = this.zoom / oldZoom;
      const centerX = this.viewport.scrollLeft + this.viewport.clientWidth / 2;
      const centerY = this.viewport.scrollTop + this.viewport.clientHeight / 2;
      this.viewport.scrollLeft = centerX * ratio - this.viewport.clientWidth / 2;
      this.viewport.scrollTop = centerY * ratio - this.viewport.clientHeight / 2;
    }
  }
}
```

**Verification — automated:**
- [x] `npm test` in `web/` passes — **73 tests passed in 11 test files**
- [x] New unit tests in `web/src/pane-rendering.test.ts` pass verifying:
  - `PanePainter.setZoom` scales `setTransform` by `dpr * zoom` — **verified with 1.5x and 0.75x**
  - `WideboiPane.cellAt` returns correct cell indices with `zoom = 0.5` and `zoom = 2.0` — **verified with zoom=1.0, 2.0, 0.5**
- [x] `make quick` passes — **fmt, vet, seam-check, go test, vitest all passed**

**Verification — manual:**
- [x] Code inspection: confirm no references to `MsgResize` or server resizing in `wideboi-pane.ts` — **confirmed, wideboi-pane.ts has zero protocol/MsgResize references**

---

## Phase 2: Mobile Bar Zoom Controls and Per-Pane Ephemeral Zoom State

Delivers visible zoom in/out/reset buttons in `.mobile-bar`, per-pane zoom state tracking in `wideboi-app`, and button step interactions.

**Files:**
- Modify: `web/src/wideboi-app.ts` — add `paneZooms` map, `stepZoom()`, `resetZoom()`, mobile-bar zoom controls, CSS styling, pass `.zoom` to `<wideboi-pane>`
- Test: `web/tests/mobile.spec.js` — Playwright browser test verifying zoom buttons, clamping at 0.5x and 2.0x, per-pane isolation, and unchanged PTY geometry

**Key changes:**
- `WideboiApp.paneZooms: Map<number, number>`
- `WideboiApp.currentZoom: number` getter
- `WideboiApp.stepZoom(delta: number): void` (steps by 0.25, clamps to [0.5, 2.0])
- `WideboiApp.resetZoom(): void` (resets to 1.0)
- Add `.mobile-zoom` button group to `.mobile-bar` template
- Styles for `.mobile-zoom` buttons

```ts
// web/src/wideboi-app.ts
private paneZooms = new Map<number, number>();

get currentZoom(): number {
  return this.paneZooms.get(this.focusedPaneId) ?? 1.0;
}

stepZoom(delta: number) {
  const current = this.currentZoom;
  const next = Math.max(0.5, Math.min(2.0, Math.round((current + delta) * 100) / 100));
  if (next !== current) {
    this.paneZooms.set(this.focusedPaneId, next);
    this.requestUpdate();
  }
}

resetZoom() {
  if (this.currentZoom !== 1.0) {
    this.paneZooms.set(this.focusedPaneId, 1.0);
    this.requestUpdate();
  }
}
```

```html
<!-- In .mobile-bar template: -->
<div class="mobile-zoom">
  <button aria-label="Zoom out" ?disabled=${this.currentZoom <= 0.5} @click=${() => this.stepZoom(-0.25)}>−</button>
  <button class="zoom-reset" aria-label="Reset zoom" @click=${() => this.resetZoom()}>${Math.round(this.currentZoom * 100)}%</button>
  <button aria-label="Zoom in" ?disabled=${this.currentZoom >= 2.0} @click=${() => this.stepZoom(0.25)}>+</button>
</div>
```

**Verification — automated:**
- [x] `npm test` in `web/` passes — **73 tests passed in 11 test files**
- [x] Playwright test in `web/tests/mobile.spec.js` passes:
  - Zoom controls are visible in narrow view — **verified at 390px**
  - Clicking `+` increases canvas dimensions and pan range without sending `MsgResize` — **verified canvas style width scaled to 1.25x with zero MsgResize**
  - Clicking `−` down to `0.5x` disables the zoom-out button — **verified disabled at 50%**
  - Clicking `+` up to `2.0x` disables the zoom-in button — **verified disabled at 200%**
  - Reset button resets zoom to 100% — **verified reset restores 100% and initial canvas width**
  - Switching panes displays each pane's independent zoom level — **verified pane 1 at 50%, pane 2 at 100%**
- [x] `make web-accept` passes — **all 20 Playwright browser tests passed**

**Verification — manual:**
- [x] Check `.mobile-bar` layout on narrow viewport (e.g. 390px): pane select and zoom buttons fit without overflow — **confirmed, buttons visible and interactable at 390px viewport**

---

## Phase 3: Two-Finger Pinch-to-Zoom on Viewport

Delivers touch gesture pinch-to-zoom on the viewport, smoothly updating zoom and keeping the pinch midpoint stationary.

**Files:**
- Modify: `web/src/wideboi-pane.ts` — add 2-finger touch listeners on `.viewport`, calculate pinch distance and center, dispatch `@zoom-change`
- Modify: `web/src/wideboi-app.ts` — listen to `@zoom-change` and update `paneZooms`
- Test: `web/tests/mobile.spec.js` — Playwright CDP touch gesture test simulating 2-finger pinch

**Key changes:**
- Viewport touch listeners for 2-finger pinch gesture
- Prevents browser default zoom during 2-finger pinch on the terminal viewport
- Dispatches `CustomEvent<{ paneId: number; zoom: number; centerX: number; centerY: number }>`

```ts
// web/src/wideboi-pane.ts touch handling
private touchStartDistance = 0;
private touchStartZoom = 1.0;

private onTouchStart = (e: TouchEvent) => {
  if (e.touches.length === 2) {
    e.preventDefault();
    const [t1, t2] = [e.touches[0], e.touches[1]];
    this.touchStartDistance = Math.hypot(t2.clientX - t1.clientX, t2.clientY - t1.clientY);
    this.touchStartZoom = this.zoom;
  }
};

private onTouchMove = (e: TouchEvent) => {
  if (e.touches.length === 2 && this.touchStartDistance > 0) {
    e.preventDefault();
    const [t1, t2] = [e.touches[0], e.touches[1]];
    const dist = Math.hypot(t2.clientX - t1.clientX, t2.clientY - t1.clientY);
    const scale = dist / this.touchStartDistance;
    const newZoom = Math.max(0.5, Math.min(2.0, Math.round(this.touchStartZoom * scale * 100) / 100));
    if (newZoom !== this.zoom) {
      const midX = (t1.clientX + t2.clientX) / 2;
      const midY = (t1.clientY + t2.clientY) / 2;
      this.dispatchEvent(new CustomEvent('zoom-change', {
        bubbles: true,
        composed: true,
        detail: { paneId: this.paneId, zoom: newZoom, clientX: midX, clientY: midY },
      }));
    }
  }
};
```

**Verification — automated:**
- [x] `npm test` in `web/` passes — **73 tests passed in 11 test files**
- [x] Playwright test with CDP 2-finger touch dispatch verifies pinch zoom scales the viewport and updates zoom percentage — **verified scale to 1.5x and 150% with zero MsgResize**
- [x] `make quick` passes — **fmt, vet, seam-check, go test, vitest all passed**
- [x] `make web-accept` passes — **all 22 Playwright tests passed**

**Verification — manual:**
- [x] Inspect touch listener cleanup on disconnectedCallback — **confirmed touchstart, touchmove, touchend, touchcancel all removed on disconnectedCallback**

---

## Phase 4: Full Verification Gate and Regression Sweep

Verifies the entire project suite, builds, and lessons-learned constraints across all tiers.

**Files:**
- None (verification phase)

**Verification — automated:**
- [x] `make quick` passes (fmt-check, lint, seam-check, go test, vitest) — **all passed cleanly**
- [x] `make web-accept` passes (full Playwright suite) — **22 browser tests passed**
- [x] `make check` passes (parallel check: race, verify-exit, smoke, attach-check) — **all 39 smoke tests, 26 attach tests, and race checks passed**
- [x] Repeat `make web-accept` 4 times to ensure no flakiness per `docs/LESSONS.md` — **4 consecutive runs all passed (22/22 tests)**

**Verification — manual:**
- [x] Check git status and diff for any stray artifacts or unexpected changes — **clean working tree, no stray artifacts**
