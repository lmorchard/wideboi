import { create } from '@bufbuild/protobuf';
import type { PlacementData } from './protocol';
import {
  LineDataSchema, type MsgLayoutSnapshot, type MsgPanePatch, type MsgPaneUpdate,
} from './gen/internal/protocol/wirepb/wideboi_pb';
import { decodeColor } from './colors';
import { reconcileFocus } from './focus';

export class GridRenderer {
  private canvas: HTMLCanvasElement;
  private ctx: CanvasRenderingContext2D;
  private cellWidth = 0;
  private cellHeight = 0;

  private panes = new Map<number, MsgPaneUpdate>();
  private layout: MsgLayoutSnapshot | null = null;
  private focusedPaneId = 0;
  private placements: PlacementData[] = [];
  private scrollX = 0;
  private selection?: { paneID: number; start: { x: number; y: number }; end: { x: number; y: number } };
  
  private animationFrameId: number | null = null;
  private running = false;
  private readonly onVisibilityChange = () => {
    if (document.hidden) {
      this.cancelFrame();
    } else {
      this.invalidate();
    }
  };

  constructor(canvas: HTMLCanvasElement) {
    this.canvas = canvas;
    const ctx = canvas.getContext('2d');
    if (!ctx) throw new Error('Could not get 2d context');
    this.ctx = ctx;

    this.measureFont();
  }

  public start() {
    if (this.running) return;
    this.running = true;
    document.addEventListener('visibilitychange', this.onVisibilityChange);
    this.invalidate();
  }

  public stop() {
    this.running = false;
    document.removeEventListener('visibilitychange', this.onVisibilityChange);
    this.cancelFrame();
  }

  private cancelFrame() {
    if (this.animationFrameId === null) return;
    cancelAnimationFrame(this.animationFrameId);
    this.animationFrameId = null;
  }

  private invalidate() {
    if (!this.running || document.hidden || this.animationFrameId !== null) return;
    this.animationFrameId = requestAnimationFrame(() => {
      this.animationFrameId = null;
      if (this.running && !document.hidden) this.draw();
    });
  }

  public handleLayoutSnapshot(snapshot: MsgLayoutSnapshot) {
    this.focusedPaneId = reconcileFocus(this.layout?.columns || [], snapshot.columns, this.focusedPaneId);
    this.layout = snapshot;
    this.recomputePlacements();
    this.invalidate();
  }

  public setFocusedPaneId(paneID: number) {
    if (this.layout?.columns.some(c => c.paneId === paneID)) {
      this.focusedPaneId = paneID;
      this.recomputePlacements();
      this.invalidate();
    }
  }

  public handlePaneUpdate(update: MsgPaneUpdate) {
    this.panes.set(update.paneId, update);
    this.invalidate();
  }

  // Generations are bigint (protobuf-es ignores jstype = JS_NUMBER); they
  // are only ever compared with each other.
  public handlePanePatch(patch: MsgPanePatch): boolean {
    const base = this.panes.get(patch.paneId);
    if (!base || base.generation !== patch.baseGeneration ||
        base.cols !== patch.cols || base.rows !== patch.rows ||
        base.lines.length !== base.rows || patch.generation <= patch.baseGeneration) {
      this.panes.delete(patch.paneId);
      this.invalidate();
      return false;
    }
    const lines = base.lines.slice();
    const seen = new Set<number>();
    for (const row of patch.changedRows) {
      if (row.y < 0 || row.y >= base.rows || seen.has(row.y) || row.cells.length !== base.cols) {
        this.panes.delete(patch.paneId);
        this.invalidate();
        return false;
      }
      seen.add(row.y);
      lines[row.y] = create(LineDataSchema, { cells: row.cells });
    }
    // Cursor and mouse fields always overwrite: an absent field is false.
    this.panes.set(patch.paneId, {
      ...base, lines, generation: patch.generation,
      cursorX: patch.cursorX, cursorY: patch.cursorY,
      cursorVisible: patch.cursorVisible, mouseTracking: patch.mouseTracking,
    });
    this.invalidate();
    return true;
  }

