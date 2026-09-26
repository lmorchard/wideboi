import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { create } from '@bufbuild/protobuf';
import { PaneStore, selectionText } from './pane-state';
import { PanePainter } from './pane-painter';
import { WideboiPane } from './wideboi-pane';
import { RenderStats } from './stats';
import { getTheme } from './themes';
import {
  CellDataSchema, LineDataSchema, MsgPanePatchSchema, MsgPaneUpdateSchema,
} from './gen/internal/protocol/wirepb/wideboi_pb';

const cell = (content: string) => create(CellDataSchema, { content, width: 1 });
const row = (...contents: string[]) => create(LineDataSchema, { cells: contents.map(cell) });

describe('pane mirrors', () => {
  it('applies changed rows and rejects a stale patch', () => {
    const store = new PaneStore();
    const full = create(MsgPaneUpdateSchema, { paneId: 1, generation: 1n, cols: 2, rows: 4,
      lines: Array.from({ length: 4 }, () => row(' ', ' ')) });
    store.update(full);
    const patch = create(MsgPanePatchSchema, { paneId: 1, cols: 2, rows: 4,
      baseGeneration: 1n, generation: 2n, changedRows: [{ y: 1, cells: [cell('X'), cell(' ')] }],
      cursorX: 1, cursorY: 1, cursorVisible: true, mouseTracking: true });
    expect(store.patch(patch)).toBe(true);
    expect(store.get(1)?.lines[1].cells[0].content).toBe('X');
    expect(store.get(1)?.lines[0]).toEqual(full.lines[0]);
    expect(store.get(1)?.cursorVisible).toBe(true);
    expect(store.mouseTracking(1)).toBe(true);
    expect(store.patch({ ...patch, generation: 3n })).toBe(false);
    expect(store.get(1)).toBeUndefined();
    store.update({ ...full, generation: 3n });
    expect(store.get(1)?.generation).toBe(3n);
    store.close(1);
    expect(store.mouseTracking(1)).toBe(false);
  });

  it('treats absent cursor and mouse fields as false', () => {
    const store = new PaneStore();
    store.update(create(MsgPaneUpdateSchema, { paneId: 1, generation: 1n, cols: 1, rows: 1,
      lines: [row(' ')], cursorVisible: true, mouseTracking: true }));
    const hide = create(MsgPanePatchSchema, { paneId: 1, cols: 1, rows: 1,
      baseGeneration: 1n, generation: 2n });
    expect(store.patch(hide)).toBe(true);
    expect(store.get(1)?.cursorVisible).toBe(false);
    expect(store.get(1)?.mouseTracking).toBe(false);
  });

  it('applies a row shift and rejects missing or interior replacement rows', () => {
    const store = new PaneStore();
    const full = create(MsgPaneUpdateSchema, { paneId: 1, generation: 1n,
      cols: 1, rows: 4, lines: ['A', 'B', 'C', 'D'].map(value => row(value)) });
    store.update(full);
    const patch = create(MsgPanePatchSchema, { paneId: 1, cols: 1, rows: 4,
      baseGeneration: 1n, generation: 2n, shiftRows: -1,
      changedRows: [{ y: 2, cells: [cell('E')] }, { y: 3, cells: [cell('F')] }] });
    expect(store.patch(patch)).toBe(true);
    expect(store.get(1)?.lines.map(line => line.cells[0].content)).toEqual(['B', 'C', 'E', 'F']);
    store.update(full);
    expect(store.patch({ ...patch, changedRows: [] })).toBe(false);
    store.update(full);
    expect(store.patch({ ...patch, changedRows: [
      { ...patch.changedRows[0], y: 1 }, patch.changedRows[1],
    ] })).toBe(false);
  });

  it('copies wide glyphs without their continuation cells', () => {
    const pane = create(MsgPaneUpdateSchema, { paneId: 1, cols: 4, rows: 1,
      lines: [{ cells: [{ content: '界', width: 2 }, cell(' '), cell('B'), cell(' ')] }] });
    expect(selectionText(pane, { x: 0, y: 0 }, { x: 2, y: 0 })).toBe('界B');
  });

  it('updates scroll offset, scrollback length, and unread output from patch', () => {
    const store = new PaneStore();
    store.update(create(MsgPaneUpdateSchema, {
      paneId: 1, generation: 1n, cols: 1, rows: 1,
      lines: [row('A')],
    }));
    const patch = create(MsgPanePatchSchema, {
      paneId: 1, cols: 1, rows: 1,
      baseGeneration: 1n, generation: 2n,
      scrollOffset: 5, scrollbackLen: 42, unreadOutput: true,
    });
    expect(store.patch(patch)).toBe(true);
    const pane = store.get(1);
    expect(pane?.scrollOffset).toBe(5);
    expect(pane?.scrollbackLen).toBe(42);
    expect(pane?.unreadOutput).toBe(true);
  });
});

