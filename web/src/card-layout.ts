export interface CardColumn { paneId: number; width: number; pinned?: boolean }

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
// later cards cover their predecessors to leave visible slivers. Pinned cards
// anchor to the left edge of the viewport.
export function cardLayout(columns: CardColumn[], focusedPaneId: number, viewportWidth: number, previousFirst: number, stackFocusId: number | null = focusedPaneId): CardLayout {
  if (!columns.length || viewportWidth <= 0) {
    return { first: 0, placements: columns.map(c => ({ paneId: c.paneId, left: 0, visible: false, z: 0 })), hiddenLeft: 0, hiddenRight: 0 };
  }

  const pinnedCols = columns.filter(c => c.pinned);
  const unpinnedCols = columns.filter(c => !c.pinned);

  let pinnedX = 0;
  const pinnedPlacements: CardPlacement[] = [];
  for (const col of pinnedCols) {
    const left = pinnedX;
    pinnedX += col.width + 1;
    pinnedPlacements.push({
      paneId: col.paneId,
      left,
      visible: true,
      z: stackFocusId !== null && col.paneId === stackFocusId ? columns.length + 1 : 1,
    });
  }

  const remainingViewport = Math.max(0, viewportWidth - pinnedX);
  const startX = pinnedX;

  if (!unpinnedCols.length || remainingViewport <= 0) {
    const placementMap = new Map(pinnedPlacements.map(p => [p.paneId, p]));
    return {
      first: 0,
      placements: columns.map(c => placementMap.get(c.paneId) ?? { paneId: c.paneId, left: 0, visible: false, z: 0 }),
      hiddenLeft: 0,
      hiddenRight: 0,
    };
  }

  if (unpinnedCols.length === 1) {
    const col = unpinnedCols[0];
    const isFocused = stackFocusId !== null && col.paneId === stackFocusId;
    const unpinnedPlacements: CardPlacement[] = [{
      paneId: col.paneId,
      left: startX,
      visible: true,
      z: isFocused ? columns.length + 1 : 1,
    }];
    const placementMap = new Map([...pinnedPlacements, ...unpinnedPlacements].map(p => [p.paneId, p]));
    return {
      first: 0,
      placements: columns.map(c => placementMap.get(c.paneId) ?? { paneId: c.paneId, left: 0, visible: false, z: 0 }),
      hiddenLeft: 0,
      hiddenRight: 0,
    };
  }

  const unpinnedFocusIdx = unpinnedCols.findIndex(c => c.paneId === focusedPaneId);
  const isPinnedFocused = unpinnedFocusIdx < 0;
  const activeFocusIdx = isPinnedFocused
    ? Math.max(0, Math.min(previousFirst, unpinnedCols.length - 1))
    : unpinnedFocusIdx;

  const focusedWidth = Math.min(unpinnedCols[activeFocusIdx].width, remainingViewport);
  const remaining = Math.max(remainingViewport - focusedWidth, 0);
  const others = unpinnedCols.length - 1;
  const budget = others && Math.floor(remaining / others) < MIN_SLIVER_WIDTH
    ? Math.floor(remaining / MIN_SLIVER_WIDTH) : others;
  const size = Math.min(budget, others) + 1;
  const margin = size >= 3 ? 1 : 0;
  const lo = Math.max(activeFocusIdx - margin, 0);
  const hi = Math.min(activeFocusIdx + margin, unpinnedCols.length - 1);
  let first = Math.max(0, Math.min(previousFirst, unpinnedCols.length - size));
  if (!isPinnedFocused) {
    if (lo < first) first = lo;
    if (hi > first + size - 1) first = hi - size + 1;
  }
  first = Math.max(0, Math.min(first, unpinnedCols.length - size));
  const last = first + size - 1;
  const slivers = size - 1;
  let sliverIndex = 0;
  let x = startX;
  const unpinnedPlacements = unpinnedCols.map((column, index) => {
    if (index < first || index > last) return { paneId: column.paneId, left: 0, visible: false, z: 0 };
    const left = x;
    if (index === activeFocusIdx) x += focusedWidth;
    else {
      const share = Math.floor(remaining / slivers) + (sliverIndex < remaining % slivers ? 1 : 0);
      x += Math.min(share, column.width);
      sliverIndex++;
    }
    return {
      paneId: column.paneId,
      left,
      visible: true,
      z: stackFocusId !== null && column.paneId === stackFocusId ? columns.length + 1 : index + 1,
    };
  });

  const placementMap = new Map([...pinnedPlacements, ...unpinnedPlacements].map(p => [p.paneId, p]));
  const placements = columns.map(c => placementMap.get(c.paneId) ?? { paneId: c.paneId, left: 0, visible: false, z: 0 });
  return { first, placements, hiddenLeft: first, hiddenRight: unpinnedCols.length - last - 1 };
}
