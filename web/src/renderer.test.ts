import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { GridRenderer } from './renderer';
import type { MsgLayoutSnapshot, MsgPanePatch, MsgPaneUpdate } from './protocol';

describe('GridRenderer frame scheduling', () => {
  const frames = new Map<number, FrameRequestCallback>();
  const listeners = new Map<string, () => void>();
  const fillRect = vi.fn();
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
      scale: vi.fn(),
      fillRect,
      save: vi.fn(), restore: vi.fn(), beginPath: vi.fn(),
      rect: vi.fn(), clip: vi.fn(), fillText: vi.fn(),
    };
    const canvas = {
      width: 0, height: 0, style: { width: '', height: '' },
      getContext: () => ctx,
    } as unknown as HTMLCanvasElement;
    return new GridRenderer(canvas);
  };

  const layout: MsgLayoutSnapshot = {
    Columns: [], FocusPaneID: 0, PaneStatuses: {}, PaneTitles: {},
  };
  const pane: MsgPaneUpdate = {
    PaneID: 1, Generation: 1, Cols: 1, Rows: 1, Lines: [], CursorX: 0, CursorY: 0,
    CursorVisible: false, MouseTracking: false,
  };

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

  it('applies complete changed rows and rejects a stale patch', () => {
    const r = renderer();
    const cell = (Content: string) => ({ Content, Width: 1, Style: {
      Fg: { Kind: 0, Index: 0, R: 0, G: 0, B: 0, A: 0 },
      Bg: { Kind: 0, Index: 0, R: 0, G: 0, B: 0, A: 0 },
      UnderlineColor: { Kind: 0, Index: 0, R: 0, G: 0, B: 0, A: 0 },
      Underline: 0, Attrs: 0,
    } });
    const full: MsgPaneUpdate = { ...pane, Cols: 2, Rows: 4,
      Lines: Array.from({ length: 4 }, () => [cell(' '), cell(' ')]) };
    r.handlePaneUpdate(full);
    const patch: MsgPanePatch = { PaneID: 1, Cols: 2, Rows: 4,
      BaseGeneration: 1, Generation: 2, ChangedRows: [{ Y: 1, Cells: [cell('X'), cell(' ')] }],
      CursorX: 1, CursorY: 1, CursorVisible: true, MouseTracking: true };
    expect(r.handlePanePatch(patch)).toBe(true);
    const panes = (r as unknown as { panes: Map<number, MsgPaneUpdate> }).panes;
    expect(panes.get(1)?.Lines[1][0].Content).toBe('X');
    expect(panes.get(1)?.Lines[0]).toEqual(full.Lines[0]);
    expect(panes.get(1)?.CursorVisible).toBe(true);
    expect(panes.get(1)?.MouseTracking).toBe(true);
    expect(r.handlePanePatch({ ...patch, Generation: 3 })).toBe(false);
    expect(panes.has(1)).toBe(false);
    r.handlePaneUpdate({ ...full, Generation: 3 });
    expect(panes.get(1)?.Generation).toBe(3);
  });
});
