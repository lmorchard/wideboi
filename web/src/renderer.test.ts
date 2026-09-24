import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { GridRenderer } from './renderer';
import type { MsgLayoutSnapshot, MsgPaneUpdate } from './protocol';

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
    PaneID: 1, Cols: 1, Rows: 1, Lines: [], CursorX: 0, CursorY: 0,
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
});
