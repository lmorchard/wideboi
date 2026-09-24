import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { GridRenderer } from './renderer';
import { create } from '@bufbuild/protobuf';
import {
  CellDataSchema, LineDataSchema, MsgLayoutSnapshotSchema, MsgPanePatchSchema, MsgPaneUpdateSchema,
  type MsgPaneUpdate,
} from './gen/internal/protocol/wirepb/wideboi_pb';

describe('GridRenderer frame scheduling', () => {
  const frames = new Map<number, FrameRequestCallback>();
  const listeners = new Map<string, () => void>();
  const fillRect = vi.fn();
  const fillText = vi.fn();
  let nextFrame = 1;
  let hidden = false;

  const flush = () => {
    const pending = [...frames.values()];
    frames.clear();
    pending.forEach(callback => callback(0));
  };

  beforeEach(() => {
    nextFrame = 1;
    hidden = false;
    frames.clear();
    listeners.clear();
    fillRect.mockClear();
    fillText.mockClear();
    vi.stubGlobal('document', {
      get hidden() { return hidden; },
      addEventListener: (name: string, cb: () => void) => listeners.set(name, cb),
      removeEventListener: (name: string) => listeners.delete(name),
    });
    vi.stubGlobal('window', { devicePixelRatio: 1 });
    vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => {
      const id = nextFrame++;
      frames.set(id, cb);
      return id;
    });
    vi.stubGlobal('cancelAnimationFrame', (id: number) => frames.delete(id));
  });

  afterEach(() => { vi.unstubAllGlobals(); });

  const renderer = () => {
    const ctx = {
      measureText: () => ({ width: 8 }),
      setTransform: vi.fn(),
      fillRect,
      save: vi.fn(), restore: vi.fn(), beginPath: vi.fn(),
      rect: vi.fn(), clip: vi.fn(), fillText,
    };
    const canvas = {
      width: 0, height: 0, style: { width: '', height: '' },
      getContext: () => ctx,
    } as unknown as HTMLCanvasElement;
    return new GridRenderer(canvas);
  };

  const layout = create(MsgLayoutSnapshotSchema);
  const pane = create(MsgPaneUpdateSchema, { paneId: 1, generation: 1n, cols: 1, rows: 1 });

  it('draws once for a burst and stays idle until another change', () => {
    const r = renderer();
    r.resize(80, 40);
    r.start();
    r.handleLayoutSnapshot(layout);
    r.handlePaneUpdate(pane);
    r.handlePaneUpdate(pane);
    expect(frames.size).toBe(1);
    flush();
    expect(fillRect).toHaveBeenCalledTimes(1);
    flush();
    expect(fillRect).toHaveBeenCalledTimes(1);
    r.handlePaneUpdate(pane);
    flush();
    expect(fillRect).toHaveBeenCalledTimes(2);
    r.stop();
  });

  it('keeps the painted canvas on a redundant resize notification', () => {
    const r = renderer();
    const canvas = (r as unknown as { canvas: HTMLCanvasElement }).canvas;
    let width = 0, height = 0, clears = 0;
    Object.defineProperty(canvas, 'width', {
      get: () => width, set: (value: number) => { width = value; clears++; },
    });
    Object.defineProperty(canvas, 'height', {
      get: () => height, set: (value: number) => { height = value; clears++; },
    });
    r.start();
    r.resize(80, 40);
    flush();
    expect(clears).toBe(2);
    r.resize(80, 40);
    expect(clears).toBe(2);
    expect(frames.size).toBe(0);
    r.stop();
  });

  it('pauses while hidden or stopped and resumes with the latest state', () => {
    const r = renderer();
    r.start();
    hidden = true;
    listeners.get('visibilitychange')?.();
    expect(frames.size).toBe(0);
    r.handlePaneUpdate(pane);
    expect(frames.size).toBe(0);
    hidden = false;
    listeners.get('visibilitychange')?.();
    expect(frames.size).toBe(1);
    flush();
    expect(fillRect).toHaveBeenCalledTimes(1);
    r.stop();
    r.handleLayoutSnapshot(layout);
    expect(frames.size).toBe(0);
  });

  it('uses wire cell indexes after a wide glyph and forgets closed panes', () => {
    const r = renderer();
    r.resize(80, 80);
    r.handleLayoutSnapshot(create(MsgLayoutSnapshotSchema, {
      columns: [{ paneId: 1, width: 10, height: 3 }],
    }));
    const blank = { content: ' ', width: 1 };
    r.handlePaneUpdate(create(MsgPaneUpdateSchema, {
      paneId: 1, generation: 1n, cols: 10, rows: 3,
      lines: [{ cells: [
        { content: '界', width: 2 },
        blank,
        { content: 'B', width: 1 },
        ...Array(7).fill(blank)
      ] }],
      mouseTracking: true
    }));
    expect(r.mouseTracking(1)).toBe(true);
    r.setSelection(1, { x: 0, y: 0 }, { x: 2, y: 0 });
    expect(r.selectionText()).toBe('界B');
    r.clearSelection();
    r.start();
    flush();
    expect(fillText.mock.calls.some(([text, x]) => text === 'B' && x === 16)).toBe(true);
    r.handlePaneClosed(1);
    expect(r.mouseTracking(1)).toBe(false);
    expect(frames.size).toBe(1);
    r.stop();
  });

  it('crops scroll placements at the source column when focus moves', () => {
    const r = renderer();
    r.resize(80, 80);
    r.handleLayoutSnapshot(create(MsgLayoutSnapshotSchema, {
      columns: [{ paneId: 1, width: 8, height: 3 }, { paneId: 2, width: 8, height: 3 }],
    }));
    r.setFocusedPaneId(2);
    expect(r.getPaneHit(0, 1).paneID).toBe(1);
    expect(r.getPaneHit(2, 1).paneID).toBe(2);
    expect(r.getPaneHit(1, 1).paneID).toBe(0);
  });

  it('applies complete changed rows and rejects a stale patch', () => {
    const r = renderer();
    // No style: the codec omits a zero style, so absence must render as default.
    const cell = (content: string) => create(CellDataSchema, { content, width: 1 });
    const row = (...cells: string[]) => create(LineDataSchema, { cells: cells.map(cell) });
    const full = create(MsgPaneUpdateSchema, { ...pane, cols: 2, rows: 4,
      lines: Array.from({ length: 4 }, () => row(' ', ' ')) });
    r.handlePaneUpdate(full);
    const patch = create(MsgPanePatchSchema, { paneId: 1, cols: 2, rows: 4,
      baseGeneration: 1n, generation: 2n, changedRows: [{ y: 1, cells: [cell('X'), cell(' ')] }],
      cursorX: 1, cursorY: 1, cursorVisible: true, mouseTracking: true });
    expect(r.handlePanePatch(patch)).toBe(true);
    const panes = (r as unknown as { panes: Map<number, MsgPaneUpdate> }).panes;
    expect(panes.get(1)?.lines[1].cells[0].content).toBe('X');
    expect(panes.get(1)?.lines[0]).toEqual(full.lines[0]);
    expect(panes.get(1)?.cursorVisible).toBe(true);
    expect(panes.get(1)?.mouseTracking).toBe(true);
    expect(r.handlePanePatch({ ...patch, generation: 3n })).toBe(false);
    expect(panes.has(1)).toBe(false);
    r.handlePaneUpdate({ ...full, generation: 3n });
    expect(panes.get(1)?.generation).toBe(3n);
  });

  it('treats an absent cursor field in a patch as hidden, not unchanged', () => {
    const r = renderer();
    r.handlePaneUpdate(create(MsgPaneUpdateSchema, { paneId: 1, generation: 1n, cols: 1, rows: 1,
      lines: [{ cells: [{ content: ' ', width: 1 }] }], cursorVisible: true, mouseTracking: true }));
    // Proto3 omits false, so a patch that hides the cursor carries no field.
    const hide = create(MsgPanePatchSchema, { paneId: 1, cols: 1, rows: 1, baseGeneration: 1n, generation: 2n });
    expect(r.handlePanePatch(hide)).toBe(true);
    const panes = (r as unknown as { panes: Map<number, MsgPaneUpdate> }).panes;
    expect(panes.get(1)?.cursorVisible).toBe(false);
    expect(panes.get(1)?.mouseTracking).toBe(false);
  });

  it('applies a whole-pane shift and rejects missing replacement rows', () => {
    const r = renderer();
    const cell = (content: string) => create(CellDataSchema, { content, width: 1 });
    const lines = ['A', 'B', 'C', 'D'].map(content => create(LineDataSchema, { cells: [cell(content)] }));
    r.handlePaneUpdate(create(MsgPaneUpdateSchema, { paneId: 1, generation: 1n,
      cols: 1, rows: 4, lines }));
    const patch = create(MsgPanePatchSchema, { paneId: 1, cols: 1, rows: 4,
      baseGeneration: 1n, generation: 2n, shiftRows: -1,
      changedRows: [{ y: 3, cells: [cell('E')] }] });
    expect(r.handlePanePatch(patch)).toBe(true);
    const panes = (r as unknown as { panes: Map<number, MsgPaneUpdate> }).panes;
    expect(panes.get(1)?.lines.map(line => line.cells[0].content)).toEqual(['B', 'C', 'D', 'E']);
    expect(r.handlePanePatch({ ...patch, generation: 3n })).toBe(false);
    r.handlePaneUpdate(create(MsgPaneUpdateSchema, { paneId: 1, generation: 1n,
      cols: 1, rows: 4, lines }));
    expect(r.handlePanePatch({ ...patch, changedRows: [] })).toBe(false);
  });
});
