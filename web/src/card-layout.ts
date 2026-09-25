export interface CardColumn { paneId: number; width: number }

export interface CardPlacement {
  paneId: number;
  left: number;
  visible: boolean;
  z: number;
}

export interface CardLayout {
  first: number;
  placements: CardPlacement[];
  hiddenLeft: number;
  hiddenRight: number;
}

const MIN_SLIVER_WIDTH = 4;

// Positions are in display cells. The elements retain their chosen card widths;
// later cards cover their predecessors to leave visible slivers.
export function cardLayout(columns: CardColumn[], focusedPaneId: number, viewportWidth: number, previousFirst: number, stackFocusId: number | null = focusedPaneId): CardLayout {
  if (!columns.length || viewportWidth <= 0) {
    return { first: 0, placements: columns.map(c => ({ paneId: c.paneId, left: 0, visible: false, z: 0 })), hiddenLeft: 0, hiddenRight: 0 };
  }

  const focus = Math.max(columns.findIndex(c => c.paneId === focusedPaneId), 0);
  const focusedWidth = Math.min(columns[focus].width, viewportWidth);
  const remaining = Math.max(viewportWidth - focusedWidth, 0);
  const others = columns.length - 1;
  const budget = others && Math.floor(remaining / others) < MIN_SLIVER_WIDTH
    ? Math.floor(remaining / MIN_SLIVER_WIDTH) : others;
  const size = Math.min(budget, others) + 1;
  const margin = size >= 3 ? 1 : 0;
  let first = Math.max(0, Math.min(previousFirst, columns.length - size));
  if (focus - margin < first) first = focus - margin;
  if (focus + margin >= first + size) first = focus + margin - size + 1;
  first = Math.max(0, Math.min(first, columns.length - size));
  const last = first + size - 1;
  const slivers = size - 1;
  let sliverIndex = 0;
  let x = 0;
  const placements = columns.map((column, index) => {
    if (index < first || index > last) return { paneId: column.paneId, left: 0, visible: false, z: 0 };
    const left = x;
    if (index === focus) x += focusedWidth;
    else {
      const share = Math.floor(remaining / slivers) + (sliverIndex < remaining % slivers ? 1 : 0);
      x += Math.min(share, column.width);
      sliverIndex++;
    }
    return { paneId: column.paneId, left, visible: true, z: stackFocusId !== null && column.paneId === stackFocusId ? columns.length + 1 : index + 1 };
  });
  return { first, placements, hiddenLeft: first, hiddenRight: columns.length - last - 1 };
}