  public handlePaneClosed(paneID: number) {
    this.panes.delete(paneID);
    if (this.selection?.paneID === paneID) this.selection = undefined;
    this.invalidate();
  }

  public mouseTracking(paneID: number): boolean {
    return this.panes.get(paneID)?.mouseTracking ?? false;
  }

  public setSelection(paneID: number, start: { x: number; y: number }, end: { x: number; y: number }) {
    this.selection = { paneID, start, end };
    this.invalidate();
  }

  public clearSelection() {
    this.selection = undefined;
    this.invalidate();
  }

  public selectionText(): string {
    const sel = this.selection;
    const pane = sel && this.panes.get(sel.paneID);
    if (!sel || !pane) return '';
    let a = sel.start, b = sel.end;
    if (a.y > b.y || (a.y === b.y && a.x > b.x)) [a, b] = [b, a];
    const rows: string[] = [];
    for (let y = a.y; y <= b.y; y++) {
      const line = pane.lines[y]?.cells || [];
      const start = y === a.y ? a.x : 0;
      const end = y === b.y ? b.x : line.length - 1;
      let text = '';
      for (let x = start; x <= end; x++) {
        const cell = line[x];
        if (!cell) continue;
        let continuation = false;
        for (let back = 1; back <= 3 && x - back >= 0; back++) {
          if (line[x - back]?.width > back) { continuation = true; break; }
        }
        if (!continuation) text += cell.content || ' ';
      }
      rows.push(text.trimEnd());
    }
    return rows.join('\n');
  }

  public resize(width: number, height: number) {
    const dpr = window.devicePixelRatio || 1;
    this.canvas.width = width * dpr;
    this.canvas.height = height * dpr;
    this.canvas.style.width = `${width}px`;
    this.canvas.style.height = `${height}px`;
    
    this.ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    this.measureFont();
    this.recomputePlacements();
    this.invalidate();
  }

  
  public pixelsToCells(clientX: number, clientY: number): { x: number, y: number, cols: number, rows: number } {
    const rect = this.canvas.getBoundingClientRect();
    
    const localX = clientX - rect.left;
    const localY = clientY - rect.top;
    
    return {
      x: Math.floor(localX / this.cellWidth),
      y: Math.floor(localY / this.cellHeight),
      cols: Math.floor(rect.width / this.cellWidth),
      rows: Math.floor(rect.height / this.cellHeight)
    };
  }

  public getPaneHit(cellX: number, cellY: number): { paneID: number, placement?: PlacementData } {
    if (!this.layout) return { paneID: 0 };

    // Check placements in reverse z-order (front to back)
    for (let i = this.placements.length - 1; i >= 0; i--) {
        const p = this.placements[i];
        if (p.Kind !== 0) continue; // Skip slivers for focus targets

        if (cellX >= p.Dst.Min.X && cellX < p.Dst.Max.X && 
            cellY >= p.Dst.Min.Y && cellY < p.Dst.Max.Y) {
            return { paneID: p.PaneID, placement: p };
        }
    }
    return { paneID: 0 };
  }

	public getFocusedPaneId(): number {
		return this.focusedPaneId;
	}

	public getGridSize(): { cols: number, rows: number } {
    if (this.cellWidth === 0 || this.cellHeight === 0) return { cols: 80, rows: 24 };
    
    const width = parseInt(this.canvas.style.width || "0", 10);
    const height = parseInt(this.canvas.style.height || "0", 10);
    
    return {
      cols: Math.floor(width / this.cellWidth),
      rows: Math.floor(height / this.cellHeight)
    };
  }

  private measureFont() {
    this.ctx.font = '14px monospace';
    this.ctx.textBaseline = 'top';
    const metrics = this.ctx.measureText('W'); // Monospace, so W is fine
    this.cellWidth = Math.max(metrics.width, 1);
    this.cellHeight = 14 * 1.2; // approx line height
  }
  
