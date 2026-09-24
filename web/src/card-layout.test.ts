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

  it('can keep the previous focus above a right-to-left slide', () => {
    const layout = cardLayout(columns, 5, 42, 2, 6);
    const oldFocus = layout.placements.find(p => p.paneId === 6)!;
    const newFocus = layout.placements.find(p => p.paneId === 5)!;
    expect(oldFocus.z).toBeGreaterThan(newFocus.z);
  });
});
