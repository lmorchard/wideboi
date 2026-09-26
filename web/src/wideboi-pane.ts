import { LitElement, html, css, type PropertyValues } from 'lit';
import { customElement, property, query } from 'lit/decorators.js';
import type { MsgPaneUpdate } from './gen/internal/protocol/wirepb/wideboi_pb';
import { termSettings, type CellPoint } from './pane-state';
import { PanePainter } from './pane-painter';
import type { RenderStats } from './stats';
import { getTheme, type Theme } from './themes';

@customElement('wideboi-pane')
export class WideboiPane extends LitElement {
  static styles = css`
    :host {
      display: block;
      position: relative;
      flex: none;
      height: 100%;
      overflow: hidden;
      box-sizing: content-box;
      border-right: var(--divider-width) solid var(--wb-border-divider, #555);
      background: var(--wb-bg-pane, #1e1e1e);
      transition: border-color 160ms ease, box-shadow 160ms ease;
    }
    :host::after {
      content: '';
      position: absolute;
      top: 0;
      left: 0;
      right: 0;
      bottom: 0;
      pointer-events: none;
      transition: box-shadow 160ms ease;
    }
    :host([focused]) {
      border-right-color: var(--wb-focus, #007fd4);
      box-shadow: inset 0 2px var(--wb-focus, #007fd4);
    }
    :host([focused])::after {
      box-shadow: inset 0 2px var(--wb-focus, #007fd4);
    }
    :host([card-mode]) {
      box-shadow: inset 3px 0 var(--wb-card-border, #b8b8b8), inset 0 2px var(--wb-card-border, #b8b8b8);
    }
    :host([card-mode])::after {
      box-shadow: inset 3px 0 var(--wb-card-border, #b8b8b8), inset 0 2px var(--wb-card-border, #b8b8b8);
    }
    :host([card-mode][focused]) {
      box-shadow: inset 3px 0 var(--wb-card-border-focus, #0e9aff), inset 0 2px var(--wb-card-border-focus, #0e9aff), inset -2px 0 var(--wb-card-border-focus, #0e9aff);
    }
    :host([card-mode][focused])::after {
      box-shadow: inset 3px 0 var(--wb-card-border-focus, #0e9aff), inset 0 2px var(--wb-card-border-focus, #0e9aff), inset -2px 0 var(--wb-card-border-focus, #0e9aff);
    }
    .viewport {
      width: 100%;
      height: 100%;
      overflow-y: auto;
      scrollbar-width: thin;
      overscroll-behavior-y: contain;
    }
    .card-label { display: none; }
    :host([card-mode]:not([focused])) .card-label {
      display: block;
      position: absolute;
      z-index: 1;
      top: 0;
      left: 0;
      max-height: 100%;
      padding: 4px 2px;
      box-sizing: border-box;
      writing-mode: vertical-rl;
      overflow: hidden;
      white-space: nowrap;
      color: var(--wb-card-label-fg, #ddd);
      background: var(--wb-card-label-bg, #303030);
      border-right: 1px solid var(--wb-card-border, #b8b8b8);
      font: 12px sans-serif;
      pointer-events: none;
    }
    canvas {
      display: block;
      outline: none;
    }
    @media (prefers-reduced-motion: reduce) {
      :host, :host::after { transition: none; }
    }
  `;

  @property({ type: Number }) paneId = 0;
  @property({ attribute: false }) pane?: MsgPaneUpdate;
  @property({ type: Boolean, reflect: true }) focused = false;
  @property({ type: Boolean, reflect: true, attribute: 'card-mode' }) cardMode = false;
  @property() cardLabel = '';
  @property({ type: Boolean }) running = false;
  @property({ type: Number }) cellWidth = 1;
  @property({ type: Number }) displayCols = 0;
  @property({ type: Number }) zoom = 1.0;
  @property({ type: Number }) minZoom = 0.5;
  // Read once when the painter is created; only set with ?stats=1.
  @property({ attribute: false }) stats?: RenderStats;
  @property({ attribute: false }) theme: Theme = getTheme('dark');

  @query('canvas') private canvas!: HTMLCanvasElement;
  @query('.viewport') private viewport!: HTMLDivElement;
  private painter?: PanePainter;
  private observer?: ResizeObserver;
  private followBottom = true;
  private viewportHeight = 0;
  private touchStartDistance = 0;
  private touchStartZoom = 1.0;
  private touchMidpoint?: { clientX: number; clientY: number };
  private pinching = false;
  private pinchCurrentZoom = 1.0;
  private pinchOriginContentX = 0;
  private pinchOriginContentY = 0;
  private pendingScrollAfterZoom?: { left: number; top: number };

  panCells(cells: number) {
    this.viewport.scrollLeft = Math.max(0, this.viewport.scrollLeft + cells * this.cellWidth * this.zoom);
  }

