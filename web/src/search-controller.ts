import {
  createSearchSession,
  applySnapshot,
  cancelSearch,
  liveSearch,
  type SearchState,
  type ScrollCommand,
} from './search';
import type { MsgHistorySnapshot } from './gen/internal/protocol/wirepb/wideboi_pb';

export interface SearchHost {
  sendScroll(cmd: ScrollCommand): void;
  requestHistory(paneId: number): void;
  focusTerminal(): void;
  focusSearchInput?(): void;
}

export class SearchController {
  private _state: SearchState | null = null;
  private _pendingNav: 0 | 1 | -1 = 0;
  private host: SearchHost;

  constructor(host: SearchHost) {
    this.host = host;
  }

  get state(): SearchState | null {
    return this._state;
  }

  set state(s: SearchState | null) {
    this._state = s;
  }

  get active(): boolean {
    return this._state !== null;
  }

  get paneId(): number | undefined {
    return this._state?.paneId;
  }

  start(paneId: number, priorOffset: number, priorHistoryLen: number, existingQuery = '') {
    this._state = createSearchSession(paneId, priorOffset, priorHistoryLen, existingQuery);
    this._pendingNav = 0;
    this.host.focusSearchInput?.();
  }

  setQuery(query: string) {
    if (!this._state) return;
    this._state = {
      ...this._state,
      query,
      status: 'input',
    };
  }

  commit() {
    if (!this._state || !this._state.query) return;
    this._state = { ...this._state, status: 'searching' };
    this._pendingNav = 0;
    this.host.requestHistory(this._state.paneId);
  }

  navigate(direction: 1 | -1) {
    if (!this._state || !this._state.query) return;
    this._pendingNav = direction;
    this._state = { ...this._state, status: 'searching' };
    this.host.requestHistory(this._state.paneId);
  }

  accept() {
    if (!this._state) return;
    this._state = null;
    this._pendingNav = 0;
    this.host.focusTerminal();
  }

  cancel() {
    if (!this._state) return;
    const cmd = cancelSearch(this._state);
    this.host.sendScroll(cmd);
    this._state = null;
    this._pendingNav = 0;
    this.host.focusTerminal();
  }

  live() {
    if (!this._state) return;
    const cmd = liveSearch(this._state);
    this.host.sendScroll(cmd);
    this._state = null;
    this._pendingNav = 0;
    this.host.focusTerminal();
  }

  handleHistorySnapshot(snapshot: MsgHistorySnapshot): boolean {
    if (!this._state || this._state.paneId !== snapshot.paneId) return false;
    const res = applySnapshot(this._state, snapshot, this._pendingNav);
    this._pendingNav = 0;
    this._state = res.nextState;
    if (res.scrollMsg) {
      this.host.sendScroll(res.scrollMsg);
    }
    return true;
  }

  closeIfMatching(paneId: number) {
    if (this._state?.paneId === paneId) {
      this._state = null;
      this._pendingNav = 0;
    }
  }

  handleInputKeydown(e: KeyboardEvent): boolean {
    if (!this._state) return false;
    if (e.key === 'Enter') {
      e.preventDefault();
      if (this._state.status === 'input') {
        this.commit();
      } else if (this._state.matches.length) {
        this.navigate(e.shiftKey ? -1 : 1);
      }
      return true;
    }
    if (e.key === 'Escape') {
      e.preventDefault();
      this.cancel();
      return true;
    }
    if (e.ctrlKey && (e.key === 'g' || e.code === 'KeyG')) {
      e.preventDefault();
      this.live();
      return true;
    }
    return false;
  }

  handleTerminalKeydown(e: KeyboardEvent): boolean {
    if (!this._state) return false;
    if (e.key === 'Escape') {
      e.preventDefault();
      this.cancel();
      return true;
    }
    if (e.key === 'Enter') {
      e.preventDefault();
      this.accept();
      return true;
    }
    if (e.ctrlKey && (e.key === 'g' || e.code === 'KeyG')) {
      e.preventDefault();
      this.live();
      return true;
    }
    if (e.key === 'n' && !e.ctrlKey && !e.altKey && !e.metaKey) {
      e.preventDefault();
      this.navigate(1);
      return true;
    }
    if (e.key === 'N' && !e.ctrlKey && !e.altKey && !e.metaKey) {
      e.preventDefault();
      this.navigate(-1);
      return true;
    }
    return false;
  }
}
