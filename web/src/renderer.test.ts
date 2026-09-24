import { expect, it, vi } from 'vitest';
import { GridRenderer } from './renderer';
import type { MsgPaneUpdate } from './protocol';

it('uses wire cell indexes after a wide glyph and forgets closed panes', () => {
  const painted: { text: string; x: number }[] = [];
  const ctx = {
    measureText: () => ({ width: 10 }),
    fillText: (text: string, x: number) => painted.push({ text, x }),
    fillRect: vi.fn(), save: vi.fn(), restore: vi.fn(),
    beginPath: vi.fn(), rect: vi.fn(), clip: vi.fn(), setTransform: vi.fn()
  };
  const canvas = {
    getContext: () => ctx, style: { width: '', height: '' },
    width: 0, height: 0
  } as unknown as HTMLCanvasElement;
  vi.stubGlobal('window', { devicePixelRatio: 1 });
  vi.stubGlobal('requestAnimationFrame', () => 1);
  vi.stubGlobal('cancelAnimationFrame', () => {});
  const renderer = new GridRenderer(canvas);
  renderer.resize(100, 100);
  renderer.handleLayoutSnapshot({
    Columns: [{ PaneID: 1, Width: 10, Height: 3 }],
    FocusPaneID: 1, PaneStatuses: {}, PaneTitles: {}
  });
  const blank = { Content: ' ', Width: 1, Style: undefined as never };
  const update: MsgPaneUpdate = {
    PaneID: 1, Cols: 10, Rows: 3,
    Lines: [[
      { Content: '界', Width: 2, Style: undefined as never },
      blank,
      { Content: 'B', Width: 1, Style: undefined as never },
      ...Array(7).fill(blank)
    ]],
    CursorX: 0, CursorY: 0, CursorVisible: false, MouseTracking: true
  };
  renderer.handlePaneUpdate(update);
  expect(renderer.mouseTracking(1)).toBe(true);
  renderer.setSelection(1, { x: 0, y: 0 }, { x: 2, y: 0 });
  expect(renderer.selectionText()).toBe('界B');
  renderer.clearSelection();
  renderer.start();
  expect(painted.find(p => p.text === 'B')?.x).toBe(20);
  renderer.handlePaneClosed(1);
  expect(renderer.mouseTracking(1)).toBe(false);
  renderer.stop();
  vi.unstubAllGlobals();
});

it('crops scroll placements at the source column when focus moves', () => {
  const ctx = {
    measureText: () => ({ width: 10 }), fillText: vi.fn(), fillRect: vi.fn(),
    save: vi.fn(), restore: vi.fn(), beginPath: vi.fn(), rect: vi.fn(),
    clip: vi.fn(), setTransform: vi.fn()
  };
  const canvas = { getContext: () => ctx, style: { width: '', height: '' },
    width: 0, height: 0 } as unknown as HTMLCanvasElement;
  vi.stubGlobal('window', { devicePixelRatio: 1 });
  const renderer = new GridRenderer(canvas);
  renderer.resize(100, 100);
  renderer.handleLayoutSnapshot({
    Columns: [{ PaneID: 1, Width: 8, Height: 3 }, { PaneID: 2, Width: 8, Height: 3 }],
    FocusPaneID: 2, PaneStatuses: {}, PaneTitles: {}
  });
  expect(renderer.getPaneHit(0, 1).paneID).toBe(1);
  expect(renderer.getPaneHit(2, 1).paneID).toBe(2);
  expect(renderer.getPaneHit(1, 1).paneID).toBe(0);
  vi.unstubAllGlobals();
});