  revealCursor() {
    if (!this.pane) return;
    const charWidth = this.cellWidth * this.zoom;
    const x = this.pane.cursorX * charWidth;
    const width = this.viewport.clientWidth;
    if (x < this.viewport.scrollLeft) this.viewport.scrollLeft = x;
    else if (x + charWidth > this.viewport.scrollLeft + width) {
      this.viewport.scrollLeft = x + charWidth - width;
    }
  }

  get hasVerticalOverflow(): boolean {
    return this.viewport.scrollHeight > this.viewport.clientHeight + 1;
  }

  private onViewportScroll() {
    // A keyboard-driven viewport resize can fire scroll before ResizeObserver.
    // Preserve the user's previous follow-bottom choice across that resize.
    if (this.viewport.clientHeight !== this.viewportHeight) {
      this.viewportHeight = this.viewport.clientHeight;
      this.scrollLiveToBottom();
      return;
    }
    this.followBottom = this.viewport.scrollHeight - this.viewport.clientHeight - this.viewport.scrollTop < 2;
  }

  private scrollLiveToBottom() {
    if (this.followBottom && this.viewport.scrollHeight > this.viewport.clientHeight) {
      this.viewport.scrollTop = this.viewport.scrollHeight;
    }
  }

  private onTouchStart = (e: TouchEvent) => {
    if (e.touches.length === 2) {
      e.preventDefault();
      const [t1, t2] = [e.touches[0], e.touches[1]];
      this.touchStartDistance = Math.hypot(t2.clientX - t1.clientX, t2.clientY - t1.clientY);
      this.touchStartZoom = this.zoom;
      this.pinching = true;
      this.pinchCurrentZoom = this.zoom;
      const rect = this.viewport.getBoundingClientRect();
      const midX = (t1.clientX + t2.clientX) / 2 - rect.left;
      const midY = (t1.clientY + t2.clientY) / 2 - rect.top;
      this.pinchOriginContentX = (this.viewport.scrollLeft + midX) / this.zoom;
      this.pinchOriginContentY = (this.viewport.scrollTop + midY) / this.zoom;
      this.touchMidpoint = { clientX: midX, clientY: midY };
      this.canvas.style.transformOrigin = `${this.viewport.scrollLeft + midX}px ${this.viewport.scrollTop + midY}px`;
    } else {
      this.touchStartDistance = 0;
      this.pinching = false;
      this.touchMidpoint = undefined;
    }
  };

  private onTouchMove = (e: TouchEvent) => {
    if (e.touches.length === 2 && this.touchStartDistance > 0) {
      e.preventDefault();
      const [t1, t2] = [e.touches[0], e.touches[1]];
      const dist = Math.hypot(t2.clientX - t1.clientX, t2.clientY - t1.clientY);
      const scale = dist / this.touchStartDistance;
      const newZoom = Math.max(this.minZoom, Math.min(2.0, Math.round(this.touchStartZoom * scale * 100) / 100));
      this.pinchCurrentZoom = newZoom;
      const visualScale = newZoom / this.zoom;
      this.canvas.style.transform = `scale(${visualScale})`;
      const rect = this.viewport.getBoundingClientRect();
      this.touchMidpoint = {
        clientX: (t1.clientX + t2.clientX) / 2 - rect.left,
        clientY: (t1.clientY + t2.clientY) / 2 - rect.top,
      };
    }
  };

  private onTouchEnd = (e: TouchEvent) => {
    if (this.pinching && e.touches.length < 2) {
      this.pinching = false;
      this.touchStartDistance = 0;
      const finalZoom = this.pinchCurrentZoom;
      this.canvas.style.transform = '';
      this.canvas.style.transformOrigin = '';
      if (finalZoom !== this.zoom) {
        const midX = this.touchMidpoint?.clientX ?? (this.viewport.clientWidth / 2);
        const midY = this.touchMidpoint?.clientY ?? (this.viewport.clientHeight / 2);
        const targetScrollLeft = Math.max(0, this.pinchOriginContentX * finalZoom - midX);
        const targetScrollTop = Math.max(0, this.pinchOriginContentY * finalZoom - midY);
        this.pendingScrollAfterZoom = { left: targetScrollLeft, top: targetScrollTop };
        this.dispatchEvent(new CustomEvent('zoom-change', {
          bubbles: true,
          composed: true,
          detail: { paneId: this.paneId, zoom: finalZoom },
        }));
      }
      this.touchMidpoint = undefined;
    }
  };

  private addTouchListeners() {
    if (!this.viewport) return;
    this.viewport.addEventListener('touchstart', this.onTouchStart, { passive: false });
    this.viewport.addEventListener('touchmove', this.onTouchMove, { passive: false });
    this.viewport.addEventListener('touchend', this.onTouchEnd);
    this.viewport.addEventListener('touchcancel', this.onTouchEnd);
  }

  private removeTouchListeners() {
    if (!this.viewport) return;
    this.viewport.removeEventListener('touchstart', this.onTouchStart);
    this.viewport.removeEventListener('touchmove', this.onTouchMove);
    this.viewport.removeEventListener('touchend', this.onTouchEnd);
    this.viewport.removeEventListener('touchcancel', this.onTouchEnd);
  }

