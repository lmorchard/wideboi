import { LitElement, html, css } from 'lit';
import { customElement, query, state } from 'lit/decorators.js';
import { repeat } from 'lit/directives/repeat.js';
import { WideboiClient } from './client';
import { CELL_HEIGHT, measureCellWidth, PaneStore, selectionText, type CellPoint } from './pane-state';
import { reconcileFocus } from './focus';
import { WideboiPane } from './wideboi-pane';
import { cardLayout } from './card-layout';
import { sendKeyboardInput, sendTextInput } from './input';
import { KeyRouter } from './key-router';
import { consumeLinkToken } from './token';
import { RenderStats, formatSummary, statsEnabled } from './stats';
import { MouseKind, MsgHistorySnapshot, MsgPaneMetadata, PaneStatus, VerbType, type ColumnData } from './gen/internal/protocol/wirepb/wideboi_pb';
import { createSearchSession, applySnapshot, cancelSearch, liveSearch, formatSearchStatus, type SearchState } from './search';

const linkToken = consumeLinkToken(window.location, window.history);
const STATS_REPORT_MS = 5000;

@customElement('wideboi-app')
export class WideboiApp extends LitElement {
  static styles = css`
    :host {
      display: flex;
      flex-direction: column;
      z-index: 20;
      width: 100vw;
      height: 100vh;
      overflow: hidden;
      background: #1e1e1e;
      position: relative;
    }
    .terminal-shell {
      display: flex;
      flex: 1;
      flex-direction: column;
      min-height: 0;
      min-width: 0;
      font: 14px monospace;
      color: #ccc;
      position: relative;
    }
    .title, .status {
      height: 16.8px;
      line-height: 16.8px;
      flex: none;
      overflow: hidden;
      white-space: nowrap;
      text-overflow: ellipsis;
    }
    .status.search-bar {
      position: absolute;
      bottom: 0;
      left: 0;
      right: 0;
      height: 22px;
      line-height: 22px;
      display: flex;
      align-items: center;
      gap: 0.4rem;
      padding: 0 0.5rem;
      background: #252526;
      border-top: 1px solid #3c3c3c;
      overflow: hidden;
      font: 12px monospace;
      color: #ccc;
      z-index: 15;
    }
    .status.search-bar input.search-input {
      background: #1e1e1e;
      border: 1px solid #555;
      color: #ccc;
      padding: 1px 4px;
      font: 12px monospace;
      border-radius: 2px;
      outline: none;
      height: 18px;
      box-sizing: border-box;
      width: 140px;
    }
    .status.search-bar input.search-input:focus {
      border-color: #007fd4;
    }
    .status.search-bar button.search-btn {
      background: #3c3c3c;
      color: #ccc;
      border: 1px solid #555;
      padding: 0 5px;
      height: 18px;
      line-height: 16px;
      font-size: 11px;
      border-radius: 2px;
      cursor: pointer;
    }
    .status.search-bar button.search-btn:hover:not(:disabled) {
      background: #4c4c4c;
      color: #fff;
    }
    .status.search-bar button.search-btn:disabled {
      opacity: 0.5;
      cursor: default;
    }
    .status.search-bar .search-msg {
      margin-left: 0.4rem;
      color: #aaa;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
    .pane-strip {
      display: flex;
      flex: 1;
      min-height: 0;
      min-width: 0;
      overflow-x: auto;
      overflow-y: hidden;
      scrollbar-width: thin;
      scrollbar-gutter: stable;
      overscroll-behavior-x: contain;
      background: #1e1e1e;
    }
    .pane-strip.cards {
      position: relative;
      overflow: hidden;
    }
    .pane-strip.cards wideboi-pane { position: absolute; top: 0; }
    .card-count {
      position: absolute;
      bottom: 0;
      z-index: 1000;
      padding: 2px 5px;
      background: #252526;
      color: #ccc;
      pointer-events: none;
    }
    .card-count.right { right: 0; }

    .toolbar {
      background: #252526;
      border-bottom: 1px solid #3c3c3c;
      padding: 0.4rem 0.6rem;
      display: flex;
      flex-wrap: wrap;
      gap: 0.4rem 0.5rem;
      align-items: center;
      white-space: nowrap;
      flex: none;
      z-index: 5;
      font-size: 13px;
    }
    .toolbar select {
      background: #3c3c3c;
      color: #cccccc;
      border: 1px solid #555;
      padding: 0.25rem;
      border-radius: 3px;
      outline: none;
    }
    .claim-size-btn {
      background: #3c3c3c;
      color: #cccccc;
      border: 1px solid #555;
      padding: 0.3rem 0.6rem;
      border-radius: 3px;
      cursor: pointer;
      font-size: 13px;
    }
    .claim-size-btn:hover {
      background: #4c4c4c;
      color: #ffffff;
    }
    .toolbar label {
      color: #aaa;
      white-space: nowrap;
    }
    .toolbar .tip {
      color: #666;
      margin-left: auto;
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
    }
    @media (max-width: 650px) {
      .toolbar .tip { display: none; }
    }
    .overlay {
      position: absolute;
      top: 0; left: 0; right: 0; bottom: 0;
      background: rgba(30, 30, 30, 0.85);
      display: flex;
      flex-direction: column;
      z-index: 20;
      align-items: center;
      justify-content: center;
      z-index: 10;
      color: #cccccc;
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
    }
    .connection-box {
      background: #252526;
      padding: 2rem;
      border-radius: 6px;
      box-shadow: 0 4px 12px rgba(0,0,0,0.5);
      border: 1px solid #3c3c3c;
      display: flex;
      flex-direction: column;
      z-index: 20;
      gap: 1rem;
      width: 320px;
    }
    .connection-box h2 {
      margin: 0;
      font-size: 1.1rem;
      font-weight: 500;
    }
    input {
      background: #3c3c3c;
      border: 1px solid #3c3c3c;
      color: #cccccc;
      padding: 0.6rem;
      font-size: 1rem;
      border-radius: 3px;
      outline: none;
      transition: border-color 0.2s;
    }
    input:focus {
      border: 1px solid #007fd4;
    }
    button {
      background: #0e639c;
      color: white;
      border: none;
      padding: 0.6rem;
      font-size: 1rem;
      cursor: pointer;
      border-radius: 3px;
      transition: background 0.2s;
    }
    button:hover {
      background: #1177bb;
    }
    .stats-overlay {
      position: fixed;
      right: 0.5rem;
      bottom: 1.5rem;
      margin: 0;
      padding: 0.4rem 0.6rem;
      background: rgba(37, 37, 38, 0.9);
      border: 1px solid #3c3c3c;
      color: #cccccc;
      font: 11px monospace;
      z-index: 30;
      pointer-events: none;
    }
    .help-overlay {
      position: absolute;
      top: 0; left: 0; right: 0; bottom: 0;
      background: rgba(0, 0, 0, 0.75);
      display: flex;
      align-items: center;
      justify-content: center;
      z-index: 25;
      color: #cccccc;
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, monospace;
    }
    .help-dialog {
      background: #252526;
      border: 1px solid #454545;
      border-radius: 6px;
      padding: 1.5rem;
      max-width: 580px;
      width: 90%;
      max-height: 85vh;
      overflow-y: auto;
      box-shadow: 0 8px 24px rgba(0,0,0,0.6);
    }
    .help-dialog h3 {
      margin: 0 0 1rem;
      font-size: 1.1rem;
      font-weight: 600;
      display: flex;
      justify-content: space-between;
      align-items: center;
      border-bottom: 1px solid #3c3c3c;
      padding-bottom: 0.5rem;
    }
    .help-dialog .close-btn {
      background: transparent;
      border: none;
      color: #999;
      font-size: 1.2rem;
      cursor: pointer;
      padding: 0 0.5rem;
    }
    .help-dialog .close-btn:hover {
      color: #fff;
    }
    .help-table {
      width: 100%;
      border-collapse: collapse;
      font-size: 13px;
      margin-bottom: 1rem;
    }
    .help-table th, .help-table td {
      padding: 4px 8px;
      text-align: left;
    }
    .help-table th {
      color: #888;
      font-weight: normal;
      border-bottom: 1px solid #3c3c3c;
    }
    .help-table kbd {
      background: #333;
      border: 1px solid #555;
      border-radius: 3px;
      padding: 1px 5px;
      font-family: monospace;
      color: #eee;
    }
    .toolbar .help-btn {
      background: #333;
      color: #ccc;
      border: 1px solid #555;
      padding: 0.25rem 0.6rem;
      font-size: 12px;
      border-radius: 3px;
      cursor: pointer;
    }
    .toolbar .help-btn:hover {
      background: #444;
      color: #fff;
    }
    .error {
      color: #f14c4c;
      font-size: 0.9rem;
      margin-top: -0.5rem;
    }
  `;

