import { reconcileFocus } from './focus';
import type { ColumnData, MsgLayoutSnapshot, MsgPaneMetadata, PaneStatus } from './gen/internal/protocol/wirepb/wideboi_pb';
import type { Macro } from './macros';

export interface SessionState {
  readonly columns: readonly ColumnData[];
  readonly activePanes: readonly number[];
  readonly focusedPaneId: number;
  readonly previousFocusId: number;
  readonly stackFocusId: number | null;
  readonly focusTransition: number;
  readonly pendingFocusId: number;
  readonly displayWidths: Readonly<Record<number, number>>;
  readonly paneStatuses: Readonly<Record<number, PaneStatus>>;
  readonly paneTitles: Readonly<Record<number, string>>;
  readonly paneMetadata: Readonly<Record<number, MsgPaneMetadata>>;
  readonly macros: readonly Macro[];
}

export type SessionAction =
  | {
      type: 'layoutSnapshot';
      snapshot: MsgLayoutSnapshot;
      followPTY?: boolean;
      layoutMode?: 'cards' | 'scroll';
      mobile?: boolean;
    }
  | {
      type: 'paneClosed';
      paneId: number;
      layoutMode?: 'cards' | 'scroll';
      mobile?: boolean;
    }
  | { type: 'paneCreated'; paneId: number }
  | { type: 'splitResponse'; paneId: number }
  | { type: 'paneMetadata'; metadata: MsgPaneMetadata }
  | { type: 'macrosSnapshot'; macros: Macro[] }
  | {
      type: 'focusPane';
      paneId: number;
      layoutMode?: 'cards' | 'scroll';
      mobile?: boolean;
    }
  | { type: 'finishFocusStack'; paneId: number; transition: number }
  | { type: 'setDisplayWidth'; paneId: number; width: number }
  | { type: 'resetDisplayWidths' }
  | { type: 'setLayoutMode'; mode: 'cards' | 'scroll' }
  | { type: 'reset' };

export function initialSessionState(initialMacros: readonly Macro[] = []): SessionState {
  return {
    columns: [],
    activePanes: [],
    focusedPaneId: 0,
    previousFocusId: 0,
    stackFocusId: 0,
    focusTransition: 0,
    pendingFocusId: 0,
    displayWidths: {},
    paneStatuses: {},
    paneTitles: {},
    paneMetadata: {},
    macros: initialMacros,
  };
}

