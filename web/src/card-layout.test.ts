import { describe, expect, it } from 'vitest';
import { cardLayout } from './card-layout';
import goldenCases from './fixtures/card-layout-golden.json';

const columns = [1, 2, 3, 4, 5, 6].map(paneId => ({ paneId, width: 30 }));

describe('card layout', () => {
  it('keeps a full focused pane and contiguous visible neighbors', () => {
    const layout = cardLayout(columns, 3, 42, 0);
    const visible = layout.placements.filter(p => p.visible);
    expect(visible.map(p => p.paneId)).toEqual([1, 2, 3, 4]);
    expect(visible.map(p => p.left)).toEqual([0, 4, 8, 38]);
    expect(layout.hiddenRight).toBe(2);
    expect(visible[2].z).toBeGreaterThan(visible[1].z);
  });

  it('moves its window to keep distant focus reachable', () => {
    const layout = cardLayout(columns, 6, 42, 0);
    expect(layout.placements.filter(p => p.visible).map(p => p.paneId)).toEqual([3, 4, 5, 6]);
    expect(layout.first).toBe(2);
    expect(layout.hiddenLeft).toBe(2);
  });

  it('shows just the focused pane when it fills the viewport', () => {
    const layout = cardLayout(columns, 5, 25, 0);
    expect(layout.placements.filter(p => p.visible).map(p => p.paneId)).toEqual([5]);
  });

  it('stacks cards from left to right during a focus slide', () => {
    for (const focus of [4, 5]) {
      const layout = cardLayout(columns, focus, 42, 2, null);
      const z = [4, 5, 6].map(paneId => layout.placements.find(p => p.paneId === paneId)!.z);
      expect(z[0]).toBeLessThan(z[1]);
      expect(z[1]).toBeLessThan(z[2]);
    }
  });

  it('anchors pinned columns to the left while fanning unpinned cards', () => {
    const cols = [
      { paneId: 1, width: 28, pinned: true },
      { paneId: 2, width: 60 },
      { paneId: 3, width: 60 },
    ];
    // Viewport width 120. Pinned col 1 takes left 0, width 28 + divider (29 total).
    // Focused unpinned col 2 takes left 29, width 60.
    const layout = cardLayout(cols, 2, 120, 0);
    const p1 = layout.placements.find(p => p.paneId === 1)!;
    const p2 = layout.placements.find(p => p.paneId === 2)!;
    expect(p1.left).toBe(0);
    expect(p1.visible).toBe(true);
    expect(p2.left).toBe(29);
    expect(p2.visible).toBe(true);
  });

  it('anchors collapsed columns to the right while fanning unpinned cards', () => {
    const cols = [
      { paneId: 1, width: 28, pinned: true },
      { paneId: 2, width: 60 },
      { paneId: 3, width: 60, collapsed: true },
    ];
    // Viewport width 120. Pinned col 1 takes left 0, width 28 + divider (29 total).
    // Collapsed col 3 takes left 120 - 4 = 116.
    // Focused unpinned col 2 takes left 29, width 60.
    const layout = cardLayout(cols, 2, 120, 0);
    const p1 = layout.placements.find(p => p.paneId === 1)!;
    const p2 = layout.placements.find(p => p.paneId === 2)!;
    const p3 = layout.placements.find(p => p.paneId === 3)!;
    expect(p1.left).toBe(0);
    expect(p1.visible).toBe(true);
    expect(p3.left).toBe(116);
    expect(p3.visible).toBe(true);
    expect(p2.left).toBe(29);
    expect(p2.visible).toBe(true);
  });

  describe('golden fixtures generated from Go', () => {
    for (const tc of goldenCases) {
      it(`matches Go layout for "${tc.name}"`, () => {
        const result = cardLayout(tc.columns, tc.focusedPaneId, tc.viewportWidth, tc.previousFirst);
        expect(result.first).toBe(tc.first);
        expect(result.hiddenLeft).toBe(tc.hiddenLeft);
        expect(result.hiddenRight).toBe(tc.hiddenRight);
        expect(result.placements).toEqual(tc.placements);
      });
    }
  });
});
