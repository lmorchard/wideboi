import type { MsgPaneUpdate } from './gen/internal/protocol/wirepb/wideboi_pb';
import { decodeColor } from './colors';
import { termSettings, type CellPoint } from './pane-state';
import type { RenderStats } from './stats';
import { getTheme, type Theme } from './themes';

// A pane paints only its own cells. CSS positions and clips its canvas.
export class PanePainter {
  private readonly ctx: CanvasRenderingContext2D;
  private pane?: MsgPaneUpdate;
  private focused = false;
  private selection?: { start: CellPoint; end: CellPoint };
  private width = 0;
  private height = 0;
  private zoom = 1.0;
  private theme: Theme;
  private frame: number | null = null;
  private running = false;
  private readonly onVisibilityChange = () => {
    if (document.hidden) this.cancelFrame();
    else this.invalidate();
  };

  // stats is only set with ?stats=1; when undefined no timing calls are made.
  constructor(private readonly canvas: HTMLCanvasElement, private cellWidth: number,
              private readonly stats?: RenderStats, theme?: Theme) {
    const ctx = canvas.getContext('2d');
    if (!ctx) throw new Error('Could not get 2d context');
    this.ctx = ctx;
    this.theme = theme ?? getTheme('dark');
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

  setTheme(theme: Theme) {
    if (this.theme === theme || this.theme.id === theme.id) return;
    this.theme = theme;
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

  setZoom(zoom: number) {
    if (this.zoom === zoom) return;
    this.zoom = zoom;
    this.applyTransform();
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
    this.applyTransform();
    if (this.running && !document.hidden) {
      this.draw();
    } else {
      this.invalidate();
    }
  }

  private applyTransform() {
    const dpr = window.devicePixelRatio || 1;
    this.ctx.setTransform(dpr * this.zoom, 0, 0, dpr * this.zoom, 0, 0);
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
    const logicalWidth = this.zoom > 0 ? this.width / this.zoom : this.width;
    const logicalHeight = this.zoom > 0 ? this.height / this.zoom : this.height;
    ctx.clearRect(0, 0, logicalWidth, logicalHeight);
    const pane = this.pane;
    if (!pane) return;
    ctx.font = termSettings.font;
    ctx.textBaseline = 'top';
    for (let y = 0; y < pane.lines.length && y * termSettings.cellHeight < logicalHeight; y++) {
      const line = pane.lines[y]?.cells;
      if (!line) continue;
      // LineData has one entry per terminal column. A wide glyph's
      // continuation occupies the next entry; Width is paint width only.
      for (let x = 0; x < line.length && x * this.cellWidth < logicalWidth; x++) {
        const cell = line[x];
        if (x > 0 && line[x - 1]?.width > 1) continue;
        const attrs = cell.style?.attrs ?? 0;
        const reverse = (attrs & 32) !== 0;
        const normalBg = decodeColor(cell.style?.bg, true, this.theme);
        const normalFg = decodeColor(cell.style?.fg, false, this.theme);
        const bg = reverse ? normalFg : normalBg;
        const fg = reverse ? normalBg : normalFg;
        const px = x * this.cellWidth;
        const py = y * termSettings.cellHeight;
        if (bg !== this.theme.terminal.background) {
          ctx.fillStyle = bg;
          ctx.fillRect(px, py, this.cellWidth * (cell.width || 1), termSettings.cellHeight);
        }
        if (cell.content && cell.content !== ' ' && !(attrs & 64)) {
          ctx.fillStyle = fg;
          ctx.globalAlpha = attrs & 2 ? 0.5 : 1;
          ctx.font = `${attrs & 4 ? 'italic ' : ''}${attrs & 1 ? 'bold ' : ''}${termSettings.font}`;
          ctx.fillText(cell.content, px, py + 1);
          ctx.globalAlpha = 1;
        }
        if (cell.style?.underline) {
          ctx.fillStyle = cell.style.underlineColor?.kind ? decodeColor(cell.style.underlineColor, false, this.theme) : fg;
          ctx.fillRect(px, py + termSettings.cellHeight - 2, this.cellWidth, 1);
        }
        if (attrs & 128) {
          ctx.fillStyle = fg;
          ctx.fillRect(px, py + termSettings.cellHeight / 2, this.cellWidth, 1);
        }
        if (this.selection) {
          let { start: a, end: b } = this.selection;
          if (a.y > b.y || (a.y === b.y && a.x > b.x)) [a, b] = [b, a];
          if ((y > a.y || (y === a.y && x >= a.x)) &&
              (y < b.y || (y === b.y && x <= b.x))) {
            ctx.fillStyle = this.theme.terminal.selection;
            ctx.fillRect(px, py, this.cellWidth * Math.max(cell.width || 1, 1), termSettings.cellHeight);
          }
        }
      }
    }
    if (pane.cursorVisible && this.focused) {
      const px = pane.cursorX * this.cellWidth;
      const py = pane.cursorY * termSettings.cellHeight;
      if (px < logicalWidth && py < logicalHeight) {
        ctx.fillStyle = this.theme.terminal.cursor;
        ctx.fillRect(px, py, this.cellWidth, termSettings.cellHeight);
        const cell = pane.lines[pane.cursorY]?.cells[pane.cursorX];
        if (cell?.content && cell.content !== ' ') {
          ctx.fillStyle = this.theme.terminal.cursorText;
          ctx.font = termSettings.font;
          ctx.fillText(cell.content, px, py + 1);
        }
      }
    }
    if (pane.scrollOffset > 0 && logicalHeight > termSettings.cellHeight) {
      const footerY = Math.floor(logicalHeight / termSettings.cellHeight) * termSettings.cellHeight - termSettings.cellHeight;
      ctx.fillStyle = this.theme.terminal.scrollFooterBg;
      ctx.fillRect(0, footerY, logicalWidth, termSettings.cellHeight);
      ctx.fillStyle = this.theme.terminal.scrollFooterFg;
      ctx.font = 'bold 12px monospace';
      let footerText = ` [▲ scroll +${pane.scrollOffset}/${pane.scrollbackLen}`;
      if (pane.unreadOutput) {
        footerText += '  ▼ new output]';
      } else {
        footerText += ']';
      }
      ctx.fillText(footerText, 0, footerY);
      ctx.font = termSettings.font;
    }
  }
}