  @query('.pane-strip')
  private paneStrip!: HTMLElement;

  private client: WideboiClient | null = null;
  // Present only with ?stats=1; everything downstream treats undefined as off.
  private readonly stats = statsEnabled(window.location.search) ? new RenderStats() : undefined;
  private panes = new PaneStore(this.stats);
  private resizeObserver: ResizeObserver;
  private cellWidth = 1;
  private statsTimer?: ReturnType<typeof setInterval>;

  @state()
  private statsText = '';

  @state()
  private connected = false;

  @state()
  private activePanes: number[] = [];

  @state()
  private columns: ColumnData[] = [];

  @state()
  private paneTitles: Record<number, string> = {};

  @state()
  private paneMetadata: Record<number, MsgPaneMetadata> = {};

  @state()
  private focusedPaneId = 0;

  @state()
  private wsUrl = `${window.location.protocol === 'https:' ? 'wss:' : 'ws:'}//${window.location.host}/ws`;

  @state()
  private token = linkToken;

  @state()
  private errorMsg = '';

  @state()
  private showHelp = false;

  @state()
  private prefixSetting = (() => {
    try {
      return localStorage.getItem('wideboi.prefix') || 'ctrl+b';
    } catch {
      return 'ctrl+b';
    }
  })();

  private keyRouter = new KeyRouter(this.prefixSetting);
  private previousFocusId = 0;
  private pendingFocusId = 0;
  @state()
  private paneStatuses: Record<number, PaneStatus> = {};
  private listeners?: AbortController;
  private pointer?: { id: number; pane: WideboiPane; button: number; tracking: boolean; focusOnClick: boolean; dragged: boolean; start: CellPoint };
  private selectedPane?: WideboiPane;
  private movement = new Map<number, Animation>();
  private lastSentSize?: { cols: number; rows: number };
  @state() private layoutMode: 'scroll' | 'cards' = 'cards';
  @state() private displayWidths: Record<number, number> = {};
  @state() private followPTY = false;
  private pendingReveal = new Set<number>();
  @state() private searchState: SearchState | null = null;
  private pendingNav: 0 | 1 | -1 = 0;
  private cardFirst = 0;
  private cardViewportWidth = 0;
  private stripScrollLeft = 0;
  private stackFocusId: number | null = 0;
  private focusTransition = 0;

  private finishFocusStack(paneID: number, transition: number) {
    const animations = Array.from(this.movement.values()).filter(animation =>
      animation.playState === 'running' || animation.playState === 'paused');
    void Promise.allSettled(animations.map(animation => animation.finished)).then(() => {
      if (this.layoutMode !== 'cards' || this.focusedPaneId !== paneID || this.focusTransition !== transition) return;
      if (Array.from(this.movement.values()).some(animation =>
        animation.playState === 'running' || animation.playState === 'paused')) {
        this.finishFocusStack(paneID, transition);
        return;
      }
      this.stackFocusId = paneID;
      this.requestUpdate();
    });
  }

  public startSearch = () => {
    if (!this.focusedPaneId) return;
    const fp = this.panes.get(this.focusedPaneId);
    const priorOffset = fp?.scrollOffset ?? 0;
    const priorHistoryLen = fp?.scrollbackLen ?? 0;
    const query = this.searchState?.query || '';
    this.searchState = createSearchSession(this.focusedPaneId, priorOffset, priorHistoryLen, query);
    this.pendingNav = 0;
    void this.updateComplete.then(() => {
      const input = this.shadowRoot?.querySelector<HTMLInputElement>('.search-input');
      if (input) {
        input.focus();
        input.select();
      }
    });
  };

  public commitSearch() {
    if (!this.searchState || !this.searchState.query || !this.client) return;
    this.searchState = { ...this.searchState, status: 'searching' };
    this.pendingNav = 0;
    this.client.send({ case: 'historyRequest', value: { paneId: this.searchState.paneId } });
  }

  public navigateSearch(direction: 1 | -1) {
    if (!this.searchState || !this.searchState.query || !this.client) return;
    this.pendingNav = direction;
    this.searchState = { ...this.searchState, status: 'searching' };
    this.client.send({ case: 'historyRequest', value: { paneId: this.searchState.paneId } });
  }

