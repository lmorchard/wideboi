import { LitElement, html, css } from 'lit';
import { customElement, property, query } from 'lit/decorators.js';
import type { MsgPaneUpdate } from './gen/internal/protocol/wirepb/wideboi_pb';
import { CELL_HEIGHT, type CellPoint } from './pane-state';
import { PanePainter } from './pane-painter';
import type { RenderStats } from './stats';

@customElement('wideboi-pane')
export class WideboiPane extends LitElement {
  static styles = css`
    :host {
      display: block;
      flex: none;
      height: 100%;
      overflow: hidden;
      box-sizing: content-box;
      border-right: var(--divider-width) solid #555;
      background: #1e1e1e;
      transition: border-color 160ms ease, box-shadow 160ms ease;
    }
    :host([focused]) {
      border-right-color: #007fd4;
      box-shadow: inset 0 2px #007fd4;
    }
    :host([card-mode]) {
      box-shadow: inset 3px 0 #777, inset 0 2px #777;
    }
    :host([card-mode][focused]) {
      box-shadow: inset 3px 0 #0e9aff, inset 0 2px #0e9aff, inset -2px 0 #0e9aff;
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
      color: #ddd;
      background: #303030;
      border-right: 1px solid #666;
      font: 12px sans-serif;
      pointer-events: none;
    }
    canvas {
      display: block;
      width: 100%;
      height: 100%;
      outline: none;
    }
    @media (prefers-reduced-motion: reduce) {
      :host { transition: none; }
    }
  `;

  @property({ type: Number }) paneId = 0;
  @property({ attribute: false }) pane?: MsgPaneUpdate;
  @property({ type: Boolean, reflect: true }) focused = false;
  @property({ type: Boolean, reflect: true, attribute: 'card-mode' }) cardMode = false;
  @property() cardLabel = '';
  @property({ type: Boolean }) running = false;
  @property({ type: Number }) cellWidth = 1;
  // Read once when the painter is created; only set with ?stats=1.
  @property({ attribute: false }) stats?: RenderStats;

  @query('canvas') private canvas!: HTMLCanvasElement;
  private painter?: PanePainter;
  private observer?: ResizeObserver;

  protected firstUpdated() {
    this.painter = new PanePainter(this.canvas, this.cellWidth, this.stats);
    this.observer = new ResizeObserver(entries => {
      for (const entry of entries) {
        this.painter?.resize(entry.contentRect.width, entry.contentRect.height);
      }
    });
    this.observer.observe(this.canvas);
    const rect = this.canvas.getBoundingClientRect();
    this.painter.resize(rect.width, rect.height);
    this.syncPainter();
  }

  protected updated() { this.syncPainter(); }

  connectedCallback() {
    super.connectedCallback();
    if (this.painter) {
      this.observer?.observe(this.canvas);
      this.syncPainter();
    }
  }

  disconnectedCallback() {
    super.disconnectedCallback();
    this.observer?.disconnect();
    this.painter?.stop();
  }

  private syncPainter() {
    if (!this.painter) return;
    this.painter.setPane(this.pane);
    this.painter.setFocused(this.focused);
    this.painter.setCellWidth(this.cellWidth);
    if (this.running) this.painter.start();
    else this.painter.stop();
  }

  cellAt(clientX: number, clientY: number): CellPoint {
    const rect = this.canvas.getBoundingClientRect();
    const cols = this.pane?.cols ?? Math.max(Math.floor(rect.width / this.cellWidth), 1);
    const rows = this.pane?.rows ?? Math.max(Math.floor(rect.height / CELL_HEIGHT), 1);
    return {
      x: Math.max(0, Math.min(cols - 1, Math.floor((clientX - rect.left) / this.cellWidth))),
      y: Math.max(0, Math.min(rows - 1, Math.floor((clientY - rect.top) / CELL_HEIGHT))),
    };
  }

  setSelection(start: CellPoint, end: CellPoint) { this.painter?.setSelection(start, end); }
  clearSelection() { this.painter?.clearSelection(); }
  focusInput() { this.canvas.focus({ preventScroll: true }); }

  render() { return html`<canvas tabindex=${this.focused ? 0 : -1}></canvas><span class="card-label">${this.cardLabel}</span>`; }
}

declare global {
  interface HTMLElementTagNameMap { 'wideboi-pane': WideboiPane; }
}