  // Match layout.ScrollStrategy's focus visibility and source crop rules.
  private recomputePlacements() {
    if (!this.layout) return;
    const { cols: width, rows } = this.getGridSize();
    const columns = this.layout.columns || [];
    if (!columns.length || width <= 0 || rows <= 0) {
      this.placements = [];
      return;
    }
    const height = Math.max(rows - 2, 1);
    const positions: number[] = [];
    let x = 0;
    for (const column of columns) {
      positions.push(x);
      x += column.width + 1;
    }
    const focus = Math.max(columns.findIndex(c => c.paneId === this.focusedPaneId), 0);
    const focusX = positions[focus];
    const focusWidth = columns[focus].width;
    if (focusX < this.scrollX) this.scrollX = focusX;
    else if (focusX + focusWidth > this.scrollX + width) {
      this.scrollX = focusX + focusWidth - width;
    }
    this.placements = columns.flatMap((column, index): PlacementData[] => {
      const paneX = positions[index] - this.scrollX;
      const left = Math.max(paneX, 0);
      const right = Math.min(paneX + column.width, width);
      if (left >= right) return [];
      return [{
        PaneID: column.paneId,
        Src: { Min: { X: left - paneX, Y: 0 }, Max: { X: right - paneX, Y: height } },
        Dst: { Min: { X: left, Y: 1 }, Max: { X: right, Y: 1 + height } },
        Z: 0, Kind: 0
      }];
    });
  }

  private draw() {
    this.ctx.fillStyle = '#1e1e1e';
    this.ctx.fillRect(0, 0, this.canvas.width, this.canvas.height);

    if (!this.layout) return;

    for (const p of this.placements) {
      this.drawPlacement(p);
    }
    const grid = this.getGridSize();
    const focused = this.focusedPaneId;
    const title = this.layout.paneTitles[focused] || `Pane ${focused}`;
    this.ctx.fillStyle = '#cccccc';
    this.ctx.font = '14px monospace';
    this.ctx.fillText(title.slice(0, grid.cols), 0, 0);
    const status = this.layout.columns.map(c =>
      `[${c.paneId}] ${this.layout?.paneStatuses[c.paneId] || ''}`).join(' ');
    this.ctx.fillText(status.slice(0, grid.cols), 0, (grid.rows - 1) * this.cellHeight);
  }

