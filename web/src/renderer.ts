import type { MsgLayoutSnapshot, MsgPanePatch, MsgPaneUpdate, PlacementData } from './protocol';
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
    this.focusedPaneId = reconcileFocus(this.layout?.Columns || [], snapshot.Columns, this.focusedPaneId);
    this.layout = snapshot;
    this.recomputePlacements();
    this.invalidate();
  }

  public setFocusedPaneId(paneID: number) {
    if (this.layout?.Columns.some(c => c.PaneID === paneID)) {
      this.focusedPaneId = paneID;
      this.recomputePlacements();
      this.invalidate();
    }
  }

  public handlePaneUpdate(update: MsgPaneUpdate) {
    this.panes.set(update.PaneID, update);
    this.invalidate();
  }

  public handlePanePatch(patch: MsgPanePatch): boolean {
    const base = this.panes.get(patch.PaneID);
    if (!base || base.Generation !== patch.BaseGeneration ||
        base.Cols !== patch.Cols || base.Rows !== patch.Rows ||
        base.Lines.length !== base.Rows || patch.Generation <= patch.BaseGeneration) {
      this.panes.delete(patch.PaneID);
      this.invalidate();
      return false;
    }
    const lines = base.Lines.slice();
    const seen = new Set<number>();
    for (const row of patch.ChangedRows ?? []) {
      if (row.Y < 0 || row.Y >= base.Rows || seen.has(row.Y) || row.Cells.length !== base.Cols) {
        this.panes.delete(patch.PaneID);
        this.invalidate();
        return false;
      }
      seen.add(row.Y);
      lines[row.Y] = row.Cells;
    }
    this.panes.set(patch.PaneID, {
      PaneID: patch.PaneID, Cols: patch.Cols, Rows: patch.Rows, Lines: lines,
      Generation: patch.Generation, CursorX: patch.CursorX, CursorY: patch.CursorY,
      CursorVisible: patch.CursorVisible, MouseTracking: patch.MouseTracking,
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
    return this.panes.get(paneID)?.MouseTracking ?? false;
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
      const line = pane.Lines[y] || [];
      const start = y === a.y ? a.x : 0;
      const end = y === b.y ? b.x : line.length - 1;
      let text = '';
      for (let x = start; x <= end; x++) {
        const cell = line[x];
        if (!cell) continue;
        let continuation = false;
        for (let back = 1; back <= 3 && x - back >= 0; back++) {
          if (line[x - back]?.Width > back) { continuation = true; break; }
        }
        if (!continuation) text += cell.Content || ' ';
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
    const columns = this.layout.Columns || [];
    if (!columns.length || width <= 0 || rows <= 0) {
      this.placements = [];
      return;
    }
    const height = Math.max(rows - 2, 1);
    const positions: number[] = [];
    let x = 0;
    for (const column of columns) {
      positions.push(x);
      x += column.Width + 1;
    }
    const focus = Math.max(columns.findIndex(c => c.PaneID === this.focusedPaneId), 0);
    const focusX = positions[focus];
    const focusWidth = columns[focus].Width;
    if (focusX < this.scrollX) this.scrollX = focusX;
    else if (focusX + focusWidth > this.scrollX + width) {
      this.scrollX = focusX + focusWidth - width;
    }
    this.placements = columns.flatMap((column, index): PlacementData[] => {
      const paneX = positions[index] - this.scrollX;
      const left = Math.max(paneX, 0);
      const right = Math.min(paneX + column.Width, width);
      if (left >= right) return [];
      return [{
        PaneID: column.PaneID,
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
    const title = this.layout.PaneTitles?.[focused] || `Pane ${focused}`;
    this.ctx.fillStyle = '#cccccc';
    this.ctx.font = '14px monospace';
    this.ctx.fillText(title.slice(0, grid.cols), 0, 0);
    const status = this.layout.Columns.map(c =>
      `[${c.PaneID}] ${this.layout?.PaneStatuses?.[c.PaneID] || ''}`).join(' ');
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

    for (let y = 0; y < pane.Lines.length; y++) {
      const screenY = y + offsetY;
      if (screenY < dstMinY || screenY >= dstMinY + dy) continue;

      const line = pane.Lines[y];
      if (!line) continue;
      
      // LineData has one entry per terminal column. A wide glyph's
      // continuation occupies the next entry; Width is paint width only.
      for (let x = 0; x < line.length; x++) {
        const cell = line[x];
        if (x > 0 && line[x - 1]?.Width > 1) continue;
        const screenX = x + offsetX;
        
        if (screenX >= dstMinX && screenX < dstMinX + dx) {
          const attrs = cell.Style?.Attrs ?? 0;
          const reverse = (attrs & 32) !== 0;
          const normalBg = decodeColor(cell.Style?.Bg, true);
          const normalFg = decodeColor(cell.Style?.Fg, false);
          const bg = reverse ? normalFg : normalBg;
          const fg = reverse ? normalBg : normalFg;
          
          if (bg !== '#1e1e1e') {
            this.ctx.fillStyle = bg;
            this.ctx.fillRect(
              screenX * this.cellWidth, 
              screenY * this.cellHeight, 
              this.cellWidth * (cell.Width || 1), 
              this.cellHeight
            );
          }

          if (cell.Content && cell.Content !== ' ' && !(attrs & 64)) {
            this.ctx.fillStyle = fg;
            this.ctx.globalAlpha = attrs & 2 ? 0.5 : 1;
            this.ctx.font = `${attrs & 4 ? 'italic ' : ''}${attrs & 1 ? 'bold ' : ''}14px monospace`;
            this.ctx.fillText(
              cell.Content, 
              screenX * this.cellWidth, 
              screenY * this.cellHeight
            );
            this.ctx.globalAlpha = 1;
          }
          const lineColor = decodeColor(cell.Style?.UnderlineColor, false);
          if (cell.Style?.Underline) {
            this.ctx.fillStyle = cell.Style.UnderlineColor?.Kind ? lineColor : fg;
            this.ctx.fillRect(screenX * this.cellWidth, (screenY + 1) * this.cellHeight - 2,
              this.cellWidth, 1);
          }
          if (attrs & 128) {
            this.ctx.fillStyle = fg;
            this.ctx.fillRect(screenX * this.cellWidth, screenY * this.cellHeight + this.cellHeight / 2,
              this.cellWidth, 1);
          }
          const sel = this.selection;
          if (sel?.paneID === pane.PaneID) {
            let a = sel.start, b = sel.end;
            if (a.y > b.y || (a.y === b.y && a.x > b.x)) [a, b] = [b, a];
            if ((y > a.y || (y === a.y && x >= a.x)) &&
                (y < b.y || (y === b.y && x <= b.x))) {
              this.ctx.fillStyle = 'rgba(100, 160, 220, 0.45)';
              this.ctx.fillRect(screenX * this.cellWidth, screenY * this.cellHeight,
                this.cellWidth * Math.max(cell.Width || 1, 1), this.cellHeight);
            }
          }
        }
      }
    }

    if (pane.CursorVisible && this.focusedPaneId === pane.PaneID) {
      const curX = pane.CursorX + offsetX;
      const curY = pane.CursorY + offsetY;
      
      if (curX >= dstMinX && curX < dstMinX + dx && curY >= dstMinY && curY < dstMinY + dy) {
        this.ctx.fillStyle = '#d4d4d4';
        this.ctx.fillRect(
          curX * this.cellWidth, 
          curY * this.cellHeight, 
          this.cellWidth, 
          this.cellHeight
        );
        
        if (curY < pane.Lines.length) {
            const targetCell = pane.Lines[pane.CursorY]?.[pane.CursorX];
            if (targetCell) {
                if (targetCell && targetCell.Content && targetCell.Content !== ' ') {
                    this.ctx.fillStyle = '#1e1e1e';
                    this.ctx.fillText(
                        targetCell.Content,
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
    const isFocused = this.focusedPaneId === pane.PaneID;
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