  public acceptSearch() {
    if (!this.searchState) return;
    this.searchState = null;
    this.pendingNav = 0;
    this.focusedPane()?.focusInput();
  }

  public cancelSearch() {
    if (!this.searchState || !this.client) return;
    const cmd = cancelSearch(this.searchState);
    this.client.send({
      case: 'scroll',
      value: {
        paneId: cmd.paneId,
        delta: 0,
        setAbsolute: true,
        offset: cmd.offset,
        anchorHistory: cmd.anchorHistory,
        historyLen: cmd.historyLen,
      },
    });
    this.searchState = null;
    this.pendingNav = 0;
    this.focusedPane()?.focusInput();
  }

  public liveSearch() {
    if (!this.searchState || !this.client) return;
    const cmd = liveSearch(this.searchState);
    this.client.send({
      case: 'scroll',
      value: {
        paneId: cmd.paneId,
        delta: 0,
        setAbsolute: true,
        offset: cmd.offset,
        anchorHistory: cmd.anchorHistory,
        historyLen: cmd.historyLen,
      },
    });
    this.searchState = null;
    this.pendingNav = 0;
    this.focusedPane()?.focusInput();
  }

  private handleHistorySnapshot(snapshot: MsgHistorySnapshot) {
    if (!this.searchState || this.searchState.paneId !== snapshot.paneId || !this.client) return;
    const res = applySnapshot(this.searchState, snapshot, this.pendingNav);
    this.pendingNav = 0;
    this.searchState = res.nextState;
    if (res.scrollMsg) {
      this.client.send({
        case: 'scroll',
        value: {
          paneId: res.scrollMsg.paneId,
          delta: 0,
          setAbsolute: true,
          offset: res.scrollMsg.offset,
          anchorHistory: res.scrollMsg.anchorHistory,
          historyLen: res.scrollMsg.historyLen,
        },
      });
    }
  }

  private focusPane(paneID: number) {
    if (!this.activePanes.includes(paneID)) return;
    if (this.searchState && this.searchState.paneId !== paneID) {
      this.cancelSearch();
    }
    if (paneID === this.focusedPaneId) {
      void this.updateComplete.then(() => {
        this.focusedPane()?.focusInput();
        this.revealFocus();
      });
      return;
    }
    const previous = this.panePositions();
    const transition = ++this.focusTransition;
    if (this.layoutMode === 'cards') this.stackFocusId = null;
    this.previousFocusId = this.focusedPaneId;
    this.focusedPaneId = paneID;
    void this.updateComplete.then(() => {
      if (this.layoutMode === 'cards') {
        this.animateReorder(previous);
        this.finishFocusStack(paneID, transition);
      }
      this.focusedPane()?.focusInput();
      this.revealFocus();
    });
  }

  private focusedPane(): WideboiPane | undefined {
    return Array.from(this.paneStrip?.querySelectorAll('wideboi-pane') || [])
      .find(element => element.paneId === this.focusedPaneId);
  }

  private revealPendingCursor(paneID: number) {
    if (!this.pendingReveal.delete(paneID)) return;
    void this.updateComplete.then(() => Array.from(this.paneStrip.querySelectorAll('wideboi-pane'))
      .find(pane => pane.paneId === paneID)?.revealCursor());
  }

  private editWidth(width: number) {
    const id = this.focusedPaneId;
    if (!id || !Number.isInteger(width) || width < 20 || width > 4096) return;
    this.followPTY = false;
    this.displayWidths = { ...this.displayWidths, [id]: width };
    this.client?.send({ case: 'setPaneWidth', value: { paneId: id, width } });
  }

  private cycleWidth(verb: VerbType) {
    const current = this.displayWidths[this.focusedPaneId];
    if (!current) return;
    let next = current;
    if (verb === VerbType.CYCLE_WIDTH) next = [40, 60, 80].find(width => width > current) ?? 40;
    if (verb === VerbType.GROW_WIDTH) next = Math.min(4096, current + 10);
    if (verb === VerbType.SHRINK_WIDTH) next = Math.max(20, current - 10);
    this.editWidth(next);
  }

