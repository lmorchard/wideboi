import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { GridRenderer } from './renderer';
import type { MsgLayoutSnapshot, MsgPaneUpdate } from './protocol';

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

  it('uses wire cell indexes after a wide glyph and forgets closed panes', () => {
    const r = renderer();
    r.resize(80, 80);
    r.handleLayoutSnapshot({
      Columns: [{ PaneID: 1, Width: 10, Height: 3 }],
      FocusPaneID: 1, PaneStatuses: {}, PaneTitles: {}
    });
    const blank = { Content: ' ', Width: 1, Style: undefined as never };
    r.handlePaneUpdate({
      PaneID: 1, Cols: 10, Rows: 3,
      Lines: [[
        { Content: '界', Width: 2, Style: undefined as never },
        blank,
        { Content: 'B', Width: 1, Style: undefined as never },
        ...Array(7).fill(blank)
      ]],
      CursorX: 0, CursorY: 0, CursorVisible: false, MouseTracking: true
    });
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
    r.handleLayoutSnapshot({
      Columns: [{ PaneID: 1, Width: 8, Height: 3 }, { PaneID: 2, Width: 8, Height: 3 }],
      FocusPaneID: 2, PaneStatuses: {}, PaneTitles: {}
    });
    expect(r.getPaneHit(0, 1).paneID).toBe(1);
    expect(r.getPaneHit(2, 1).paneID).toBe(2);
    expect(r.getPaneHit(1, 1).paneID).toBe(0);
  });
});