export function reduceSession(state: SessionState, action: SessionAction): SessionState {
  switch (action.type) {
    case 'layoutSnapshot': {
      const snapshot = action.snapshot;
      const previousFocus = state.focusedPaneId;
      let nextFocus = reconcileFocus(state.columns, snapshot.columns, state.focusedPaneId);
      const nextColumns = snapshot.columns ?? [];
      const nextActivePanes = nextColumns.map((c: ColumnData) => c.paneId);

      const widths: Record<number, number> = { ...state.displayWidths };
      for (const column of nextColumns) {
        if (widths[column.paneId] === undefined || action.followPTY) {
          widths[column.paneId] = column.width;
        }
      }
      for (const id of Object.keys(widths)) {
        if (!nextColumns.some((column: ColumnData) => column.paneId === Number(id))) {
          delete widths[Number(id)];
        }
      }

      let stackFocusId = state.stackFocusId;
      let focusTransition = state.focusTransition;

      if (nextFocus !== previousFocus) {
        stackFocusId = nextFocus;
        focusTransition++;
      } else if (stackFocusId !== null && !nextActivePanes.includes(stackFocusId)) {
        stackFocusId = nextFocus;
      }

      let pendingFocusId = state.pendingFocusId;
      let previousFocusId = state.previousFocusId;
      if (pendingFocusId && nextActivePanes.includes(pendingFocusId)) {
        if (pendingFocusId !== nextFocus) {
          if (action.layoutMode === 'cards' && !action.mobile) {
            stackFocusId = null;
          }
          previousFocusId = nextFocus;
          nextFocus = pendingFocusId;
          focusTransition++;
        }
        pendingFocusId = 0;
      }

      if (!nextActivePanes.includes(previousFocusId)) {
        previousFocusId = 0;
      }

          const activeSet = new Set(nextActivePanes);
          const nextMeta: Record<number, MsgPaneMetadata> = {};
          for (const [id, meta] of Object.entries(state.paneMetadata)) {
        if (activeSet.has(Number(id))) {
          nextMeta[Number(id)] = meta;
        }
      }

      return {
        ...state,
        columns: nextColumns,
        activePanes: nextActivePanes,
        focusedPaneId: nextFocus,
        previousFocusId,
        stackFocusId,
        focusTransition,
        pendingFocusId,
        displayWidths: widths,
        paneStatuses: snapshot.paneStatuses ?? {},
        paneTitles: snapshot.paneTitles ?? {},
        paneMetadata: nextMeta,
      };
    }

    case 'paneClosed': {
      const closedId = action.paneId;
      const previousColumns = state.columns;
      const nextColumns = previousColumns.filter(column => column.paneId !== closedId);
      const nextActivePanes = nextColumns.map(column => column.paneId);
      const nextFocus = reconcileFocus(previousColumns, nextColumns, state.focusedPaneId);
      const focusChanged = nextFocus !== state.focusedPaneId;

      let stackFocusId = state.stackFocusId;
      let focusTransition = state.focusTransition;
      if (stackFocusId === closedId || focusChanged) {
        stackFocusId = action.layoutMode === 'cards' && !action.mobile && focusChanged ? null : nextFocus;
        focusTransition++;
      }

      const displayWidths = { ...state.displayWidths };
      delete displayWidths[closedId];

      const paneMetadata = { ...state.paneMetadata };
      delete paneMetadata[closedId];

      return {
        ...state,
        columns: nextColumns,
        activePanes: nextActivePanes,
        focusedPaneId: nextFocus,
        previousFocusId: state.previousFocusId === closedId ? 0 : state.previousFocusId,
        pendingFocusId: state.pendingFocusId === closedId ? 0 : state.pendingFocusId,
        stackFocusId,
        focusTransition,
        displayWidths,
        paneMetadata,
      };
    }

    case 'paneCreated': {
      return {
        ...state,
        pendingFocusId: action.paneId,
      };
    }

    case 'splitResponse': {
      if (!action.paneId) return state;
      return {
        ...state,
        pendingFocusId: action.paneId,
      };
    }

    case 'paneMetadata': {
      return {
        ...state,
        paneMetadata: {
          ...state.paneMetadata,
          [action.metadata.paneId]: action.metadata,
        },
      };
    }

    case 'macrosSnapshot': {
      return {
        ...state,
        macros: action.macros,
      };
    }

    case 'focusPane': {
      const paneId = action.paneId;
      if (!state.activePanes.includes(paneId)) return state;
      if (paneId === state.focusedPaneId) return state;

      return {
        ...state,
        focusedPaneId: paneId,
        previousFocusId: state.focusedPaneId,
        stackFocusId: action.layoutMode === 'cards' && !action.mobile ? null : state.stackFocusId,
        focusTransition: state.focusTransition + 1,
      };
    }

    case 'finishFocusStack': {
      if (state.focusTransition !== action.transition) return state;
      if (!state.activePanes.includes(action.paneId)) return state;
      return {
        ...state,
        stackFocusId: action.paneId,
      };
    }

    case 'setDisplayWidth': {
      return {
        ...state,
        displayWidths: {
          ...state.displayWidths,
          [action.paneId]: action.width,
        },
      };
    }

    case 'resetDisplayWidths': {
      const widths: Record<number, number> = {};
      for (const col of state.columns) {
        widths[col.paneId] = col.width;
      }
      return {
        ...state,
        displayWidths: widths,
      };
    }

    case 'setLayoutMode': {
      return {
        ...state,
        stackFocusId: action.mode === 'cards' ? state.focusedPaneId : state.stackFocusId,
        focusTransition: state.focusTransition + 1,
      };
    }

    case 'reset': {
      return initialSessionState(state.macros);
    }
  }
}
