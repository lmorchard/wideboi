import type { MsgPaneUpdate } from './gen/internal/protocol/wirepb/wideboi_pb';
import { decodeColor } from './colors';
import { CELL_HEIGHT, FONT, type CellPoint } from './pane-state';
import type { RenderStats } from './stats';

// A pane paints only its own cells. CSS positions and clips its canvas.
export class PanePainter {
  private readonly ctx: CanvasRenderingContext2D;
  private pane?: MsgPaneUpdate;
  private focused = false;
  private selection?: { start: CellPoint; end: CellPoint };
  private width = 0;
  private height = 0;
  private frame: number | null = null;
  private running = false;
  private readonly onVisibilityChange = () => {
    if (document.hidden) this.cancelFrame();
    else this.invalidate();
  };

  // stats is only set with ?stats=1; when undefined no timing calls are made.
  constructor(private readonly canvas: HTMLCanvasElement, private cellWidth: number,
              private readonly stats?: RenderStats) {
    const ctx = canvas.getContext('2d');
    if (!ctx) throw new Error('Could not get 2d context');
    this.ctx = ctx;
  }

  start() {
    if (this.running) return;
    this.running = true;
    document.addEventListener('visibilitychange', this.onVisibilityChange);
    this.invalidate();
  }

  stop() {
    this.running = false;
    document.removeEventListener('visibilitychange', this.onVisibilityChange);
    this.cancelFrame();
  }

  setPane(pane: MsgPaneUpdate | undefined) {
    if (this.pane === pane) return;
    this.pane = pane;
    this.invalidate();
  }

  setFocused(focused: boolean) {
    if (this.focused === focused) return;
    this.focused = focused;
    this.invalidate();
  }

  setCellWidth(width: number) {
    if (this.cellWidth === width) return;
    this.cellWidth = width;
    this.invalidate();
  }

  setSelection(start: CellPoint, end: CellPoint) {
    this.selection = { start, end };
    this.invalidate();
  }

  clearSelection() {
    if (!this.selection) return;
    this.selection = undefined;
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
    // Assigning a backing dimension clears the bitmap, even if unchanged.
    // CSS controls canvas size; never write its inline width or height here.
    if (this.canvas.width !== pixelWidth) this.canvas.width = pixelWidth;
    if (this.canvas.height !== pixelHeight) this.canvas.height = pixelHeight;
    this.ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    this.invalidate();
  }

  private cancelFrame() {
    if (this.frame === null) return;
    cancelAnimationFrame(this.frame);
    this.frame = null;
  }

  private invalidate() {
    if (!this.running || document.hidden || this.frame !== null) return;
    this.frame = requestAnimationFrame(() => {
      this.frame = null;
      if (!this.running || document.hidden) return;
      const stats = this.stats;
      if (!stats) {
        this.draw();
        return;
      }
      // Measures issuing the 2D canvas calls on the main thread, not
      // rasterisation or compositing, which happen later.
      const start = performance.now();
      this.draw();
      stats.recordDraw(performance.now() - start);
    });
  }

  private draw() {
    const ctx = this.ctx;
    ctx.clearRect(0, 0, this.width, this.height);
    const pane = this.pane;
    if (!pane) return;
    ctx.font = FONT;
    ctx.textBaseline = 'top';
    for (let y = 0; y < pane.lines.length && y * CELL_HEIGHT < this.height; y++) {
      const line = pane.lines[y]?.cells;
      if (!line) continue;
      // LineData has one entry per terminal column. A wide glyph's
      // continuation occupies the next entry; Width is paint width only.
      for (let x = 0; x < line.length && x * this.cellWidth < this.width; x++) {
        const cell = line[x];
        if (x > 0 && line[x - 1]?.width > 1) continue;
        const attrs = cell.style?.attrs ?? 0;
        const reverse = (attrs & 32) !== 0;
        const normalBg = decodeColor(cell.style?.bg, true);
        const normalFg = decodeColor(cell.style?.fg, false);
        const bg = reverse ? normalFg : normalBg;
        const fg = reverse ? normalBg : normalFg;
        const px = x * this.cellWidth;
        const py = y * CELL_HEIGHT;
        if (bg !== '#1e1e1e') {
          ctx.fillStyle = bg;
          ctx.fillRect(px, py, this.cellWidth * (cell.width || 1), CELL_HEIGHT);
        }
        if (cell.content && cell.content !== ' ' && !(attrs & 64)) {
          ctx.fillStyle = fg;
          ctx.globalAlpha = attrs & 2 ? 0.5 : 1;
          ctx.font = `${attrs & 4 ? 'italic ' : ''}${attrs & 1 ? 'bold ' : ''}${FONT}`;
          ctx.fillText(cell.content, px, py);
          ctx.globalAlpha = 1;
        }
        if (cell.style?.underline) {
          ctx.fillStyle = cell.style.underlineColor?.kind ? decodeColor(cell.style.underlineColor, false) : fg;
          ctx.fillRect(px, py + CELL_HEIGHT - 2, this.cellWidth, 1);
        }
        if (attrs & 128) {
          ctx.fillStyle = fg;
          ctx.fillRect(px, py + CELL_HEIGHT / 2, this.cellWidth, 1);
        }
        if (this.selection) {
          let { start: a, end: b } = this.selection;
          if (a.y > b.y || (a.y === b.y && a.x > b.x)) [a, b] = [b, a];
          if ((y > a.y || (y === a.y && x >= a.x)) &&
              (y < b.y || (y === b.y && x <= b.x))) {
            ctx.fillStyle = 'rgba(100, 160, 220, 0.45)';
            ctx.fillRect(px, py, this.cellWidth * Math.max(cell.width || 1, 1), CELL_HEIGHT);
          }
        }
      }
    }
    if (pane.cursorVisible && this.focused) {
      const px = pane.cursorX * this.cellWidth;
      const py = pane.cursorY * CELL_HEIGHT;
      if (px < this.width && py < this.height) {
        ctx.fillStyle = '#d4d4d4';
        ctx.fillRect(px, py, this.cellWidth, CELL_HEIGHT);
        const cell = pane.lines[pane.cursorY]?.cells[pane.cursorX];
        if (cell?.content && cell.content !== ' ') {
          ctx.fillStyle = '#1e1e1e';
          ctx.font = FONT;
          ctx.fillText(cell.content, px, py);
        }
      }
    }
    if (pane.scrollOffset > 0 && this.height > CELL_HEIGHT) {
      const footerY = Math.floor(this.height / CELL_HEIGHT) * CELL_HEIGHT - CELL_HEIGHT;
      ctx.fillStyle = '#333333';
      ctx.fillRect(0, footerY, this.width, CELL_HEIGHT);
      ctx.fillStyle = '#ffffff';
      ctx.font = 'bold 12px monospace';
      let footerText = ` [▲ scroll +${pane.scrollOffset}/${pane.scrollbackLen}`;
      if (pane.unreadOutput) {
        footerText += '  ▼ new output]';
      } else {
        footerText += ']';
      }
      ctx.fillText(footerText, 0, footerY);
      ctx.font = FONT;
    }
  }
}