describe('per-pane painting', () => {
  const frames = new Map<number, FrameRequestCallback>();
  const listeners = new Map<string, () => void>();
  const clearRect = vi.fn();
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
    clearRect.mockClear();
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

  const painter = (stats?: RenderStats) => {
    const ctx = { setTransform: vi.fn(), clearRect, fillRect, fillText, fillStyle: '' };
    const canvas = { width: 0, height: 0, style: { width: '', height: '' },
      getContext: () => ctx } as unknown as HTMLCanvasElement;
    return { painter: new PanePainter(canvas, 8, stats), canvas, ctx };
  };

  it('records full, patch, resync and draw timings only when given stats', () => {
    // The clock only advances while draw() issues canvas calls, so a non-zero
    // draw sample proves the timing wraps draw() itself.
    let clock = 0;
    const now = vi.spyOn(performance, 'now').mockImplementation(() => clock);
    clearRect.mockImplementation(() => { clock += 7; });
    fillRect.mockImplementation(() => { clock += 7; });
    const full = create(MsgPaneUpdateSchema, { paneId: 1, cols: 1, rows: 1, generation: 1n,
      lines: [row(' ')] });
    const patch = create(MsgPanePatchSchema, { paneId: 1, cols: 1, rows: 1,
      baseGeneration: 1n, generation: 2n });

    const plainStore = new PaneStore();
    plainStore.update(full);
    plainStore.patch(patch);
    const { painter: plain } = painter();
    plain.resize(8, 20);
    plain.start();
    plain.setPane(plainStore.get(1));
    flush();
    plain.stop();
    expect(now).not.toHaveBeenCalled();

    const stats = new RenderStats();
    const store = new PaneStore(stats);
    store.update(full);
    expect(store.patch(patch)).toBe(true);
    expect(store.patch(patch)).toBe(false);
    const { painter: p } = painter(stats);
    p.resize(8, 20);
    p.start();
    p.setPane(full);
    flush();
    p.stop();
    expect([stats.fulls, stats.patches, stats.resyncs, stats.draws]).toEqual([1, 1, 1, 1]);
    const s = stats.summary(1000);
    expect([s.apply.full.count, s.apply.patch.count, s.apply.resync.count]).toEqual([1, 1, 1]);
    expect(s.apply.full.max).toBe(0);
    expect(s.draw.count).toBe(1);
    expect(s.draw.max).toBeGreaterThan(0);
    clearRect.mockReset();
    fillRect.mockReset();
    now.mockRestore();
  });

  it('draws once for a burst and pauses while hidden or stopped', () => {
    const { painter: p } = painter();
    p.resize(80, 40);
    p.start();
    p.setPane(create(MsgPaneUpdateSchema, { paneId: 1, cols: 1, rows: 1,
      lines: [row('A')] }));
    expect(frames.size).toBe(1);
    flush();
    expect(fillText.mock.calls.some(([value]) => value === 'A')).toBe(true);
    expect(frames.size).toBe(0);
    hidden = true;
    listeners.get('visibilitychange')?.();
    p.setPane(create(MsgPaneUpdateSchema, { paneId: 1, cols: 1, rows: 1,
      lines: [row('B')] }));
    expect(frames.size).toBe(0);
    hidden = false;
    listeners.get('visibilitychange')?.();
    expect(frames.size).toBe(1);
    p.stop();
    expect(frames.size).toBe(0);
  });

  it('does not clear the canvas on a redundant resize', () => {
    const { painter: p, canvas } = painter();
    let width = 0, height = 0, clears = 0;
    Object.defineProperty(canvas, 'width', {
      get: () => width, set: (value: number) => { width = value; clears++; },
    });
    Object.defineProperty(canvas, 'height', {
      get: () => height, set: (value: number) => { height = value; clears++; },
    });
    p.start();
    p.resize(80, 40);
    flush();
    expect(clears).toBe(2);
    p.resize(80, 40);
    expect(clears).toBe(2);
    expect(frames.size).toBe(0);
    expect(canvas.style.width).toBe('');
    expect(canvas.style.height).toBe('');
    p.stop();
  });

  it('draws scroll footer when scrolled up', () => {
    const { painter: p } = painter();
    p.start();
    p.resize(240, 100);
    p.setPane(create(MsgPaneUpdateSchema, {
      paneId: 1, cols: 10, rows: 5,
      scrollOffset: 7, scrollbackLen: 50, unreadOutput: true,
      lines: [row('A'), row('B'), row('C'), row('D'), row('E')],
    }));
    flush();
    const calls = fillText.mock.calls.map(c => c[0]);
    expect(calls.some(text => typeof text === 'string' && text.includes('▲ scroll +7/50') && text.includes('new output'))).toBe(true);
    p.stop();
  });

  it('scales 2D transform and redraws when zoom changes', () => {
    const { painter: p, ctx } = painter();
    p.start();
    p.resize(100, 100);
    expect(ctx.setTransform).toHaveBeenLastCalledWith(1, 0, 0, 1, 0, 0);
    p.setZoom(1.5);
    expect(ctx.setTransform).toHaveBeenLastCalledWith(1.5, 0, 0, 1.5, 0, 0);
    expect(frames.size).toBe(1);
    flush();
    p.setZoom(0.75);
    expect(ctx.setTransform).toHaveBeenLastCalledWith(0.75, 0, 0, 0.75, 0, 0);
    p.stop();
  });

  it('clears canvas even when pane is undefined', () => {
    const { painter: p } = painter();
    p.start();
    p.resize(100, 100);
    p.setPane(undefined);
    flush();
    expect(clearRect).toHaveBeenCalledWith(0, 0, 100, 100);
    p.stop();
  });

  it('calculates cellAt coordinates correctly under zoom', () => {
    const fakePane = {
      canvas: {
        getBoundingClientRect: () => ({ left: 20, top: 40, width: 800, height: 400 }),
      },
      pane: { cols: 80, rows: 24 },
      cellWidth: 10,
      zoom: 1.0,
    };
    // At zoom = 1.0: cellWidth = 10, cellHeight = 16.8
    // point: (clientX=50, clientY=74) -> dx = 30 -> col 3, dy = 34 -> row 2
    let pt = WideboiPane.prototype.cellAt.call(fakePane, 50, 74);
    expect(pt).toEqual({ x: 3, y: 2 });

    // At zoom = 2.0: effectiveCellWidth = 20, effectiveCellHeight = 33.6
    fakePane.zoom = 2.0;
    // dx = 40 -> col 2, dy = 68 -> row 2
    pt = WideboiPane.prototype.cellAt.call(fakePane, 60, 108);
    expect(pt).toEqual({ x: 2, y: 2 });

    // At zoom = 0.5: effectiveCellWidth = 5, effectiveCellHeight = 8.4
    fakePane.zoom = 0.5;
    // dx = 30 -> col 6, dy = 34 -> row 4
    pt = WideboiPane.prototype.cellAt.call(fakePane, 50, 74);
    expect(pt).toEqual({ x: 6, y: 4 });
  });

  it('redraws and uses theme colors when theme changes', () => {
    const nord = getTheme('nord');
    const { painter: p, ctx } = painter();
    p.start();
    p.resize(100, 100);
    p.setFocused(true);
    p.setPane(create(MsgPaneUpdateSchema, {
      paneId: 1, cols: 2, rows: 2,
      cursorVisible: true, cursorX: 0, cursorY: 0,
      lines: [row('A', 'B'), row('C', 'D')],
    }));
    flush();

    // Default dark theme cursor color was used (#d4d4d4)
    expect(ctx.fillStyle).toBe('#1e1e1e');

    // Switch theme to nord
    p.setTheme(nord);
    expect(frames.size).toBe(1);
    flush();
    // After drawing cursor under nord: cursor fill is nord's cursor (#d8dee9) and char text is nord's cursorText (#2e3440)
    expect(ctx.fillStyle).toBe(nord.terminal.cursorText);
    p.stop();
  });
});
