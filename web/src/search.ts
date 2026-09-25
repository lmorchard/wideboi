export interface HistoryMatch {
  row: number;
  col: number;
}

export type SearchStatus = 'input' | 'searching' | 'navigating' | 'no_match';

export interface SearchState {
  paneId: number;
  priorOffset: number;
  priorHistoryLen: number;
  query: string;
  status: SearchStatus;
  matches: HistoryMatch[];
  selectedIndex: number;
}

export interface ScrollCommand {
  paneId: number;
  offset: number;
  anchorHistory: boolean;
  historyLen: number;
}

export function runeCount(str: string): number {
  return Array.from(str).length;
}

export function findHistoryMatches(rows: string[], query: string): HistoryMatch[] {
  if (!query) return [];
  const matches: HistoryMatch[] = [];
  for (let y = 0; y < rows.length; y++) {
    const row = rows[y];
    let start = 0;
    while (start < row.length) {
      const idx = row.indexOf(query, start);
      if (idx === -1) break;
      const col = runeCount(row.slice(0, idx));
      matches.push({ row: y, col });
      start = idx + query.length;
    }
  }
  return matches;
}

export function computeScrollTarget(scrollbackLen: number, matchRow: number): number {
  const target = scrollbackLen - matchRow;
  return Math.max(0, Math.min(target, scrollbackLen));
}

export function navigateIndex(currentIndex: number, totalMatches: number, direction: 1 | -1 | 0): number {
  if (totalMatches <= 0) return -1;
  if (currentIndex < 0 || direction === 0) return totalMatches - 1;
  return (currentIndex + direction + totalMatches) % totalMatches;
}

export function createSearchSession(
  paneId: number,
  priorOffset: number,
  priorHistoryLen: number,
  initialQuery = ''
): SearchState {
  return {
    paneId,
    priorOffset,
    priorHistoryLen,
    query: initialQuery,
    status: 'input',
    matches: [],
    selectedIndex: -1,
  };
}

export function applySnapshot(
  state: SearchState,
  snapshot: { scrollbackLen: number; rows: string[] },
  direction: 1 | -1 | 0
): { nextState: SearchState; scrollMsg?: ScrollCommand } {
  const matches = findHistoryMatches(snapshot.rows, state.query);
  if (matches.length === 0) {
    return {
      nextState: {
        ...state,
        status: 'no_match',
        matches: [],
        selectedIndex: -1,
      },
    };
  }
  const nextIdx = navigateIndex(state.selectedIndex, matches.length, direction);
  const match = matches[nextIdx];
  const offset = computeScrollTarget(snapshot.scrollbackLen, match.row);
  return {
    nextState: {
      ...state,
      status: 'navigating',
      matches,
      selectedIndex: nextIdx,
    },
    scrollMsg: {
      paneId: state.paneId,
      offset,
      anchorHistory: true,
      historyLen: snapshot.scrollbackLen,
    },
  };
}

export function cancelSearch(state: SearchState): ScrollCommand {
  return {
    paneId: state.paneId,
    offset: state.priorOffset,
    anchorHistory: state.priorOffset > 0,
    historyLen: state.priorHistoryLen,
  };
}

export function liveSearch(state: SearchState): ScrollCommand {
  return {
    paneId: state.paneId,
    offset: 0,
    anchorHistory: false,
    historyLen: 0,
  };
}

export function formatSearchStatus(state: SearchState): string {
  if (state.status === 'input') {
    return 'Enter find · Esc cancel';
  }
  if (state.status === 'searching') {
    return 'searching…';
  }
  if (state.status === 'no_match' || state.matches.length === 0) {
    return 'no match · Esc restore · Ctrl+g live';
  }
  const m = state.matches[state.selectedIndex];
  if (!m) return '';
  return `${state.selectedIndex + 1}/${state.matches.length} row ${m.row + 1} char ${m.col + 1} · n/N next/prev · Enter keep · Esc restore · Ctrl+g live`;
}
