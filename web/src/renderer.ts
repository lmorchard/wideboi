import type { MsgLayoutSnapshot, MsgPaneUpdate, PlacementData, Rectangle } from './protocol';
import { decodeColor } from './colors';

export class GridRenderer {
  private canvas: HTMLCanvasElement;
  private ctx: CanvasRenderingContext2D;
  private cellWidth = 0;
  private cellHeight = 0;

  private panes = new Map<number, MsgPaneUpdate>();
  private layout: MsgLayoutSnapshot | null = null;
  private placements: PlacementData[] = [];
  
  private animationFrameId = 0;

  constructor(canvas: HTMLCanvasElement) {
    this.canvas = canvas;
    const ctx = canvas.getContext('2d');
    if (!ctx) throw new Error('Could not get 2d context');
    this.ctx = ctx;

    this.measureFont();
  }

  public start() {
    const loop = () => {
      this.draw();
      this.animationFrameId = requestAnimationFrame(loop);
    };
    loop();
  }

  public stop() {
    cancelAnimationFrame(this.animationFrameId);
  }

  public handleLayoutSnapshot(snapshot: MsgLayoutSnapshot) {
    this.layout = snapshot;
    this.recomputePlacements();
  }

  public handlePaneUpdate(update: MsgPaneUpdate) {
    this.panes.set(update.PaneID, update);
  }

  public resize(width: number, height: number) {
    const dpr = window.devicePixelRatio || 1;
    this.canvas.width = width * dpr;
    this.canvas.height = height * dpr;
    this.canvas.style.width = `${width}px`;
    this.canvas.style.height = `${height}px`;
    
    this.ctx.scale(dpr, dpr);
    this.measureFont();
    this.recomputePlacements();
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
		return this.layout?.FocusPaneID || 0;
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
  
  // Re-implements ScrollStrategy from Go
  private recomputePlacements() {
      if (!this.layout) return;
      
      const grid = this.getGridSize();
      const availHeight = Math.max(grid.rows - 2, 1);
      const w = grid.cols;
      
      let focusIdx = -1;
      for (let i = 0; i < this.layout.Columns.length; i++) {
          if (this.layout.Columns[i].PaneID === this.layout.FocusPaneID) {
              focusIdx = i;
              break;
          }
      }
      
      let startIdx = Math.max(focusIdx, 0);
      if (startIdx >= this.layout.Columns.length) {
          this.placements = [];
          return;
      }
      
      let availWidth = w;
      availWidth -= this.layout.Columns[startIdx].Width;
      
      for (; startIdx > 0 && availWidth > 0; ) {
          const prevW = this.layout.Columns[startIdx - 1].Width;
          if (availWidth - (prevW + 1) >= 0) {
              availWidth -= (prevW + 1);
              startIdx--;
          } else {
              break;
          }
      }
      
      const places: PlacementData[] = [];
      let currentX = 0;
      
      for (let i = startIdx; i < this.layout.Columns.length; i++) {
          const c = this.layout.Columns[i];
          const paneW = c.Width;
          
          if (currentX >= w) break;
          
          let drawW = paneW;
          if (currentX + drawW > w) {
              drawW = w - currentX;
          }
          
          const dst: Rectangle = { Min: {X: currentX, Y: 1}, Max: {X: currentX+drawW, Y: 1+availHeight} };
          
          // Src
          const srcY = dst.Min.Y - 1;
          const src: Rectangle = { Min: {X: 0, Y: srcY}, Max: {X: drawW, Y: srcY + (dst.Max.Y - dst.Min.Y)} };
          
          places.push({
              PaneID: c.PaneID,
              Src: src,
              Dst: dst,
              Z: 0,
              Kind: 0
          });
          currentX += paneW + 1;
      }
      
      this.placements = places;
  }

  private draw() {
    this.ctx.fillStyle = '#1e1e1e';
    this.ctx.fillRect(0, 0, this.canvas.width, this.canvas.height);

    if (!this.layout) return;

    for (const p of this.placements) {
      this.drawPlacement(p);
    }
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
      
      let x = 0;
      for (const cell of line) {
        const screenX = x + offsetX;
        
        if (screenX >= dstMinX && screenX < dstMinX + dx) {
          const bg = decodeColor(cell.Style?.Bg, true);
          const fg = decodeColor(cell.Style?.Fg, false);
          
          if (bg !== '#1e1e1e') {
            this.ctx.fillStyle = bg;
            this.ctx.fillRect(
              screenX * this.cellWidth, 
              screenY * this.cellHeight, 
              this.cellWidth * (cell.Width || 1), 
              this.cellHeight
            );
          }

          if (cell.Content && cell.Content !== ' ') {
            this.ctx.fillStyle = fg;
            const isBold = (cell.Style?.Attrs ?? 0) & 1;
            this.ctx.font = `${isBold ? 'bold ' : ''}14px monospace`;
            this.ctx.fillText(
              cell.Content, 
              screenX * this.cellWidth, 
              screenY * this.cellHeight
            );
          }
        }
        x += (cell.Width || 1);
      }
    }

    if (pane.CursorVisible && this.layout?.FocusPaneID === pane.PaneID) {
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
            let cx = 0;
            let targetCell = null;
            if (pane.Lines[curY]) {
                for(const cell of pane.Lines[curY]) {
                    if (cx === pane.CursorX) { targetCell = cell; break; }
                    cx += (cell.Width || 1);
                }
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
    const isFocused = this.layout?.FocusPaneID === pane.PaneID;
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