  protected   firstUpdated() {
    this.painter = new PanePainter(this.canvas, this.cellWidth, this.stats, this.theme);
    this.observer = new ResizeObserver(entries => {
      for (const entry of entries) {
        if (entry.target === this.canvas) {
          this.painter?.resize(entry.contentRect.width, entry.contentRect.height);
        }
      }
      this.scrollLiveToBottom();
      this.viewportHeight = this.viewport.clientHeight;
    });
    this.observer.observe(this.canvas);
    this.observer.observe(this.viewport);
    this.addTouchListeners();
    const rect = this.canvas.getBoundingClientRect();
    this.painter.resize(rect.width, rect.height);
    this.syncPainter();
    this.scrollLiveToBottom();
    this.viewportHeight = this.viewport.clientHeight;
  }

  protected updated(changedProperties: PropertyValues<this>) {
    if (changedProperties.has('zoom') && this.viewport) {
      const canvasHeight = (this.pane?.rows ?? 0) * termSettings.cellHeight * this.zoom;
      const canvasWidth = (this.pane?.cols ?? 0) * this.cellWidth * this.zoom;

      if (this.pendingScrollAfterZoom) {
        const { left, top } = this.pendingScrollAfterZoom;
        this.pendingScrollAfterZoom = undefined;
        this.viewport.scrollLeft = canvasWidth <= this.viewport.clientWidth ? 0 : left;
        this.viewport.scrollTop = canvasHeight <= this.viewport.clientHeight ? 0 : top;
      } else {
        const oldZoom = (changedProperties.get('zoom') as number) || 1.0;
        const ratio = oldZoom > 0 ? this.zoom / oldZoom : 1.0;

        if (canvasWidth <= this.viewport.clientWidth) {
          this.viewport.scrollLeft = 0;
        } else {
          const centerX = this.viewport.scrollLeft + this.viewport.clientWidth / 2;
          this.viewport.scrollLeft = Math.max(0, centerX * ratio - this.viewport.clientWidth / 2);
        }

        if (canvasHeight <= this.viewport.clientHeight) {
          this.viewport.scrollTop = 0;
        } else if (this.followBottom) {
          this.scrollLiveToBottom();
        } else {
          const centerY = this.viewport.scrollTop + this.viewport.clientHeight / 2;
          this.viewport.scrollTop = Math.max(0, centerY * ratio - this.viewport.clientHeight / 2);
        }
      }
    }
    this.syncPainter();
    this.scrollLiveToBottom();
  }

  connectedCallback() {
    super.connectedCallback();
    if (this.painter) {
      this.observer?.observe(this.canvas);
      this.observer?.observe(this.viewport);
      this.addTouchListeners();
      this.syncPainter();
    }
  }

  disconnectedCallback() {
    super.disconnectedCallback();
    this.observer?.disconnect();
    this.painter?.stop();
    this.removeTouchListeners();
  }

  private syncPainter() {
    if (!this.painter) return;
    this.painter.setTheme(this.theme);
    this.painter.setPane(this.pane);
    this.painter.setFocused(this.focused);
    this.painter.setCellWidth(this.cellWidth);
    this.painter.setZoom(this.zoom);
    if (this.running) this.painter.start();
    else this.painter.stop();
  }

  cellAt(clientX: number, clientY: number): CellPoint {
    const rect = this.canvas.getBoundingClientRect();
    const effectiveCellWidth = this.cellWidth * this.zoom;
    const effectiveCellHeight = termSettings.cellHeight * this.zoom;
    const cols = this.pane?.cols ?? Math.max(Math.floor(rect.width / effectiveCellWidth), 1);
    const rows = this.pane?.rows ?? Math.max(Math.floor(rect.height / effectiveCellHeight), 1);
    return {
      x: Math.max(0, Math.min(cols - 1, Math.floor((clientX - rect.left) / effectiveCellWidth))),
      y: Math.max(0, Math.min(rows - 1, Math.floor((clientY - rect.top) / effectiveCellHeight))),
    };
  }

  setSelection(start: CellPoint, end: CellPoint) { this.painter?.setSelection(start, end); }
  clearSelection() { this.painter?.clearSelection(); }
  focusInput() { this.canvas.focus({ preventScroll: true }); }

  render() { return html`
    <div class="viewport" style=${`overflow-x: ${this.pane && this.pane.cols * this.zoom > this.displayCols ? 'auto' : 'hidden'}`}
      @scroll=${this.onViewportScroll}>
      <canvas tabindex=${this.focused ? 0 : -1}
        style=${`width: ${this.pane ? `${this.pane.cols * this.cellWidth * this.zoom}px` : '100%'}; height: ${this.pane ? `${this.pane.rows * termSettings.cellHeight * this.zoom}px` : '100%'}`}></canvas>
    </div>
    <span class="card-label">${this.cardLabel}</span>`; }
}

declare global {
  interface HTMLElementTagNameMap { 'wideboi-pane': WideboiPane; }
}