  private revealFocus() {
    if (this.layoutMode === 'cards') return;
    const pane = this.focusedPane();
    if (!pane) return;
    pane.scrollIntoView({ block: 'nearest', inline: 'nearest',
      behavior: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth' });
  }

  private panePositions(): Map<number, number> {
    return new Map(Array.from(this.paneStrip?.querySelectorAll('wideboi-pane') || [])
      .map(pane => [pane.paneId, pane.getBoundingClientRect().left]));
  }

  private animateReorder(previous: Map<number, number>) {
    for (const animation of this.movement.values()) animation.cancel();
    this.movement.clear();
    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) return;
    for (const pane of this.paneStrip.querySelectorAll('wideboi-pane')) {
      const oldLeft = previous.get(pane.paneId);
      if (oldLeft === undefined) continue;
      const offset = oldLeft - pane.getBoundingClientRect().left;
      if (Math.abs(offset) < 1) continue;
      const animation = pane.animate(
        [{ transform: `translateX(${offset}px)` }, { transform: 'translateX(0)' }],
        { duration: 180, easing: 'ease-out' });
      this.movement.set(pane.paneId, animation);
      animation.onfinish = () => this.movement.delete(pane.paneId);
    }
  }

  private getGridSize() {
    return {
      cols: Math.floor(this.paneStrip.clientWidth / this.cellWidth),
      rows: Math.floor(this.paneStrip.clientHeight / CELL_HEIGHT) + 2,
    };
  }

  private sendResizeIfChanged() {
    if (!this.client || !this.connected || !this.lastSentSize) return;
    const size = this.getGridSize();
    if (size.cols > 0 && size.rows > 2 &&
        (size.cols !== this.lastSentSize.cols || size.rows !== this.lastSentSize.rows)) {
      this.client.send({ case: 'resize', value: size });
      this.lastSentSize = size;
    }
  }

  constructor() {
    super();

    this.resizeObserver = new ResizeObserver(() => {
      if (this.cardViewportWidth !== this.paneStrip.clientWidth) {
        this.cardViewportWidth = this.paneStrip.clientWidth;
        if (this.layoutMode === 'cards') this.requestUpdate();
      }
      this.sendResizeIfChanged();
    });
  }

  firstUpdated() {
    this.listeners = new AbortController();
    this.cellWidth = measureCellWidth();
    this.resizeObserver.observe(this.paneStrip);
    this.requestUpdate();
    
    this.setupKeyboard();
    this.setupMouse();
  }

  connectedCallback() {
    super.connectedCallback();
    if (this.paneStrip && !this.listeners) {
      this.listeners = new AbortController();
      this.resizeObserver.observe(this.paneStrip);
      this.setupKeyboard();
      this.setupMouse();
    }
  }

  disconnectedCallback() {
    super.disconnectedCallback();
    this.listeners?.abort();
    this.listeners = undefined;
    this.pointer = undefined;
    this.keyRouter.reset();
    for (const animation of this.movement.values()) animation.cancel();
    this.movement.clear();
    this.resizeObserver.disconnect();
    this.stopStatsReport();
    if (this.client) {
      this.client.disconnect();
      this.client = null;
    }
    this.connected = false;
  }

  private startStatsReport() {
    const stats = this.stats;
    if (!stats) return;
    this.stopStatsReport();
    stats.reset();
    let windowStart = performance.now();
    this.statsTimer = setInterval(() => {
      const now = performance.now();
      const line = formatSummary(stats.summary(now - windowStart));
      stats.reset();
      windowStart = now;
      console.info('[wideboi stats]', line);
      this.statsText = line.split(' | ').join('\n');
    }, STATS_REPORT_MS);
  }

  // Clears the text too (startStatsReport calls this), so a reconnect shows
  // "collecting…" rather than the previous connection's numbers.
  private stopStatsReport() {
    this.statsText = '';
    if (this.statsTimer === undefined) return;
    clearInterval(this.statsTimer);
    this.statsTimer = undefined;
  }

  private connectClient() {
    if (this.client) {
      this.client.disconnect();
    }
    this.stopStatsReport();
    this.connected = false;
    this.lastSentSize = undefined;
    this.keyRouter.reset();
    this.pendingFocusId = 0;
    this.panes = new PaneStore(this.stats);
    this.selectedPane = undefined;
    this.columns = [];
    this.displayWidths = {};
    this.pendingReveal.clear();
    this.activePanes = [];
    this.focusedPaneId = 0;
    this.previousFocusId = 0;
    this.stackFocusId = 0;
    this.focusTransition++;
    
    this.errorMsg = '';
    
    let url: URL;
    try {
      url = new URL(this.wsUrl);
      if (url.protocol !== 'ws:' && url.protocol !== 'wss:') {
        throw new Error('invalid WebSocket protocol');
      }
    } catch {
      this.errorMsg = 'Enter a valid WebSocket URL.';
      return;
    }
    // Accept an older token-bearing WebSocket URL entered in the form, but
    // remove its query credential before the browser opens the connection.
    const token = this.token || new URLSearchParams(url.hash.slice(1)).get('token') || url.searchParams.get('token') || '';
    url.searchParams.delete('token');
    url.hash = '';
    const client = new WideboiClient(url.toString(), token, this.stats);
    this.client = client;
    
    client.onConnect = () => {
      if (this.client !== client) return;
      console.log('Connected to server');
      this.connected = true;
      this.startStatsReport();
      this.errorMsg = '';
      void this.updateComplete.then(() => {
        if (this.client === client && this.connected) this.sendAttach();
      });
    };

    client.onDisconnect = () => {
      if (this.client !== client) return;
      this.connected = false;
      this.keyRouter.reset();
      this.stopStatsReport();
      this.errorMsg = 'Disconnected from server.';
    }
    client.onMessage = (message) => {
      if (this.client !== client) return;

      switch (message.msg.case) {
        case 'layoutSnapshot': {
          const snapshot = message.msg.value;
          const previous = this.panePositions();
          const previousFocus = this.focusedPaneId;
          this.focusedPaneId = reconcileFocus(this.columns, snapshot.columns, this.focusedPaneId);
          const widths = { ...this.displayWidths };
          for (const column of snapshot.columns) {
            if (widths[column.paneId] === undefined || this.followPTY) widths[column.paneId] = column.width;
          }
          for (const id of Object.keys(widths)) {
            if (!snapshot.columns.some(column => column.paneId === Number(id))) delete widths[Number(id)];
          }
          this.displayWidths = widths;
          this.columns = snapshot.columns;
          this.activePanes = snapshot.columns.map(c => c.paneId);
          if (this.focusedPaneId !== previousFocus) {
            this.stackFocusId = this.focusedPaneId;
            this.focusTransition++;
          } else if (this.stackFocusId !== null && !this.activePanes.includes(this.stackFocusId)) {
            this.stackFocusId = this.focusedPaneId;
          }
          if (this.pendingFocusId && this.activePanes.includes(this.pendingFocusId)) {
            this.focusPane(this.pendingFocusId);
            this.pendingFocusId = 0;
          }
          if (!this.activePanes.includes(this.previousFocusId)) this.previousFocusId = 0;
          this.paneStatuses = snapshot.paneStatuses;
          this.paneTitles = snapshot.paneTitles;
          const activeSet = new Set(this.activePanes);
          let metaPruned = false;
          const nextMeta = { ...this.paneMetadata };
          for (const id of Object.keys(nextMeta)) {
            if (!activeSet.has(Number(id))) {
              delete nextMeta[Number(id)];
              metaPruned = true;
            }
          }
          if (metaPruned) this.paneMetadata = nextMeta;
          void this.updateComplete.then(() => {
            this.animateReorder(previous);
            this.revealFocus();
            this.sendResizeIfChanged();
          });
          break;
        }
        case 'paneCreated':
          this.pendingFocusId = message.msg.value.paneId;
          break;
        case 'paneUpdate':
          this.panes.update(message.msg.value);
          this.requestUpdate();
          this.revealPendingCursor(message.msg.value.paneId);
          break;
        case 'panePatch':
          if (!this.panes.patch(message.msg.value)) {
            client.send({ case: 'paneResync', value: { paneId: message.msg.value.paneId } });
          }
          this.requestUpdate();
          this.revealPendingCursor(message.msg.value.paneId);
          break;
        case 'paneClosed': {
          const closedId = message.msg.value.paneId;
          const previous = this.panePositions();
          this.panes.close(closedId);
          const previousColumns = this.columns;
          const nextColumns = previousColumns.filter(column => column.paneId !== closedId);
          const nextFocus = reconcileFocus(previousColumns, nextColumns, this.focusedPaneId);
          const focusChanged = nextFocus !== this.focusedPaneId;
          this.columns = nextColumns;
          this.activePanes = nextColumns.map(column => column.paneId);
          this.focusedPaneId = nextFocus;
          let transition = this.focusTransition;
          if (this.stackFocusId === closedId || focusChanged) {
            this.stackFocusId = this.layoutMode === 'cards' && focusChanged ? null : nextFocus;
            transition = ++this.focusTransition;
          }
          if (this.previousFocusId === closedId) this.previousFocusId = 0;
          if (this.pendingFocusId === closedId) this.pendingFocusId = 0;
          if (this.searchState && this.searchState.paneId === closedId) {
            this.searchState = null;
            this.pendingNav = 0;
          }
          if (this.pointer?.pane.paneId === closedId) this.pointer = undefined;
          if (this.selectedPane?.paneId === closedId) this.selectedPane = undefined;
          if (this.paneMetadata[closedId]) {
            const next = { ...this.paneMetadata };
            delete next[closedId];
            this.paneMetadata = next;
          }
          void this.updateComplete.then(() => {
            this.animateReorder(previous);
            if (focusChanged) {
              this.focusedPane()?.focusInput();
              if (this.layoutMode === 'cards') this.finishFocusStack(nextFocus, transition);
            }
            const animations = Array.from(this.movement.values());
            void Promise.allSettled(animations.map(animation => animation.finished)).then(() => {
              if (this.focusedPaneId === nextFocus) this.revealFocus();
            });
            this.sendResizeIfChanged();
          });
          break;
        }
        case 'paneMetadata': {
          const meta = message.msg.value;
          this.paneMetadata = { ...this.paneMetadata, [meta.paneId]: meta };
          break;
        }
        case 'focusPane': {
          this.focusPane(message.msg.value.paneId);
          break;
        }
        case 'historySnapshot': {
          this.handleHistorySnapshot(message.msg.value);
          break;
        }
      }
    };

    client.connect();
  }

  private setupKeyboard() {
    document.addEventListener('keydown', (e) => {
      if (!this.connected || !this.client) return;
      const target = (e.composedPath()[0] || e.target) as HTMLElement;
      if (this.showHelp) {
        if (e.key === 'Escape' || e.key === '?' || (e.ctrlKey && e.key === 'c')) {
          this.closeHelp();
          e.preventDefault();
        }
        return;
      }
      if ((e.ctrlKey || e.metaKey) && (e.key === 'f' || e.code === 'KeyF')) {
        e.preventDefault();
        this.startSearch();
        return;
      }
      if (target instanceof HTMLInputElement || target instanceof HTMLSelectElement ||
          target instanceof HTMLButtonElement) return;
      if (e.isComposing || e.key === 'Process' || e.key === 'Dead') return;

      if (this.searchState) {
        if (e.key === 'Escape') {
          e.preventDefault();
          this.cancelSearch();
          return;
        }
        if (e.key === 'Enter') {
          e.preventDefault();
          this.acceptSearch();
          return;
        }
        if (e.ctrlKey && (e.key === 'g' || e.code === 'KeyG')) {
          e.preventDefault();
          this.liveSearch();
          return;
        }
        if (e.key === 'n' && !e.ctrlKey && !e.altKey && !e.metaKey) {
          e.preventDefault();
          this.navigateSearch(1);
          return;
        }
        if (e.key === 'N' && !e.ctrlKey && !e.altKey && !e.metaKey) {
          e.preventDefault();
          this.navigateSearch(-1);
          return;
        }
      }

      const action = this.keyRouter.handle(e);
      switch (action.type) {
        case 'ignore':
          e.preventDefault();
          return;
        case 'search':
          this.startSearch();
          e.preventDefault();
          return;
        case 'send_literal_key':
        case 'forward':
          if (sendKeyboardInput(this.client, this.focusedPaneId, e)) {
            this.pendingReveal.add(this.focusedPaneId);
            this.focusedPane()?.revealCursor();
            e.preventDefault();
          }
          return;
        case 'scroll':
          this.client.send({ case: 'scroll', value: { paneId: this.focusedPaneId, delta: action.delta } });
          e.preventDefault();
          return;
        case 'toggle_cards':
          this.setLayoutMode(this.layoutMode === 'cards' ? 'scroll' : 'cards');
          e.preventDefault();
          return;
        case 'pan':
          this.focusedPane()?.panCells(action.direction * 10);
          e.preventDefault();
          return;
        case 'toggle_follow_pty':
          this.followPTY = !this.followPTY;
          if (this.followPTY) this.displayWidths = Object.fromEntries(this.columns.map(column => [column.paneId, column.width]));
          e.preventDefault();
          return;
        case 'focus_column':
          this.focusColumnByIndex(action.column);
          e.preventDefault();
          return;
        case 'toggle_help':
          this.toggleHelp();
          e.preventDefault();
          return;
        case 'verb': {
          const verb = action.verb;
          const index = this.activePanes.indexOf(this.focusedPaneId);
          if (verb === VerbType.FOCUS_LEFT && index > 0) this.focusPane(this.activePanes[index - 1]);
          else if (verb === VerbType.FOCUS_RIGHT && index >= 0 && index < this.activePanes.length - 1) this.focusPane(this.activePanes[index + 1]);
          else if (verb === VerbType.FOCUS_LAST) this.focusPane(this.previousFocusId);
          else if (verb === VerbType.SMART_JUMP) {
            const rank = (status: PaneStatus | undefined) =>
              status === PaneStatus.FAILED ? 3 : status === PaneStatus.DONE ? 2 : status === PaneStatus.NEEDS_INPUT ? 1 : 0;
            const target = this.activePanes.reduce((best, id) => {
              const score = rank(this.paneStatuses[id]);
              return score > rank(this.paneStatuses[best]) ||
                (score > 0 && score === rank(this.paneStatuses[best]) && id < best) ? id : best;
            }, 0);
            if (target) this.focusPane(target);
          } else if ([VerbType.CYCLE_WIDTH, VerbType.GROW_WIDTH, VerbType.SHRINK_WIDTH].includes(verb)) {
            this.cycleWidth(verb);
          } else if (![VerbType.FOCUS_LEFT, VerbType.FOCUS_RIGHT, VerbType.SMART_JUMP, VerbType.FOCUS_LAST].includes(verb)) {
            this.client.send({ case: 'verb', value: { verb, paneId: this.focusedPaneId } });
          }
          e.preventDefault();
          return;
        }
      }
    }, { signal: this.listeners?.signal });

    document.addEventListener('paste', (e) => {
      const target = (e.composedPath()[0] || e.target) as HTMLElement;
      if (!this.connected || !this.client || target instanceof HTMLInputElement) return;
      const value = e.clipboardData?.getData('text/plain') || '';
      if (!sendTextInput(this.client, this.focusedPaneId, value)) return;
      this.pendingReveal.add(this.focusedPaneId);
      this.focusedPane()?.revealCursor();
      e.preventDefault();
    }, { signal: this.listeners?.signal });

    document.addEventListener('compositionend', (e) => {
      const target = (e.composedPath()[0] || e.target) as HTMLElement;
      if (!this.connected || !this.client || target instanceof HTMLInputElement) return;
      sendTextInput(this.client, this.focusedPaneId, (e as CompositionEvent).data);
    }, { signal: this.listeners?.signal });
  }

  private setupMouse() {
    this.paneStrip.addEventListener('scroll', () => {
      if (this.layoutMode === 'cards' && this.paneStrip.scrollLeft) this.paneStrip.scrollLeft = 0;
    }, { signal: this.listeners?.signal });

    this.paneStrip.addEventListener('pointerdown', (e) => {
      if (!this.connected || !this.client) return;
      const pane = this.eventPane(e);
      if (!pane) return;
      const start = pane.cellAt(e.clientX, e.clientY);
      this.pointer = {
        id: e.pointerId, pane,
        button: e.button === 0 ? 1 : e.button === 2 ? 3 : 2,
        tracking: pane.paneId === this.focusedPaneId && this.panes.mouseTracking(pane.paneId),
        focusOnClick: pane.paneId !== this.focusedPaneId, dragged: false, start,
      };
      pane.setPointerCapture(e.pointerId);
      this.selectedPane?.clearSelection();
      this.selectedPane = undefined;
      if (this.pointer.tracking) this.sendPointerMouse(MouseKind.PRESS, e);
      e.preventDefault();
    }, { signal: this.listeners?.signal });

    this.paneStrip.addEventListener('pointermove', (e) => {
      const press = this.pointer;
      if (!press || press.id !== e.pointerId) return;
      const end = press.pane.cellAt(e.clientX, e.clientY);
      if (end.x !== press.start.x || end.y !== press.start.y) press.dragged = true;
      if (press.tracking) this.sendPointerMouse(MouseKind.MOTION, e);
      else if (press.button === 1) {
        press.pane.setSelection(press.start, end);
        this.selectedPane = press.pane;
      }
      e.preventDefault();
    }, { signal: this.listeners?.signal });

    const release = (e: PointerEvent) => {
      if (!this.pointer || this.pointer.id !== e.pointerId) return;
      if (this.pointer.tracking) this.sendPointerMouse(MouseKind.RELEASE, e);
      else {
        if (e.type === 'pointerup' && this.pointer.focusOnClick && !this.pointer.dragged) {
          this.pointer.pane.clearSelection();
          this.focusPane(this.pointer.pane.paneId);
        } else {
          const press = this.pointer;
          const text = selectionText(this.panes.get(press.pane.paneId), press.start,
            press.pane.cellAt(e.clientX, e.clientY));
          if (text && navigator.clipboard?.writeText) void navigator.clipboard.writeText(text).catch(() => {});
        }
      }
      const pane = this.pointer.pane;
      this.pointer = undefined;
      if (pane.hasPointerCapture(e.pointerId)) pane.releasePointerCapture(e.pointerId);
      e.preventDefault();
    };
    this.paneStrip.addEventListener('pointerup', release, { signal: this.listeners?.signal });
    this.paneStrip.addEventListener('pointercancel', release, { signal: this.listeners?.signal });

    this.paneStrip.addEventListener('wheel', (e) => {
      if (!this.connected || !this.client) return;
      // Leave horizontal gestures to the pane viewport or outer strip.
      if (e.shiftKey || Math.abs(e.deltaX) > Math.abs(e.deltaY)) return;
      const pane = this.eventPane(e);
      if (pane) {
        // A short client scrolls within the live terminal grid. Alt+wheel
        // explicitly navigates terminal history instead.
        if (pane.hasVerticalOverflow && !e.altKey) return;
        e.preventDefault();
        // e.deltaY > 0 means scrolling down (towards bottom/newer).
        // e.deltaY < 0 means scrolling up (towards top/older).
        // In MsgScroll, Delta > 0 is up (older), Delta < 0 is down (newer).
        // A standard wheel step is often 3 lines.
        const delta = e.deltaY > 0 ? -3 : 3;
        this.client.send({ case: 'scroll', value: { paneId: pane.paneId, delta } });
      }
    }, { passive: false, signal: this.listeners?.signal });
  }

  private eventPane(e: Event): WideboiPane | undefined {
    return e.composedPath().find(node => node instanceof WideboiPane) as WideboiPane | undefined;
  }

  private sendPointerMouse(kind: MouseKind, e: PointerEvent) {
    const press = this.pointer;
    if (!press || !this.client || !this.connected) return;
    const { x, y } = press.pane.cellAt(e.clientX, e.clientY);
    this.client.send({ case: 'mouse', value: {
      paneId: press.pane.paneId, kind, x, y,
      button: press.button,
      mod: (e.shiftKey ? 1 : 0) | (e.altKey ? 2 : 0) | (e.ctrlKey ? 4 : 0)
    } });
  }

  private sendAttach() {
     if (!this.client) return;
     const size = this.getGridSize();
     this.client.send({ case: 'attach', value: { cols: size.cols, rows: size.rows } });
     this.lastSentSize = size;
  }

  private handleUrlChange(e: Event) {
    this.wsUrl = (e.target as HTMLInputElement).value;
  }

  private handleTokenChange(e: Event) {
    this.token = (e.target as HTMLInputElement).value;
  }

  private handleKeydown(e: KeyboardEvent) {
    if (e.key === 'Enter') {
      this.connectClient();
    }
  }



  private handlePaneSelect(e: Event) {
    const select = e.target as HTMLSelectElement;
    const paneID = parseInt(select.value, 10);
    if (paneID > 0 && this.client && this.connected) {
      this.focusPane(paneID);
    }
  }

  private openHelp() {
    this.showHelp = true;
    void this.updateComplete.then(() => {
      this.renderRoot.querySelector<HTMLButtonElement>('.help-dialog .close-btn')?.focus();
    });
  }

  private closeHelp() {
    this.showHelp = false;
    void this.updateComplete.then(() => {
      this.focusedPane()?.focusInput();
    });
  }

  private toggleHelp() {
    if (this.showHelp) {
      this.closeHelp();
    } else {
      this.openHelp();
    }
  }

  private handlePrefixChange(e: Event) {
    const val = (e.target as HTMLSelectElement).value;
    this.prefixSetting = val;
    try {
      localStorage.setItem('wideboi.prefix', val);
    } catch {
      // ignore localStorage quota/disabled errors
    }
    this.keyRouter.setPrefix(val);
  }

  private focusColumnByIndex(colIndex: number) {
    if (this.columns.length === 0) return;
    let targetPaneId: number | undefined;
    if (colIndex === 0) {
      targetPaneId = this.columns[this.columns.length - 1]?.paneId;
    } else if (colIndex >= 1 && colIndex <= this.columns.length) {
      targetPaneId = this.columns[colIndex - 1]?.paneId;
    }
    if (targetPaneId !== undefined) {
      this.focusPane(targetPaneId);
    }
  }

  claimSize() {
    if (!this.client || !this.connected) return;
    this.sendResizeIfChanged();
    this.client.send({
      case: 'verb',
      value: { verb: VerbType.CLAIM_SIZE, paneId: this.focusedPaneId, widths: this.displayWidths },
    });
  }

  private setLayoutMode(mode: 'cards' | 'scroll') {
    if (mode !== 'cards' && mode !== 'scroll') return;
    if (mode === this.layoutMode) return;
    const previous = this.panePositions();
    if (mode === 'cards') {
      this.stackFocusId = this.focusedPaneId;
      this.stripScrollLeft = this.paneStrip.scrollLeft;
      // Cancel a smooth focus reveal still in progress in the strip.
      this.paneStrip.scrollTo({ left: 0, behavior: 'instant' });
    }
    this.focusTransition++;
    this.layoutMode = mode;
    void this.updateComplete.then(() => {
      this.paneStrip.scrollTo({ left: mode === 'cards' ? 0 : this.stripScrollLeft, behavior: 'instant' });
      this.animateReorder(previous);
      const animations = Array.from(this.movement.values());
      void Promise.allSettled(animations.map(animation => animation.finished)).then(() => {
        if (this.layoutMode === mode) this.revealFocus();
      });
    });
  }

  private handleWidthInput(e: Event) { this.editWidth(Number((e.target as HTMLInputElement).value)); }

  private handleLayoutSelect(e: Event) {
    const mode = (e.target as HTMLSelectElement).value;
    if (mode === 'cards' || mode === 'scroll') {
      this.setLayoutMode(mode);
    }
  }

  render() {
    const cards = this.layoutMode === 'cards';
    const displayWidth = (column: ColumnData) => this.displayWidths[column.paneId] ?? column.width;
    const displayColumns = this.columns.map(column => ({ paneId: column.paneId, width: displayWidth(column) }));
    const stackFocusId = this.stackFocusId === null ? null :
      (this.activePanes.includes(this.stackFocusId) ? this.stackFocusId : this.focusedPaneId);
    const layout = cards ? cardLayout(displayColumns, this.focusedPaneId,
      Math.floor(this.cardViewportWidth / this.cellWidth), this.cardFirst, stackFocusId) : undefined;
    if (layout) this.cardFirst = layout.first;
    const placements = new Map(layout?.placements.map(p => [p.paneId, p]));
    return html`
      ${this.connected ? html`
        <div class="toolbar">
          <label for="focus-pane">Focus Pane:</label>
          <select id="focus-pane" @change=${this.handlePaneSelect}>
            ${repeat(this.activePanes, id => id, id => html`
              <option value=${id} .selected=${id === this.focusedPaneId}>[${id}] ${this.paneTitles[id] || 'Terminal'}</option>
            `)}
          </select>
          <label for="layout-mode">Layout:</label>
          <select id="layout-mode" aria-label="Layout" @change=${this.handleLayoutSelect}>
            <option value="scroll" .selected=${!cards}>Scroll</option>
            <option value="cards" .selected=${cards}>Cards</option>
          </select>
          <label for="pane-width">Pane width:</label>
          <input id="pane-width" aria-label="Pane width" type="number" min="20" max="4096"
            .value=${String(this.displayWidths[this.focusedPaneId] ?? '')} @change=${this.handleWidthInput}>
          <label><input type="checkbox" aria-label="Follow PTY widths" .checked=${this.followPTY}
            @change=${() => { this.followPTY = !this.followPTY; if (this.followPTY) this.displayWidths = Object.fromEntries(this.columns.map(column => [column.paneId, column.width])); }}>Follow PTY</label>
          <label for="prefix-key">Prefix:</label>
          <select id="prefix-key" aria-label="Prefix key" @change=${this.handlePrefixChange}>
            <option value="ctrl+b" .selected=${this.prefixSetting === 'ctrl+b'}>Ctrl+B</option>
            <option value="ctrl+a" .selected=${this.prefixSetting === 'ctrl+a'}>Ctrl+A</option>
            <option value="ctrl+space" .selected=${this.prefixSetting === 'ctrl+space'}>Ctrl+Space</option>
          </select>
          <button class="claim-size-btn" @click=${this.claimSize} title="Fit session terminal size to this window">Fit to Window</button>
          <button class="claim-size-btn search-btn" @click=${this.startSearch} title="Search pane history (/ or Ctrl+F)">Search</button>
          <button class="help-btn" @click=${this.toggleHelp} aria-label="Help">Help (?)</button>
          <span class="tip">(Tip: ${this.keyRouter.prefixLabel} then arrows or h/l to switch, ? for help)</span>
        </div>
      ` : ''}
      <div class="terminal-shell">
        <div class="title">${this.paneTitles[this.focusedPaneId] ||
          (this.focusedPaneId ? `Pane ${this.focusedPaneId}` : '')}</div>
        <div class=${cards ? 'pane-strip cards' : 'pane-strip'}>
          ${repeat(this.columns, column => column.paneId, column => {
            const placement = placements.get(column.paneId);
            return html`
            <wideboi-pane
              style=${`width: ${displayWidth(column) * this.cellWidth}px; --divider-width: ${cards ? 0 : this.cellWidth}px; ${cards ? `left: ${(placement?.left ?? 0) * this.cellWidth}px; z-index: ${placement?.z ?? 0}; visibility: ${placement?.visible ? 'visible' : 'hidden'}` : ''}`}
              .paneId=${column.paneId}
              .pane=${this.panes.get(column.paneId)}
              .focused=${column.paneId === this.focusedPaneId}
              .cardMode=${cards}
              .cardLabel=${`[${column.paneId}] ${this.paneTitles[column.paneId] || 'Terminal'}`}
              .running=${this.connected}
              .cellWidth=${this.cellWidth}
              .displayCols=${displayWidth(column)}
              .stats=${this.stats}
              aria-label=${`Pane ${column.paneId}`}
            ></wideboi-pane>
          `; })}
          ${cards && layout?.hiddenLeft ? html`<span class="card-count left">+${layout.hiddenLeft}</span>` : ''}
          ${cards && layout?.hiddenRight ? html`<span class="card-count right">+${layout.hiddenRight}</span>` : ''}
        </div>
        ${this.searchState ? html`
          <div class="status search-bar">
            <span>search /</span>
            <input
              class="search-input"
              type="text"
              .value=${this.searchState.query}
              @input=${(e: InputEvent) => {
                if (this.searchState) {
                  this.searchState = {
                    ...this.searchState,
                    query: (e.target as HTMLInputElement).value,
                    status: 'input',
                  };
                }
              }}
              @keydown=${(e: KeyboardEvent) => {
                if (e.key === 'Enter') {
                  e.preventDefault();
                  if (this.searchState?.status === 'input') {
                    this.commitSearch();
                  } else if (this.searchState?.matches.length) {
                    this.navigateSearch(e.shiftKey ? -1 : 1);
                  }
                } else if (e.key === 'Escape') {
                  e.preventDefault();
                  this.cancelSearch();
                } else if (e.ctrlKey && (e.key === 'g' || e.code === 'KeyG')) {
                  e.preventDefault();
                  this.liveSearch();
                }
              }}
              placeholder="find in history..."
            />
            <button
              class="search-btn prev-btn"
              ?disabled=${!this.searchState.matches.length}
              @click=${() => this.navigateSearch(-1)}
              title="Previous match (Shift+Enter or N)"
            >▲ Prev</button>
            <button
              class="search-btn next-btn"
              ?disabled=${!this.searchState.matches.length}
              @click=${() => this.navigateSearch(1)}
              title="Next match (Enter or n)"
            >▼ Next</button>
            <button class="search-btn keep-btn" @click=${() => this.acceptSearch()} title="Keep current scroll (Enter)">Keep</button>
            <button class="search-btn restore-btn" @click=${() => this.cancelSearch()} title="Restore previous view (Esc)">Restore</button>
            <button class="search-btn live-btn" @click=${() => this.liveSearch()} title="Jump to bottom (Ctrl+G)">Live</button>
            <span class="search-msg">
              ${formatSearchStatus(this.searchState)}
            </span>
          </div>
        ` : html`
          <div class="status">${(() => {
            const fp = this.panes.get(this.focusedPaneId);
            const focusScroll = (fp && fp.scrollOffset > 0) ? `focus: [${this.focusedPaneId} ★] [scroll +${fp.scrollOffset}${fp.unreadOutput ? ' ⤓' : ''}]  ` : '';
            const items = this.columns.map(column =>
              `[${column.paneId}] ${PaneStatus[this.paneStatuses[column.paneId] ?? PaneStatus.IDLE] || ''}`
            ).join('  ');
            return `${focusScroll}${items}`;
          })()}</div>
        `}
      </div>
      ${this.stats ? html`<pre class="stats-overlay">${this.statsText || 'stats: collecting…'}</pre>` : ''}
      ${this.showHelp ? html`
        <div class="help-overlay" @click=${this.closeHelp}>
          <div class="help-dialog" role="dialog" aria-modal="true" aria-labelledby="help-title" @click=${(e: Event) => e.stopPropagation()}>
            <h3 id="help-title">
              <span>wideboi Shortcuts</span>
              <button class="close-btn" @click=${this.closeHelp} aria-label="Close help">×</button>
            </h3>
            <p style="margin-top: 0; color: #aaa;">Prefix: <kbd>${this.keyRouter.prefixLabel}</kbd> (press twice to send literal key)</p>
            <table class="help-table">
              <thead><tr><th>Key after prefix</th><th>Action</th></tr></thead>
              <tbody>
                <tr><td><kbd>h</kbd> / <kbd>←</kbd></td><td>Focus left</td></tr>
                <tr><td><kbd>l</kbd> / <kbd>→</kbd></td><td>Focus right</td></tr>
                <tr><td><kbd>Tab</kbd></td><td>Focus previous pane</td></tr>
                <tr><td><kbd>1</kbd>–<kbd>9</kbd>, <kbd>0</kbd></td><td>Focus column by position (0 is last)</td></tr>
                <tr><td><kbd>a</kbd></td><td>Smart jump (needs input / failed / done)</td></tr>
                <tr><td><kbd>c</kbd></td><td>Toggle cards / scroll layout</td></tr>
                <tr><td><kbd>n</kbd></td><td>New column</td></tr>
                <tr><td><kbd>w</kbd></td><td>Cycle column width</td></tr>
                <tr><td><kbd>o</kbd> / <kbd>p</kbd></td><td>Shrink / grow column width</td></tr>
                <tr><td><kbd>H</kbd> / <kbd>L</kbd></td><td>Pan focused pane left / right</td></tr>
                <tr><td><kbd>f</kbd></td><td>Toggle following PTY widths</td></tr>
                <tr><td><kbd>y</kbd> / <kbd>u</kbd></td><td>Move column left / right</td></tr>
                <tr><td><kbd>j</kbd> / <kbd>k</kbd></td><td>Scroll history down / up</td></tr>
                <tr><td><kbd>/</kbd></td><td>Search focused pane history (Ctrl+F)</td></tr>
                <tr><td><kbd>x</kbd></td><td>Kill focused pane</td></tr>
                <tr><td><kbd>?</kbd></td><td>Toggle this help</td></tr>
                <tr><td><kbd>Esc</kbd> / <kbd>Ctrl+C</kbd></td><td>Cancel prefix mode</td></tr>
              </tbody>
            </table>
          </div>
        </div>
      ` : ''}
      ${!this.connected ? html`
        <div class="overlay">
          <div class="connection-box">
            <h2>Connect to wideboi</h2>
            <input 
              type="text" 
              .value=${this.wsUrl} 
              @input=${this.handleUrlChange}
              @keydown=${this.handleKeydown}
              placeholder=${`${window.location.protocol === 'https:' ? 'wss:' : 'ws:'}//${window.location.host}/ws`}
            />
            <input 
              type="password" 
              .value=${this.token} 
              @input=${this.handleTokenChange}
              @keydown=${this.handleKeydown}
              placeholder="Token (optional)"
            />
            <button @click=${this.connectClient}>Connect</button>
            ${this.errorMsg ? html`<div class="error">${this.errorMsg}</div>` : ''}
          </div>
        </div>
      ` : ''}
    `;
  }
}