  private drawPlacement(p: PlacementData) {
    const pane = this.panes.get(p.PaneID);
    if (!pane) return;

    const srcMinX = p.Src.Min.X;
    const srcMinY = p.Src.Min.Y;
    const dstMinX = p.Dst.Min.X;
    const dstMinY = p.Dst.Min.Y;
    const dx = p.Dst.Max.X - p.Dst.Min.X;
    const dy = p.Dst.Max.Y - p.Dst.Min.Y;

    this.ctx.save();
    
    this.ctx.beginPath();
    this.ctx.rect(
      dstMinX * this.cellWidth, 
      dstMinY * this.cellHeight, 
      dx * this.cellWidth, 
      dy * this.cellHeight
    );
    this.ctx.clip();

    this.ctx.font = '14px monospace';
    this.ctx.textBaseline = 'top';

    const offsetX = dstMinX - srcMinX;
    const offsetY = dstMinY - srcMinY;

    for (let y = 0; y < pane.lines.length; y++) {
      const screenY = y + offsetY;
      if (screenY < dstMinY || screenY >= dstMinY + dy) continue;

      const line = pane.lines[y]?.cells;
      if (!line) continue;
      
      // LineData has one entry per terminal column. A wide glyph's
      // continuation occupies the next entry; Width is paint width only.
      for (let x = 0; x < line.length; x++) {
        const cell = line[x];
        if (x > 0 && line[x - 1]?.width > 1) continue;
        const screenX = x + offsetX;
        
        if (screenX >= dstMinX && screenX < dstMinX + dx) {
          // style and its colours are absent when zero: absence is default.
          const attrs = cell.style?.attrs ?? 0;
          const reverse = (attrs & 32) !== 0;
          const normalBg = decodeColor(cell.style?.bg, true);
          const normalFg = decodeColor(cell.style?.fg, false);
          const bg = reverse ? normalFg : normalBg;
          const fg = reverse ? normalBg : normalFg;
          
          if (bg !== '#1e1e1e') {
            this.ctx.fillStyle = bg;
            this.ctx.fillRect(
              screenX * this.cellWidth, 
              screenY * this.cellHeight, 
              this.cellWidth * (cell.width || 1), 
              this.cellHeight
            );
          }

          if (cell.content && cell.content !== ' ' && !(attrs & 64)) {
            this.ctx.fillStyle = fg;
            this.ctx.globalAlpha = attrs & 2 ? 0.5 : 1;
            this.ctx.font = `${attrs & 4 ? 'italic ' : ''}${attrs & 1 ? 'bold ' : ''}14px monospace`;
            this.ctx.fillText(
              cell.content, 
              screenX * this.cellWidth, 
              screenY * this.cellHeight
            );
            this.ctx.globalAlpha = 1;
          }
          const lineColor = decodeColor(cell.style?.underlineColor, false);
          if (cell.style?.underline) {
            this.ctx.fillStyle = cell.style.underlineColor?.kind ? lineColor : fg;
            this.ctx.fillRect(screenX * this.cellWidth, (screenY + 1) * this.cellHeight - 2,
              this.cellWidth, 1);
          }
          if (attrs & 128) {
            this.ctx.fillStyle = fg;
            this.ctx.fillRect(screenX * this.cellWidth, screenY * this.cellHeight + this.cellHeight / 2,
              this.cellWidth, 1);
          }
          const sel = this.selection;
          if (sel?.paneID === pane.paneId) {
            let a = sel.start, b = sel.end;
            if (a.y > b.y || (a.y === b.y && a.x > b.x)) [a, b] = [b, a];
            if ((y > a.y || (y === a.y && x >= a.x)) &&
                (y < b.y || (y === b.y && x <= b.x))) {
              this.ctx.fillStyle = 'rgba(100, 160, 220, 0.45)';
              this.ctx.fillRect(screenX * this.cellWidth, screenY * this.cellHeight,
                this.cellWidth * Math.max(cell.width || 1, 1), this.cellHeight);
            }
          }
        }
      }
    }

    if (pane.cursorVisible && this.focusedPaneId === pane.paneId) {
      const curX = pane.cursorX + offsetX;
      const curY = pane.cursorY + offsetY;
      
      if (curX >= dstMinX && curX < dstMinX + dx && curY >= dstMinY && curY < dstMinY + dy) {
        this.ctx.fillStyle = '#d4d4d4';
        this.ctx.fillRect(
          curX * this.cellWidth, 
          curY * this.cellHeight, 
          this.cellWidth, 
          this.cellHeight
        );
        
        if (curY < pane.lines.length) {
            const targetCell = pane.lines[pane.cursorY]?.cells[pane.cursorX];
            if (targetCell) {
                if (targetCell && targetCell.content && targetCell.content !== ' ') {
                    this.ctx.fillStyle = '#1e1e1e';
                    this.ctx.fillText(
                        targetCell.content,
                        curX * this.cellWidth,
                        curY * this.cellHeight
                    );
                }
            }
        }
      }
    }


    this.ctx.restore();
    
    // Draw border
    const isFocused = this.focusedPaneId === pane.paneId;
    if (p.Dst.Max.X < this.getGridSize().cols) {
        this.ctx.fillStyle = '#1e1e1e';
        this.ctx.fillRect(
           (dstMinX + dx) * this.cellWidth,
           dstMinY * this.cellHeight,
           this.cellWidth,
           dy * this.cellHeight
        );
        this.ctx.fillStyle = isFocused ? '#007fd4' : '#555555';
        this.ctx.font = '14px monospace';
        for (let y = dstMinY; y < dstMinY + dy; y++) {
            this.ctx.fillText(isFocused ? '┃' : '│', (dstMinX + dx) * this.cellWidth, y * this.cellHeight);
        }
    }
    
    if (p.Kind === 1) { 

       this.ctx.fillStyle = 'rgba(255, 255, 255, 0.1)';
       this.ctx.fillRect(
           dstMinX * this.cellWidth,
           dstMinY * this.cellHeight,
           dx * this.cellWidth,
           dy * this.cellHeight
       );
       this.ctx.strokeStyle = '#555';
       this.ctx.strokeRect(
           dstMinX * this.cellWidth,
           dstMinY * this.cellHeight,
           dx * this.cellWidth,
           dy * this.cellHeight
       );
    }
  }
}
