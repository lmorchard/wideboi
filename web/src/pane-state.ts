import { create } from '@bufbuild/protobuf';
import { formatFontSpec } from './fonts';
import {
  LineDataSchema, type MsgPanePatch, type MsgPaneUpdate,
} from './gen/internal/protocol/wirepb/wideboi_pb';
import type { RenderStats } from './stats';

export const termSettings = {
  fontSize: parseInt(typeof localStorage !== 'undefined' ? localStorage.getItem('wideboi:fontSize') || '14' : '14', 10),
  fontFamily: (typeof localStorage !== 'undefined' ? localStorage.getItem('wideboi:fontFamily') : null) || 'monospace',
  get font() {
    return formatFontSpec(this.fontFamily, this.fontSize);
  },
  get cellHeight() {
    return this.fontSize * 1.2;
  },
  save() {
    if (typeof localStorage !== 'undefined') {
      localStorage.setItem('wideboi:fontSize', this.fontSize.toString());
      localStorage.setItem('wideboi:fontFamily', this.fontFamily);
    }
  }
};

export type CellPoint = { x: number; y: number };

export function measureCellWidth(): number {
  const canvas = document.createElement('canvas');
  const context = canvas.getContext('2d');
  if (!context) throw new Error('Could not measure terminal font');
  context.font = termSettings.font;
  return Math.max(context.measureText('W').width, 1);
}

// Keep the client mirror outside the elements: a pane update may arrive before
// Lit creates the pane after a layout snapshot.
export class PaneStore {
  private panes = new Map<number, MsgPaneUpdate>();

  // stats is only set with ?stats=1; when undefined no timing calls are made.
  constructor(private readonly stats?: RenderStats) {}

  get(paneID: number): MsgPaneUpdate | undefined { return this.panes.get(paneID); }
  update(pane: MsgPaneUpdate) {
    const stats = this.stats;
    const start = stats ? performance.now() : 0;
    this.panes.set(pane.paneId, pane);
    if (stats) stats.recordApply('full', performance.now() - start);
  }
  close(paneID: number) { this.panes.delete(paneID); }
  mouseTracking(paneID: number): boolean { return this.panes.get(paneID)?.mouseTracking ?? false; }

  patch(patch: MsgPanePatch): boolean {
    const stats = this.stats;
    if (!stats) return this.applyPatch(patch);
    const start = performance.now();
    const applied = this.applyPatch(patch);
    stats.recordApply(applied ? 'patch' : 'resync', performance.now() - start);
    return applied;
  }

  // Generations are bigint (protobuf-es ignores jstype = JS_NUMBER).
  private applyPatch(patch: MsgPanePatch): boolean {
    const base = this.panes.get(patch.paneId);
    if (!base || base.generation !== patch.baseGeneration ||
        base.cols !== patch.cols || base.rows !== patch.rows ||
        base.lines.length !== base.rows || patch.generation <= patch.baseGeneration ||
        Math.abs(patch.shiftRows) >= base.rows) {
      this.close(patch.paneId);
      return false;
    }
    const band = patch.changedRows.length;
    const shifted = patch.shiftRows !== 0;
    if (shifted && (band < Math.abs(patch.shiftRows) || band > Math.floor(base.rows / 2))) {
      this.close(patch.paneId);
      return false;
    }
    const lines = Array.from({ length: base.rows }, (_, y) => base.lines[y - patch.shiftRows]);
    const seen = new Set<number>();
    for (const row of patch.changedRows) {
      if (row.y < 0 || row.y >= base.rows || seen.has(row.y) || row.cells.length !== base.cols ||
          (shifted && (patch.shiftRows < 0 ? row.y < base.rows - band : row.y >= band))) {
        this.close(patch.paneId);
        return false;
      }
      seen.add(row.y);
      lines[row.y] = create(LineDataSchema, { cells: row.cells });
    }
    if (lines.some(line => line === undefined || line.cells.length !== base.cols)) {
      this.close(patch.paneId);
      return false;
    }
    // Cursor and mouse fields always overwrite: absent proto3 fields are false.
    this.panes.set(patch.paneId, {
      ...base, lines, generation: patch.generation,
      cursorX: patch.cursorX, cursorY: patch.cursorY,
      cursorVisible: patch.cursorVisible, mouseTracking: patch.mouseTracking,
      scrollOffset: patch.scrollOffset, scrollbackLen: patch.scrollbackLen,
      unreadOutput: patch.unreadOutput,
    });
    return true;
  }
}

export function selectionText(pane: MsgPaneUpdate | undefined, start: CellPoint, end: CellPoint): string {
  if (!pane) return '';
  let a = start, b = end;
  if (a.y > b.y || (a.y === b.y && a.x > b.x)) [a, b] = [b, a];
  const rows: string[] = [];
  for (let y = a.y; y <= b.y; y++) {
    const line = pane.lines[y]?.cells || [];
    const first = y === a.y ? a.x : 0;
    const last = y === b.y ? b.x : line.length - 1;
    let text = '';
    for (let x = first; x <= last; x++) {
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
