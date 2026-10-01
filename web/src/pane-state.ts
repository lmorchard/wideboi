import { create } from '@bufbuild/protobuf';
import { formatFontSpec } from './fonts';
import { getPref, setPref } from './prefs';
import {
  LineDataSchema, type MsgPanePatch, type MsgPaneUpdate,
} from './gen/internal/protocol/wirepb/wideboi_pb';
import type { RenderStats } from './stats';

export const termSettings = {
  fontSize: getPref('fontSize'),
  fontFamily: getPref('fontFamily'),
  get font() {
    return formatFontSpec(this.fontFamily, this.fontSize);
  },
  get cellHeight() {
    return this.fontSize * 1.2;
  },
  save() {
    setPref('fontSize', this.fontSize);
    setPref('fontFamily', this.fontFamily);
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
      lines[row.y] = create(LineDataSchema, { cells: row.cells, wrapped: row.wrapped });
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
      links: patch.links?.length ? patch.links : base.links,
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
  let result = '';
  for (let i = 0; i < rows.length; i++) {
    result += rows[i];
    if (i < rows.length - 1) {
      const lineY = a.y + i;
      const wrapped = pane.lines[lineY]?.wrapped ?? false;
      if (!wrapped) {
        result += '\n';
      }
    }
  }
  return result;
}

export interface DetectedUrl {
  url: string;
  start: CellPoint;
  end: CellPoint;
}

function isSafeUrl(rawUrl: string): boolean {
  try {
    const parsed = new URL(rawUrl);
    return ['http:', 'https:', 'mailto:', 'ssh:', 'git:', 'gemini:'].includes(parsed.protocol);
  } catch {
    return false;
  }
}

function cleanUrl(raw: string): string {
  let url = raw;
  while (url.length > 0) {
    const last = url[url.length - 1];
    if (['.', ',', ';', ':', '!', "'", '"', '`', '>'].includes(last)) {
      url = url.slice(0, -1);
    } else if (last === ')' || last === ']' || last === '}') {
      const open = last === ')' ? '(' : last === ']' ? '[' : '{';
      const openCount = (url.match(new RegExp('\\' + open, 'g')) || []).length;
      const closeCount = (url.match(new RegExp('\\' + last, 'g')) || []).length;
      if (closeCount > openCount) {
        url = url.slice(0, -1);
        continue;
      }
      break;
    } else {
      break;
    }
  }
  return url;
}

export function findUrlAt(pane: MsgPaneUpdate | undefined, point: CellPoint): DetectedUrl | undefined {
  if (!pane || point.y < 0 || point.y >= pane.lines.length) return undefined;

  // 1. Explicit OSC 8 hyperlink on cell
  const targetCell = pane.lines[point.y]?.cells?.[point.x];
  if (targetCell && targetCell.linkId > 0 && pane.links && pane.links.length >= targetCell.linkId) {
    const linkId = targetCell.linkId;
    const url = pane.links[linkId - 1];
    if (isSafeUrl(url)) {
      let startX = point.x;
      while (startX > 0 && pane.lines[point.y]?.cells?.[startX - 1]?.linkId === linkId) {
        startX--;
      }
      let endX = point.x;
      while (endX < pane.cols - 1 && pane.lines[point.y]?.cells?.[endX + 1]?.linkId === linkId) {
        endX++;
      }
      return {
        url,
        start: { x: startX, y: point.y },
        end: { x: endX, y: point.y },
      };
    }
  }

  // 2. Plaintext URL autolinking fallback

  let startY = point.y;
  while (startY > 0) {
    const prevLine = pane.lines[startY - 1]?.cells || [];
    if (prevLine.length >= pane.cols && prevLine[pane.cols - 1]?.content?.trim()) {
      startY--;
    } else {
      break;
    }
  }

  let endY = point.y;
  while (endY < pane.lines.length - 1) {
    const curLine = pane.lines[endY]?.cells || [];
    if (curLine.length >= pane.cols && curLine[pane.cols - 1]?.content?.trim()) {
      endY++;
    } else {
      break;
    }
  }

  const charToPoint: CellPoint[] = [];
  let combinedText = '';

  for (let y = startY; y <= endY; y++) {
    const cells = pane.lines[y]?.cells || [];
    for (let x = 0; x < cells.length; x++) {
      const cell = cells[x];
      if (!cell) continue;
      let continuation = false;
      for (let back = 1; back <= 3 && x - back >= 0; back++) {
        if (cells[x - back]?.width > back) {
          continuation = true;
          break;
        }
      }
      if (continuation) continue;
      const content = cell.content || ' ';
      for (let i = 0; i < content.length; i++) {
        charToPoint.push({ x, y });
        combinedText += content[i];
      }
    }
  }

  const urlRegex = /https?:\/\/[^\s<>"'|\\^`]+/g;
  let match: RegExpExecArray | null;
  while ((match = urlRegex.exec(combinedText)) !== null) {
    const rawUrl = match[0];
    const cleaned = cleanUrl(rawUrl);
    if (!cleaned) continue;
    const matchStart = match.index;
    const matchEnd = matchStart + cleaned.length - 1;
    const startPoint = charToPoint[matchStart];
    const endPoint = charToPoint[matchEnd];
    if (!startPoint || !endPoint) continue;

    const afterOrAtStart = point.y > startPoint.y || (point.y === startPoint.y && point.x >= startPoint.x);
    const beforeOrAtEnd = point.y < endPoint.y || (point.y === endPoint.y && point.x <= endPoint.x);
    if (afterOrAtStart && beforeOrAtEnd) {
      return { url: cleaned, start: startPoint, end: endPoint };
    }
  }
  return undefined;
}
