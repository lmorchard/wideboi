import { describe, expect, it } from 'vitest';
import { cardLayout } from './card-layout';

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
});
