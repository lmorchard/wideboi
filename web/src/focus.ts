import type { ColumnData } from './gen/internal/protocol/wirepb/wideboi_pb';

export function reconcileFocus(previous: ColumnData[], next: ColumnData[], focusedPaneId: number): number {
  if (next.some(c => c.paneId === focusedPaneId)) return focusedPaneId;
  if (next.length === 0) return 0;

  const oldIndex = previous.findIndex(c => c.paneId === focusedPaneId);
  const nextIndex = Math.min(Math.max(oldIndex - 1, 0), next.length - 1);
  return next[nextIndex].paneId;
}
