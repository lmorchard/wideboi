import { MsgLayoutSnapshot, MsgPaneUpdate, PlacementData } from './gen/wideboi_pb';
import { decodeColor } from './colors';

export class GridRenderer {
  private canvas: HTMLCanvasElement;
  private ctx: CanvasRenderingContext2D;
  private cellWidth = 0;
  private cellHeight = 0;

  private panes = new Map<number, MsgPaneUpdate>();
  private layout: MsgLayoutSnapshot | null = null;
  
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
  }

  public handlePaneUpdate(update: MsgPaneUpdate) {
    this.panes.set(update.paneId, update);
  }

  public resize(width: number, height: number) {
    const dpr = window.devicePixelRatio || 1;
    this.canvas.width = width * dpr;
    this.canvas.height = height * dpr;
    this.canvas.style.width = `${width}px`;
    this.canvas.style.height = `${height}px`;
    
    this.ctx.scale(dpr, dpr);
    this.measureFont();
  }

  public getGridSize(): { cols: number, rows: number } {
    if (this.cellWidth === 0 || this.cellHeight === 0) return { cols: 80, rows: 24 };
    
    // Reverse engineer device CSS pixels to cols/rows
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

  private draw() {
    // Clear background
    this.ctx.fillStyle = '#1e1e1e';
    this.ctx.fillRect(0, 0, this.canvas.width, this.canvas.height);

    if (!this.layout) return;

    // Placements are ordered by Z-index by the server, but let's sort just in case
    const placements = [...this.layout.placements].sort((a, b) => a.z - b.z);

    for (const p of placements) {
      this.drawPlacement(p);
    }
  }

  private drawPlacement(p: PlacementData) {
    const pane = this.panes.get(p.paneId);
    if (!pane) return;
    if (!p.src || !p.dst) return;

    const srcMinX = p.src.minX;
    const srcMinY = p.src.minY;
    const dstMinX = p.dst.minX;
    const dstMinY = p.dst.minY;
    const dx = p.dst.maxX - p.dst.minX;
    const dy = p.dst.maxY - p.dst.minY;

    // Save context for clipping
    this.ctx.save();
    
    // Set clipping region to Dst
    this.ctx.beginPath();
    this.ctx.rect(
      dstMinX * this.cellWidth, 
      dstMinY * this.cellHeight, 
      dx * this.cellWidth, 
      dy * this.cellHeight
    );
    this.ctx.clip();

    // Set font again (context state might change)
    this.ctx.font = '14px monospace';
    this.ctx.textBaseline = 'top';

    // Draw the pane content offset by (Dst.Min - Src.Min)
    const offsetX = dstMinX - srcMinX;
    const offsetY = dstMinY - srcMinY;

    for (let y = 0; y < pane.lines.length; y++) {
      const screenY = y + offsetY;
      // Skip rendering lines outside the clip bounds vertically to save CPU
      if (screenY < dstMinY || screenY >= dstMinY + dy) continue;

      const line = pane.lines[y];
      let x = 0;
      for (const cell of line.cells) {
        const screenX = x + offsetX;
        
        // Only draw if within horizontal bounds
        if (screenX >= dstMinX && screenX < dstMinX + dx) {
          const bg = decodeColor(cell.style?.bg, true);
          const fg = decodeColor(cell.style?.fg, false);
          
          if (bg !== '#1e1e1e') {
            this.ctx.fillStyle = bg;
            this.ctx.fillRect(
              screenX * this.cellWidth, 
              screenY * this.cellHeight, 
              this.cellWidth * (cell.width || 1), 
              this.cellHeight
            );
          }

          if (cell.content && cell.content !== ' ') {
            this.ctx.fillStyle = fg;
            // Handle bold attributes (AttrBold = 1 << 0)
            const isBold = (cell.style?.attrs ?? 0) & 1;
            this.ctx.font = `${isBold ? 'bold ' : ''}14px monospace`;
            this.ctx.fillText(
              cell.content, 
              screenX * this.cellWidth, 
              screenY * this.cellHeight
            );
          }
        }
        x += (cell.width || 1);
      }
    }

    // Draw Cursor
    if (pane.cursorVisible && this.layout?.focusPaneId === pane.paneId) {
      const curX = pane.cursorX + offsetX;
      const curY = pane.cursorY + offsetY;
      
      if (curX >= dstMinX && curX < dstMinX + dx && curY >= dstMinY && curY < dstMinY + dy) {
        this.ctx.fillStyle = '#d4d4d4'; // Cursor color
        this.ctx.fillRect(
          curX * this.cellWidth, 
          curY * this.cellHeight, 
          this.cellWidth, 
          this.cellHeight
        );
        
        // Invert text under cursor
        if (curY < pane.lines.length) {
            let cx = 0;
            let targetCell = null;
            for(const cell of pane.lines[curY].cells) {
                if (cx === pane.cursorX) { targetCell = cell; break; }
                cx += (cell.width || 1);
            }
            if (targetCell && targetCell.content && targetCell.content !== ' ') {
                this.ctx.fillStyle = '#1e1e1e'; // inverted
                this.ctx.fillText(
                    targetCell.content,
                    curX * this.cellWidth,
                    curY * this.cellHeight
                );
            }
        }
      }
    }

    this.ctx.restore();
    
    // Draw borders or sliver chrome if this is a sliver placement (Split 4 stuff, but basic implementation here)
    if (p.kind === 1) { // PLACEMENT_SLIVER
       this.ctx.fillStyle = 'rgba(255, 255, 255, 0.1)';
       this.ctx.fillRect(
           dstMinX * this.cellWidth,
           dstMinY * this.cellHeight,
           dx * this.cellWidth,
           dy * this.cellHeight
       );
       // Border
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
