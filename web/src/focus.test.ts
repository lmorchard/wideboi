import { describe, expect, it } from 'vitest';
import { reconcileFocus } from './focus';
import type { ColumnData } from './protocol';

const columns = (ids: number[]): ColumnData[] => ids.map(PaneID => ({ PaneID, Width: 40, Height: 20 }));

describe('reconcileFocus', () => {
  it('retains a pane that is still present', () => {
    expect(reconcileFocus(columns([1, 2, 3]), columns([1, 2, 3, 4]), 2)).toBe(2);
  });

  it('moves to the preceding column when focus closes', () => {
    expect(reconcileFocus(columns([1, 2, 3]), columns([1, 2]), 3)).toBe(2);
    expect(reconcileFocus(columns([1, 2, 3]), columns([1, 3]), 2)).toBe(1);
  });

  it('selects the first pane on attach and clears focus when empty', () => {
    expect(reconcileFocus([], columns([1, 2]), 0)).toBe(1);
    expect(reconcileFocus(columns([1]), [], 1)).toBe(0);
  });
});
