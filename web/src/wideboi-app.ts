import { LitElement, html, type PropertyValues } from 'lit';
import { customElement, query, state } from 'lit/decorators.js';
import { repeat } from 'lit/directives/repeat.js';
import { appHostStyles, wideboiAppStyles } from './wideboi-app.styles';
import { WideboiClient } from './client';
import { termSettings, measureCellWidth, PaneStore, selectionText, findUrlAt, type CellPoint } from './pane-state';
import { WideboiPane } from './wideboi-pane';
import { cardLayout } from './card-layout';
import { sendKeyboardInput, sendPasteInput, sendTextInput, sendWheelInput } from './input';
import { KeyRouter, type KeyRouterAction } from './key-router';
import { consumeLinkToken } from './token';
import { RenderStats, formatSummary, statsEnabled } from './stats';
import { MouseKind, MsgHistorySnapshot, MsgPaneMetadata, PaneStatus, VerbType, type ColumnData } from './gen/internal/protocol/wirepb/wideboi_pb';
import { SearchController } from './search-controller';
import { formatSearchStatus, type SearchState } from './search';
import { executeMacro, macroEndsWithEnter, DEFAULT_MACROS, wireToLocalMacro, localToWireMacro, type Macro, type MacroStep } from './macros';
import { initialSessionState, reduceSession, type SessionAction, type SessionState } from './session-state';
import { getPref, setPref } from './prefs';
import { getTheme, listThemes, getThemeCSSVariables, type Theme } from './themes';
import './components/settings-dialog';
import type { WideboiSettings } from './components/settings-dialog';
import './components/mobile-bar';
import './components/command-menu';
import type { WideboiCommandMenu } from './components/command-menu';
import './components/command-palette';
import type { WideboiCommandPalette } from './components/command-palette';
import './components/context-menu';
import type { ContextMenuAction } from './components/context-menu';
import { MobileDirectInputController } from './mobile-direct-input';

const desktopSession = typeof window !== 'undefined' ? new URLSearchParams(window.location.search).get('session') : null;
const STATS_REPORT_MS = 5000;
const NARROW_VIEW = '(max-width: 480px)';
function isApplePlatform(): boolean {
  if (typeof navigator === 'undefined') return false;
  return /Mac|iPhone|iPad|iPod/.test(navigator.platform || '') || /Macintosh|iPhone|iPad/.test(navigator.userAgent || '');
}

@customElement('wideboi-app')
export class WideboiApp extends LitElement {
  static styles = [appHostStyles, wideboiAppStyles];

  @query('.pane-strip')
  private paneStrip!: HTMLElement;

  private client: WideboiClient | null = null;
  // Present only with ?stats=1; everything downstream treats undefined as off.
  private readonly stats = (typeof window !== 'undefined' && statsEnabled(window.location.search)) ? new RenderStats() : undefined;
  private panes = new PaneStore(this.stats);
  private resizeObserver: ResizeObserver;
  private readonly narrowMedia = (typeof window !== 'undefined' && window.matchMedia) ? window.matchMedia(NARROW_VIEW) : { matches: false } as MediaQueryList;
  private cellWidth = 1;
  private statsTimer?: ReturnType<typeof setInterval>;

  @state()
  private statsText = '';

  @state()
  private connected = false;

  @state()
  private session: SessionState = initialSessionState(getPref('macros'));

  private dispatchSession(action: SessionAction) {
    this.session = reduceSession(this.session, action);
    this.requestUpdate();
  }

  get activePanes(): number[] { return this.session.activePanes as number[]; }
  get columns(): ColumnData[] { return this.session.columns as ColumnData[]; }
  get paneTitles(): Record<number, string> { return this.session.paneTitles as Record<number, string>; }
  get paneMetadata(): Record<number, MsgPaneMetadata> { return this.session.paneMetadata as Record<number, MsgPaneMetadata>; }
  get focusedPaneId(): number { return this.session.focusedPaneId; }
  get displayWidths(): Record<number, number> { return this.session.displayWidths as Record<number, number>; }
  set displayWidths(widths: Record<number, number>) {
    this.session = { ...this.session, displayWidths: widths };
    this.requestUpdate();
  }
  get macros(): Macro[] { return this.session.macros as Macro[]; }
  set macros(m: Macro[]) {
    this.dispatchSession({ type: 'macrosSnapshot', macros: m });
  }
  get stackFocusId(): number | null { return this.session.stackFocusId; }
  get previousFocusId(): number { return this.session.previousFocusId; }
  get pendingFocusId(): number { return this.session.pendingFocusId; }
  get focusTransition(): number { return this.session.focusTransition; }
  get paneStatuses(): Record<number, PaneStatus> { return this.session.paneStatuses as Record<number, PaneStatus>; }
  get paneScrolls(): Record<number, string> {
    const scrolls: Record<number, string> = {};
    for (const id of this.activePanes) {
      const pane = this.panes.get(id);
      if (pane && pane.scrollOffset > 0) {
        scrolls[id] = `+${pane.scrollOffset}${pane.unreadOutput ? ' ⤓' : ''}`;
      }
    }
    return scrolls;
  }

  @state()
  private wsUrl = (() => {
    if (typeof window === 'undefined') return '';
    const url = new URL(`${window.location.protocol === 'https:' ? 'wss:' : 'ws:'}//${window.location.host}/ws`);
    if (desktopSession) url.searchParams.set('session', desktopSession);
    return url.toString();
  })();

  @state()
  private token = '';

  @state()
  private errorMsg = '';

  @state()
  private showHelp = false;

  @state()
  private showSettings = false;

  @state()
  private showCommandMenu = false;

  @state()
  private showCommandPalette = false;
  private commandPaletteInitialQuery = '';

  @state()
  private themeId = getPref('theme');

  get currentTheme(): Theme {
    return getTheme(this.themeId);
  }

  private applyTheme(id: string) {
    const theme = getTheme(id);
    this.themeId = theme.id;
    const vars = getThemeCSSVariables(theme);
    for (const [key, val] of Object.entries(vars)) {
      this.style.setProperty(key, val);
    }
  }

  public setTheme(id: string) {
    const validId = getTheme(id).id;
    this.applyTheme(validId);
    setPref('theme', validId);
    this.requestUpdate();
  }

  private handleThemeSelect = (e: Event) => {
    const val = (e.target as HTMLSelectElement).value;
    this.setTheme(val);
  };

  @state()
  private prefixSetting = getPref('prefix');

  private keyRouter = new KeyRouter(this.prefixSetting);
  private listeners?: AbortController;
  private pointer?: { id: number; pane: WideboiPane; button: number; tracking: boolean; focusOnClick: boolean; dragged: boolean; start: CellPoint };
  private selectedPane?: WideboiPane;
  private hoveredPane?: WideboiPane;
  private movement = new Map<number, Animation>();
  private lastSentSize?: { cols: number; rows: number };
  @state() private layoutMode: 'scroll' | 'cards' = 'cards';
  @state() private followPTY = false;
  @state() private widthPresets: number[] = [40, 60, 80];
  @state() private minColumnWidth: number = 20;
  @state() private maxColumnWidth: number = 4096;
  private pendingReveal = new Set<number>();
  @state() private mobile = this.narrowMedia.matches;
  @state() private mobileDraft = '';
  @state() private mobileCtrl = false;
  @state() private mobileInputMode: 'draft' | 'direct' = 'draft';
  @state() private showMacros = false;
  @state() private showMacroEditor = false;
  @state() private contextMenu = { open: false, x: 0, y: 0, paneId: 0, hasSelection: false };
  @state() private draftMacroSteps: MacroStep[] = [];
  @state() private paneZooms = new Map<number, number>();
  private searchController = new SearchController({
    sendScroll: (cmd) => {
      if (!this.client) return;
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
    },
    requestHistory: (paneId) => {
      this.client?.send({ case: 'historyRequest', value: { paneId } });
    },
    focusTerminal: () => {
      this.focusedPane()?.focusInput();
    },
    focusSearchInput: () => {
      this.requestUpdate();
      void this.updateComplete.then(() => {
        const input = this.shadowRoot?.querySelector<HTMLInputElement>('.search-input');
        if (input) {
          input.focus();
          input.select();
        }
      });
    },
  });

  private mobileDirectController = new MobileDirectInputController({
    onKey: (e) => {
      if (!this.client || !this.connected || !this.focusedPaneId) return false;
      return sendKeyboardInput(this.client, this.focusedPaneId, e);
    },
    onText: (text) => {
      if (!this.client || !this.connected || !this.focusedPaneId) return false;
      return sendTextInput(this.client, this.focusedPaneId, text);
    },
    getCtrl: () => this.mobileCtrl,
    resetCtrl: () => {
      this.mobileCtrl = false;
    },
    onRevealCursor: () => {
      if (this.focusedPaneId) {
        this.pendingReveal.add(this.focusedPaneId);
        this.focusedPane()?.revealCursor();
      }
    },
  });

  get searchState(): SearchState | null {
    return this.searchController.state;
  }
  set searchState(s: SearchState | null) {
    this.searchController.state = s;
    this.requestUpdate();
  }

  private cardFirst = 0;
  private cardViewportWidth = 0;
  private stripScrollLeft = 0;

  private finishFocusStack(paneID: number, transition: number) {
    const animations = Array.from(this.movement.values()).filter(animation =>
      animation.playState === 'running' || animation.playState === 'paused');
    void Promise.allSettled(animations.map(animation => animation.finished)).then(() => {
      if (this.mobile || this.layoutMode !== 'cards' || this.focusedPaneId !== paneID || this.focusTransition !== transition) return;
      if (Array.from(this.movement.values()).some(animation =>
        animation.playState === 'running' || animation.playState === 'paused')) {
        this.finishFocusStack(paneID, transition);
        return;
      }
      this.dispatchSession({ type: 'finishFocusStack', paneId: paneID, transition });
    });
  }

  public startSearch = () => {
    if (!this.focusedPaneId) return;
    const fp = this.panes.get(this.focusedPaneId);
    const priorOffset = fp?.scrollOffset ?? 0;
    const priorHistoryLen = fp?.scrollbackLen ?? 0;
    this.searchController.start(this.focusedPaneId, priorOffset, priorHistoryLen, this.searchController.state?.query || '');
    this.requestUpdate();
  };

  public commitSearch() {
    this.searchController.commit();
    this.requestUpdate();
  }

  public navigateSearch(direction: 1 | -1) {
    this.searchController.navigate(direction);
    this.requestUpdate();
  }

  public acceptSearch() {
    this.searchController.accept();
    this.requestUpdate();
  }

  public cancelSearch() {
    this.searchController.cancel();
    this.requestUpdate();
  }

  public liveSearch() {
    this.searchController.live();
    this.requestUpdate();
  }

  private handleHistorySnapshot(snapshot: MsgHistorySnapshot) {
    if (this.searchController.handleHistorySnapshot(snapshot)) {
      this.requestUpdate();
    }
  }

  private focusPane(paneID: number) {
    if (!this.activePanes.includes(paneID)) return;
    if (this.searchState && this.searchState.paneId !== paneID) {
      this.cancelSearch();
    }
    if (paneID === this.focusedPaneId) {
      void this.updateComplete.then(() => {
        if (!this.mobile) this.focusedPane()?.focusInput();
        this.revealFocus();
      });
      return;
    }
    const previous = this.panePositions();
    this.dispatchSession({ type: 'focusPane', paneId: paneID, layoutMode: this.layoutMode, mobile: this.mobile });
    const transition = this.focusTransition;
    void this.updateComplete.then(() => {
      if (this.layoutMode === 'cards' && !this.mobile) {
        this.animateReorder(previous);
        this.finishFocusStack(paneID, transition);
      }
      if (!this.mobile) this.focusedPane()?.focusInput();
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
    const presets = this.widthPresets && this.widthPresets.length > 0 ? this.widthPresets : [40, 60, 80];
    if (verb === VerbType.CYCLE_WIDTH) next = presets.find(width => width > current) ?? presets[0];
    if (verb === VerbType.GROW_WIDTH) next = Math.min(this.maxColumnWidth, current + 10);
    if (verb === VerbType.SHRINK_WIDTH) next = Math.max(this.minColumnWidth, current - 10);
    this.editWidth(next);
  }

  private revealFocus() {
    if (this.mobile || this.layoutMode === 'cards') return;
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
    if (this.mobile) return;
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
      rows: Math.floor(this.paneStrip.clientHeight / termSettings.cellHeight) + 2,
    };
  }

  private sendResizeIfChanged() {
    if (this.mobile || !this.client || !this.connected || !this.lastSentSize) return;
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

  async firstUpdated() {
    this.listeners = new AbortController();
    this.setupViewport();
    try { await document.fonts.load(termSettings.font); } catch (e) {}
    this.cellWidth = measureCellWidth();
    this.resizeObserver.observe(this.paneStrip);
    this.requestUpdate();
    
    this.setupKeyboard();
    this.setupMouse();
    if (desktopSession) this.connectClient();
  }

  connectedCallback() {
    super.connectedCallback();
    if (typeof window !== 'undefined' && !this.token) {
      this.token = consumeLinkToken(window.location, window.history);
    }
    this.applyTheme(this.themeId);
    if (this.paneStrip && !this.listeners) {
      this.listeners = new AbortController();
      this.setupViewport();
      this.resizeObserver.observe(this.paneStrip);
      this.setupKeyboard();
      this.setupMouse();
    }
  }

  private setupViewport() {
    const syncWidth = () => {
      this.mobile = this.narrowMedia.matches;
      this.syncVisibleHeight();
      if (!this.mobile) this.sendResizeIfChanged();
    };
    this.narrowMedia.addEventListener('change', syncWidth, { signal: this.listeners?.signal });
    window.visualViewport?.addEventListener('resize', () => this.syncVisibleHeight(),
      { signal: this.listeners?.signal });
    this.syncVisibleHeight();
  }

  private syncVisibleHeight() {
    if (this.mobile && window.visualViewport) {
      this.style.setProperty('--app-height', `${window.visualViewport.height}px`);
    } else {
      this.style.removeProperty('--app-height');
    }
  }

  disconnectedCallback() {
    super.disconnectedCallback();
    this.mobileDirectController.detach();
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

  private notifyBackground(title: string, body: string) {
    if (!getPref('notifications')) return;
    if (typeof Notification !== 'undefined' && Notification.permission === 'granted' && document.hidden) {
      try {
        new Notification(title, { body });
      } catch (e) {}
    }
  }

  private connectClient() {
    if (this.client) {
      this.client.disconnect();
    }
    this.stopStatsReport();
    this.connected = false;
    this.lastSentSize = undefined;
    this.keyRouter.reset();
    this.mobileDraft = '';
    this.mobileCtrl = false;
    this.mobileInputMode = 'draft';
    this.showMacros = false;
    this.showMacroEditor = false;
    this.showCommandMenu = false;
    this.paneZooms.clear();
    this.panes = new PaneStore(this.stats);
    this.selectedPane = undefined;
    this.pendingReveal.clear();
    this.dispatchSession({ type: 'reset' });
    
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
      this.mobileCtrl = false;
      this.stopStatsReport();
      this.errorMsg = 'Disconnected from server.';
    }
    client.onMessage = (message) => {
      if (this.client !== client) return;

      switch (message.msg.case) {
        case 'layoutSnapshot': {
          const snapshot = message.msg.value;
          if (snapshot.paneStatuses) {
            for (const [idStr, newStatus] of Object.entries(snapshot.paneStatuses)) {
              const paneId = Number(idStr);
              const oldStatus = this.paneStatuses[paneId];
              if (oldStatus === PaneStatus.WORKING && newStatus !== oldStatus && paneId !== this.focusedPaneId) {
                const title = snapshot.paneTitles?.[paneId] || `Pane ${paneId}`;
                let desc = '';
                if (newStatus === PaneStatus.NEEDS_INPUT) desc = 'Needs input';
                else if (newStatus === PaneStatus.DONE) desc = 'Finished successfully';
                else if (newStatus === PaneStatus.FAILED) desc = 'Failed';
                if (desc) this.notifyBackground(title, desc);
              }
            }
          }
          const previous = this.panePositions();
          const previousFocus = this.focusedPaneId;
          this.dispatchSession({
            type: 'layoutSnapshot',
            snapshot,
            followPTY: this.followPTY,
            layoutMode: this.layoutMode,
            mobile: this.mobile,
          });
          const nextFocus = this.focusedPaneId;
          const focusChanged = nextFocus !== previousFocus;
          const transition = this.focusTransition;
          if (focusChanged && this.searchController.active && this.searchController.paneId !== nextFocus) {
            this.cancelSearch();
          }
          void this.updateComplete.then(() => {
            this.animateReorder(previous);
            if (focusChanged) {
              if (!this.mobile) this.focusedPane()?.focusInput();
              if (this.layoutMode === 'cards' && !this.mobile) this.finishFocusStack(nextFocus, transition);
            }
            this.revealFocus();
            this.sendResizeIfChanged();
          });
          break;
        }
        case 'paneCreated':
          this.dispatchSession({ type: 'paneCreated', paneId: message.msg.value.paneId });
          break;
        case 'splitResponse':
          if (message.msg.value.paneId) {
            this.dispatchSession({ type: 'splitResponse', paneId: message.msg.value.paneId });
          }
          break;
        case 'renamePaneResponse':
          if (message.msg.value.error) {
            console.warn(`[wideboi] rename-pane error: ${message.msg.value.error}`);
          }
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
          this.paneZooms.delete(closedId);
          const previous = this.panePositions();
          this.panes.close(closedId);
          const previousFocus = this.focusedPaneId;
          this.dispatchSession({
            type: 'paneClosed',
            paneId: closedId,
            layoutMode: this.layoutMode,
            mobile: this.mobile,
          });
          const nextFocus = this.focusedPaneId;
          const focusChanged = nextFocus !== previousFocus;
          const transition = this.focusTransition;
          this.searchController.closeIfMatching(closedId);
          if (this.pointer?.pane.paneId === closedId) this.pointer = undefined;
          if (this.selectedPane?.paneId === closedId) this.selectedPane = undefined;
          void this.updateComplete.then(() => {
            this.animateReorder(previous);
            if (focusChanged) {
              if (!this.mobile) this.focusedPane()?.focusInput();
              if (this.layoutMode === 'cards' && !this.mobile) this.finishFocusStack(nextFocus, transition);
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
          this.dispatchSession({ type: 'paneMetadata', metadata: message.msg.value });
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
        case 'macrosSnapshot': {
          const snap = message.msg.value;
          if (snap.macros) {
            const macros = snap.macros.map(wireToLocalMacro);
            this.dispatchSession({ type: 'macrosSnapshot', macros });
            setPref('macros', macros);
          }
          break;
        }
        case 'configSnapshot': {
          const config = message.msg.value;
          if (config.widthPresets && config.widthPresets.length > 0) {
            this.widthPresets = config.widthPresets;
          }
          if (config.minColumnWidth) {
            this.minColumnWidth = config.minColumnWidth;
          }
          if (config.maxColumnWidth) {
            this.maxColumnWidth = config.maxColumnWidth;
          }
          if (config.bindings && config.bindings.length > 0) {
            this.keyRouter.setBindings(config.bindings);
          }
          this.requestUpdate();
          break;
        }
        case 'paneNotification': {
          const notif = message.msg.value;
          if (notif.paneId !== this.focusedPaneId) {
            this.notifyBackground(notif.title, notif.message);
          }
          break;
        }
      }
    };

    client.connect();
  }

  private setupKeyboard() {
    const fromFormControl = (e: Event) => e.composedPath().some(node =>
      (node instanceof HTMLInputElement || node instanceof HTMLTextAreaElement ||
       node instanceof HTMLSelectElement || node instanceof HTMLButtonElement) &&
      !(node instanceof HTMLElement && node.classList.contains('clipboard-helper')));
    document.addEventListener('keydown', (e) => {
      if (!this.connected || !this.client) return;
      if (this.showHelp) {
        if (e.key === 'Escape' || e.key === '?' || (e.ctrlKey && e.key === 'c')) {
          this.closeHelp();
          e.preventDefault();
        }
        return;
      }
      if (this.showSettings) {
        if (e.key === 'Escape' || e.key === ',' || (e.ctrlKey && e.key === 'c')) {
          this.closeSettings();
          e.preventDefault();
        }
        return;
      }
      if (this.showCommandPalette) {
        if (e.key === 'Escape' || (e.ctrlKey && e.key === 'c')) {
          this.closeCommandPalette();
          e.preventDefault();
        }
        return;
      }
      if (this.showCommandMenu) {
        if (e.key === 'Escape' || (e.ctrlKey && e.key === 'c')) {
          this.closeCommandMenu();
          e.preventDefault();
        }
        return;
      }
      if (this.contextMenu.open) {
        if (e.key === 'Escape') {
          this.closeContextMenu();
          e.preventDefault();
        }
        return;
      }
      if (fromFormControl(e)) return;
      if ((e.ctrlKey || e.metaKey) && (e.key === 'f' || e.code === 'KeyF')) {
        e.preventDefault();
        this.startSearch();
        return;
      }
      if (e.isComposing || e.key === 'Process' || e.key === 'Dead') return;

      // macOS Shortcuts (Cmd+C / Cmd+V) - only on Apple platforms
      if (!this.keyRouter.inPrefix && isApplePlatform() && e.metaKey && !e.ctrlKey && !e.altKey) {
        if (e.key === 'c' || e.key === 'C') {
          const text = this.selectedPane?.selectedText() || this.focusedPane()?.selectedText() || '';
          if (text) {
            void this.copyToClipboard(text);
            this.selectedPane?.clearSelection();
            this.selectedPane = undefined;
            e.preventDefault();
            return;
          }
          return;
        }
        if (e.key === 'v' || e.key === 'V') {
          void this.pasteFromClipboard();
          return;
        }
      }

      // Linux / Windows Shortcuts (Ctrl+C, Ctrl+Shift+C, Ctrl+V, Ctrl+Shift+V)
      if (!this.keyRouter.inPrefix && e.ctrlKey && !e.metaKey && !e.altKey) {
        if (e.shiftKey && (e.key === 'c' || e.key === 'C')) {
          const text = this.selectedPane?.selectedText() || this.focusedPane()?.selectedText() || '';
          if (text) {
            void this.copyToClipboard(text);
            this.selectedPane?.clearSelection();
            this.selectedPane = undefined;
          }
          e.preventDefault();
          return;
        }
        if (!e.shiftKey && (e.key === 'c' || e.key === 'C')) {
          const text = this.selectedPane?.selectedText() || this.focusedPane()?.selectedText() || '';
          if (text) {
            void this.copyToClipboard(text);
            this.selectedPane?.clearSelection();
            this.selectedPane = undefined;
            e.preventDefault();
            return;
          }
          // No selection: falls through to terminal SIGINT
        }
        if (e.key === 'v' || e.key === 'V') {
          void this.pasteFromClipboard();
          return;
        }
      }

      if (e.shiftKey && !e.ctrlKey && !e.altKey && !e.metaKey && e.key === 'Insert') {
        void this.pasteFromClipboard();
        return;
      }

      if (this.searchController.handleTerminalKeydown(e)) {
        this.requestUpdate();
        return;
      }

      const action = this.keyRouter.handle(e);
      this.dispatchKeyAction(action, e);
    }, { signal: this.listeners?.signal });

    document.addEventListener('copy', (e) => {
      if (!this.connected || !this.client || fromFormControl(e)) return;
      const text = this.selectedPane?.selectedText() || this.focusedPane()?.selectedText() || '';
      if (text) {
        e.clipboardData?.setData('text/plain', text);
        if (navigator.clipboard?.writeText) void navigator.clipboard.writeText(text).catch(() => {});
        e.preventDefault();
      }
    }, { signal: this.listeners?.signal });

    document.addEventListener('paste', (e) => {
      if (!this.connected || !this.client || fromFormControl(e)) return;
      const value = e.clipboardData?.getData('text/plain') || '';
      if (this.handlePasteInput(this.focusedPaneId, value)) {
        e.preventDefault();
      }
    }, { signal: this.listeners?.signal });

    this.paneStrip.addEventListener('pane-paste', (e: Event) => {
      const custom = e as CustomEvent<{ paneId: number; text: string }>;
      if (!custom.detail?.text) return;
      this.handlePasteInput(custom.detail.paneId, custom.detail.text);
    }, { signal: this.listeners?.signal });

    document.addEventListener('compositionend', (e) => {
      if (!this.connected || !this.client || fromFormControl(e)) return;
      sendTextInput(this.client, this.focusedPaneId, (e as CompositionEvent).data);
    }, { signal: this.listeners?.signal });
  }

  private forwardKeyEvent(e: KeyboardEvent) {
    if (this.client && sendKeyboardInput(this.client, this.focusedPaneId, e)) {
      this.pendingReveal.add(this.focusedPaneId);
      this.focusedPane()?.revealCursor();
      e.preventDefault();
    }
  }

  private handleVerbAction(verb: VerbType) {
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
      this.client?.send({ case: 'verb', value: { verb, paneId: this.focusedPaneId } });
    }
  }

  private openPrompt() {
    this.openCommandPalette(':');
  }

  private openPalette() {
    this.openCommandPalette();
  }

  private keyActionHandlers: { [K in KeyRouterAction['type']]: (action: Extract<KeyRouterAction, { type: K }>, e: KeyboardEvent) => void } = {
    ignore: (_action, e) => { e.preventDefault(); },
    search: (_action, e) => { this.startSearch(); e.preventDefault(); },
    prompt: (_action, e) => {
      this.openPrompt();
      e.preventDefault();
    },
    palette: (_action, e) => {
      this.openPalette();
      e.preventDefault();
    },
    send_literal_key: (_action, e) => this.forwardKeyEvent(e),
    forward: (_action, e) => this.forwardKeyEvent(e),
    scroll: (action, e) => {
      this.client?.send({ case: 'scroll', value: { paneId: this.focusedPaneId, delta: action.delta } });
      e.preventDefault();
    },
    toggle_cards: (_action, e) => {
      this.setLayoutMode(this.layoutMode === 'cards' ? 'scroll' : 'cards');
      e.preventDefault();
    },
    pan: (action, e) => {
      this.focusedPane()?.panCells(action.direction * 10);
      e.preventDefault();
    },
    toggle_follow_pty: (_action, e) => {
      this.followPTY = !this.followPTY;
      if (this.followPTY) this.displayWidths = Object.fromEntries(this.columns.map(column => [column.paneId, column.width]));
      e.preventDefault();
    },
    focus_column: (action, e) => {
      this.focusColumnByIndex(action.column);
      e.preventDefault();
    },
    toggle_help: (_action, e) => {
      this.toggleHelp();
      e.preventDefault();
    },
    toggle_settings: (_action, e) => {
      this.toggleSettings();
      e.preventDefault();
    },
    quit: (_action, e) => {
      this.client?.send({ case: 'shutdown', value: {} });
      this.client?.disconnect();
      e.preventDefault();
    },
    detach: (_action, e) => {
      this.client?.send({ case: 'detach', value: {} });
      this.client?.disconnect();
      e.preventDefault();
    },
    verb: (action, e) => {
      this.handleVerbAction(action.verb);
      e.preventDefault();
    },
  };

  private dispatchKeyAction(action: KeyRouterAction, e: KeyboardEvent) {
    const handler = this.keyActionHandlers[action.type] as (action: KeyRouterAction, e: KeyboardEvent) => void;
    if (handler) {
      handler(action, e);
    }
  }

  private async copyToClipboard(text: string): Promise<boolean> {
    if (!text) return false;
    let copied = false;
    if (navigator.clipboard?.writeText) {
      try {
        await navigator.clipboard.writeText(text);
        copied = true;
      } catch {}
    }
    if (!copied) {
      const pane = this.selectedPane || this.focusedPane();
      const helper = pane?.helperElement;
      if (helper) {
        helper.value = text;
        helper.select();
        try {
          copied = document.execCommand('copy');
        } catch {}
      } else {
        const textarea = document.createElement('textarea');
        textarea.value = text;
        textarea.style.position = 'fixed';
        textarea.style.opacity = '0';
        document.body.appendChild(textarea);
        textarea.select();
        try {
          copied = document.execCommand('copy');
        } catch {}
        document.body.removeChild(textarea);
      }
    }
    return copied;
  }

  private lastPaste = { text: '', time: 0 };

  private handlePasteInput(paneId: number, text: string): boolean {
    if (!this.connected || !this.client || !text) return false;
    const now = Date.now();
    if (this.lastPaste.text === text && now - this.lastPaste.time < 150) {
      return true;
    }
    this.lastPaste = { text, time: now };
    if (!sendPasteInput(this.client, paneId, text)) return false;
    this.pendingReveal.add(paneId);
    this.focusedPane()?.revealCursor();
    return true;
  }

  private async pasteFromClipboard(): Promise<boolean> {
    if (!this.connected || !this.client) return false;
    if (navigator.clipboard?.readText) {
      try {
        const text = await navigator.clipboard.readText();
        if (text) {
          return this.handlePasteInput(this.focusedPaneId, text);
        }
      } catch {}
    }
    // Fallback: focus helper textarea so user can paste via native shortcut
    this.focusedPane()?.focusInput();
    return false;
  }

  private openContextMenu(x: number, y: number, pane: WideboiPane) {
    const text = pane.selectedText();
    this.contextMenu = {
      open: true,
      x,
      y,
      paneId: pane.paneId,
      hasSelection: Boolean(text),
    };
  }

  private closeContextMenu = () => {
    if (this.contextMenu.open) {
      this.contextMenu = { ...this.contextMenu, open: false };
    }
  };

  private getPaneElement(paneId: number): WideboiPane | undefined {
    return Array.from(this.paneStrip?.querySelectorAll('wideboi-pane') || [])
      .find(element => element.paneId === paneId);
  }

  private handleContextMenuAction = (e: CustomEvent<{ action: ContextMenuAction; paneId: number }>) => {
    const { action, paneId } = e.detail;
    const pane = this.getPaneElement(paneId) || this.focusedPane();
    if (!pane) return;

    if (action === 'copy') {
      const text = pane.selectedText();
      if (text) {
        void this.copyToClipboard(text);
        pane.clearSelection();
        this.selectedPane = undefined;
      }
    } else if (action === 'paste') {
      this.focusPane(paneId);
      void this.pasteFromClipboard();
    } else if (action === 'select-all') {
      this.focusPane(paneId);
      pane.selectAll();
      this.selectedPane = pane;
    }
  };

  private setupMouse() {
    this.paneStrip.addEventListener('scroll', () => {
      if (this.contextMenu.open) this.closeContextMenu();
      if (this.layoutMode === 'cards' && this.paneStrip.scrollLeft) this.paneStrip.scrollLeft = 0;
    }, { signal: this.listeners?.signal });

    this.paneStrip.addEventListener('pointerdown', (e) => {
      if (!this.connected || !this.client) return;
      if (this.mobile) return;
      if (this.contextMenu.open) this.closeContextMenu();
      const pane = this.eventPane(e);
      if (!pane) return;
      if (e.button === 2 && !this.panes.mouseTracking(pane.paneId)) {
        return;
      }
      const start = pane.cellAt(e.clientX, e.clientY);
      this.pointer = {
        id: e.pointerId, pane,
        button: e.button === 0 ? 1 : e.button === 2 ? 3 : 2,
        tracking: !e.metaKey && pane.paneId === this.focusedPaneId && this.panes.mouseTracking(pane.paneId),
        focusOnClick: pane.paneId !== this.focusedPaneId, dragged: false, start,
      };
      pane.setPointerCapture(e.pointerId);
      this.selectedPane?.clearSelection();
      this.selectedPane = undefined;
      if (this.pointer.tracking) this.sendPointerMouse(MouseKind.PRESS, e);
      e.preventDefault();
    }, { signal: this.listeners?.signal });

    this.paneStrip.addEventListener('pointermove', (e) => {
      if (this.mobile) return;
      const press = this.pointer;
      if (!press) {
        const pane = this.eventPane(e);
        if (pane && (pane.paneId === this.focusedPaneId || !pane.cardMode) && !this.panes.mouseTracking(pane.paneId)) {
          if (this.hoveredPane && this.hoveredPane !== pane) {
            this.hoveredPane.setHoverCursor('');
          }
          this.hoveredPane = pane;
          const point = pane.cellAt(e.clientX, e.clientY);
          const urlMatch = findUrlAt(this.panes.get(pane.paneId), point);
          if (urlMatch) {
            pane.setHoverCursor('pointer', urlMatch.url);
          } else {
            pane.setHoverCursor('');
          }
        } else if (this.hoveredPane) {
          this.hoveredPane.setHoverCursor('');
          this.hoveredPane = undefined;
        }
        return;
      }
      if (press.id !== e.pointerId) return;
      const end = press.pane.cellAt(e.clientX, e.clientY);
      if (end.x !== press.start.x || end.y !== press.start.y) press.dragged = true;
      if (press.tracking) this.sendPointerMouse(MouseKind.MOTION, e);
      else if (press.button === 1) {
        press.pane.setSelection(press.start, end);
        this.selectedPane = press.pane;
      }
      e.preventDefault();
    }, { signal: this.listeners?.signal });

    this.paneStrip.addEventListener('pointerleave', () => {
      if (this.hoveredPane) {
        this.hoveredPane.setHoverCursor('');
        this.hoveredPane = undefined;
      }
    }, { signal: this.listeners?.signal });

    const release = (e: PointerEvent) => {
      if (this.mobile) return;
      if (!this.pointer || this.pointer.id !== e.pointerId) return;
      const press = this.pointer;
      this.pointer = undefined;
      if (press.pane.hasPointerCapture(e.pointerId)) press.pane.releasePointerCapture(e.pointerId);

      if (press.tracking) {
        this.sendPointerMouse(MouseKind.RELEASE, e);
      } else if (e.type === 'pointerup') {
        if (press.focusOnClick && !press.dragged) {
          press.pane.clearSelection();
          this.focusPane(press.pane.paneId);
        } else if (press.dragged) {
          const text = selectionText(this.panes.get(press.pane.paneId), press.start,
            press.pane.cellAt(e.clientX, e.clientY));
          if (text) void this.copyToClipboard(text);
          press.pane.focusInput();
        } else {
          const urlMatch = findUrlAt(this.panes.get(press.pane.paneId), press.start);
          if (urlMatch) {
            window.open(urlMatch.url, '_blank', 'noopener,noreferrer');
          } else {
            this.selectedPane?.clearSelection();
            this.selectedPane = undefined;
            press.pane.focusInput();
          }
        }
      }
      e.preventDefault();
    };
    this.paneStrip.addEventListener('pointerup', release, { signal: this.listeners?.signal });
    this.paneStrip.addEventListener('pointercancel', release, { signal: this.listeners?.signal });

    this.paneStrip.addEventListener('click', (e) => {
      if (!this.mobile) return;
      const pane = this.eventPane(e);
      if (pane) {
        const mouseEvent = e as MouseEvent;
        const cell = pane.cellAt(mouseEvent.clientX, mouseEvent.clientY);
        const urlMatch = findUrlAt(this.panes.get(pane.paneId), cell);
        if (urlMatch) {
          window.open(urlMatch.url, '_blank', 'noopener,noreferrer');
          return;
        }
        this.focusPane(pane.paneId);
        if (this.mobileInputMode === 'direct') {
          this.renderRoot.querySelector<HTMLInputElement>('.mobile-input-bar .mobile-direct-input')?.focus();
        } else {
          this.renderRoot.querySelector<HTMLInputElement>('.mobile-input-bar .mobile-draft-input')?.focus();
        }
      }
    }, { signal: this.listeners?.signal });

    this.paneStrip.addEventListener('wheel', (e) => {
      if (this.mobile) return;
      if (!this.connected || !this.client) return;
      // Leave horizontal gestures to the pane viewport or outer strip.
      if (e.shiftKey || Math.abs(e.deltaX) > Math.abs(e.deltaY)) return;
      const pane = this.eventPane(e);
      if (pane) {
        // A short client scrolls within the live terminal grid. Alt+wheel
        // explicitly navigates terminal history instead.
        if (pane.hasVerticalOverflow && !e.altKey) return;

        if (!e.altKey && !e.metaKey && (pane.paneId === this.focusedPaneId || !pane.cardMode) && this.panes.mouseTracking(pane.paneId)) {
          if (sendWheelInput(this.client, pane.paneId, pane.cellAt(e.clientX, e.clientY), e)) {
            e.preventDefault();
            return;
          }
        }

        e.preventDefault();
        // e.deltaY > 0 means scrolling down (towards bottom/newer).
        // e.deltaY < 0 means scrolling up (towards top/older).
        // In MsgScroll, Delta > 0 is up (older), Delta < 0 is down (newer).
        // A standard wheel step is often 3 lines.
        const delta = e.deltaY > 0 ? -3 : 3;
        this.client.send({ case: 'scroll', value: { paneId: pane.paneId, delta } });
      }
    }, { passive: false, signal: this.listeners?.signal });

    this.paneStrip.addEventListener('contextmenu', (e: MouseEvent) => {
      if (!this.connected || !this.client) return;
      const pane = this.eventPane(e);
      if (!pane) return;
      if (this.panes.mouseTracking(pane.paneId)) {
        return;
      }
      e.preventDefault();
      this.openContextMenu(e.clientX, e.clientY, pane);
    }, { signal: this.listeners?.signal });
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



  private handlePaneSelect(paneIdOrEvent: number | CustomEvent<number> | Event) {
    let paneID = 0;
    if (typeof paneIdOrEvent === 'number') {
      paneID = paneIdOrEvent;
    } else if (paneIdOrEvent instanceof CustomEvent && typeof paneIdOrEvent.detail === 'number') {
      paneID = paneIdOrEvent.detail;
    } else if ('detail' in paneIdOrEvent && typeof (paneIdOrEvent as CustomEvent).detail === 'number') {
      paneID = (paneIdOrEvent as CustomEvent).detail;
    } else {
      const select = (paneIdOrEvent as Event).target as HTMLSelectElement;
      paneID = parseInt(select?.value, 10);
    }
    if (paneID > 0 && this.client && this.connected) {
      this.focusPane(paneID);
    }
  }

  get currentZoom(): number {
    return this.zoomForPane(this.focusedPaneId);
  }

  zoomForPane(paneId: number): number {
    const zoom = this.paneZooms.get(paneId);
    if (zoom !== undefined) return zoom;
    if (this.mobile) {
      return this.minZoomForPane(paneId);
    }
    return 1.0;
  }

  minZoomForPane(paneId: number): number {
    const pane = this.panes.get(paneId);
    if (!pane || !this.paneStrip) return 0.5;
    const termWidth = pane.cols * this.cellWidth;
    const termHeight = pane.rows * termSettings.cellHeight;
    const viewWidth = Math.max(1, (this.paneStrip.clientWidth || window.innerWidth) - 2);
    const viewHeight = Math.max(1, (this.paneStrip.clientHeight || (window.innerHeight - 100)) - 2);
    if (termWidth <= 0 || termHeight <= 0) return 0.5;
    const fitX = viewWidth / termWidth;
    const fitY = viewHeight / termHeight;
    const fitZoom = Math.min(fitX, fitY);
    return Math.max(0.25, Math.min(1.0, Math.floor(fitZoom * 100) / 100));
  }

  get currentMinZoom(): number {
    return this.minZoomForPane(this.focusedPaneId);
  }

  private stepZoom(delta: number) {
    if (!this.focusedPaneId) return;
    const current = this.currentZoom;
    const minZoom = this.currentMinZoom;
    const next = Math.max(minZoom, Math.min(2.0, Math.round((current + delta) * 100) / 100));
    if (next !== current) {
      this.paneZooms.set(this.focusedPaneId, next);
      this.requestUpdate();
    }
  }

  private resetZoom() {
    if (!this.focusedPaneId) return;
    const minZoom = this.currentMinZoom;
    const current = this.currentZoom;
    const target = current !== minZoom ? minZoom : 1.0;
    this.paneZooms.set(this.focusedPaneId, target);
    this.requestUpdate();
  }

  private handleZoomChange(e: CustomEvent<{ paneId: number; zoom: number }>) {
    const { paneId, zoom } = e.detail;
    if (paneId && zoom) {
      this.paneZooms.set(paneId, zoom);
      this.requestUpdate();
    }
  }

  private moveMobilePane(delta: number) {
    const index = this.activePanes.indexOf(this.focusedPaneId);
    const next = this.activePanes[index + delta];
    if (next !== undefined) this.focusPane(next);
  }

  private sendMobileKey(key: string, code: string) {
    if (!this.client || !this.connected || !this.focusedPaneId) return;
    const ctrl = this.mobileCtrl;
    const event = new KeyboardEvent('keydown', { key, code, ctrlKey: ctrl });
    if (sendKeyboardInput(this.client, this.focusedPaneId, event)) {
      this.pendingReveal.add(this.focusedPaneId);
      this.focusedPane()?.revealCursor();
    }
    this.mobileCtrl = false;
  }

  private handleMobileDraftKey(e: KeyboardEvent) {
    if (!this.client || !this.connected) return;
    if (e.key === 'Enter') {
      if (this.mobileDraft) {
        this.sendMobileDraft();
      }
      e.preventDefault();
      return;
    }
    if (this.mobileCtrl && e.key.length === 1) {
      this.sendMobileKey(e.key, e.code);
      e.preventDefault();
    } else if (e.ctrlKey || e.altKey || e.key === 'Escape' || e.key === 'Tab' ||
               (this.mobileDraft === '' && e.key.startsWith('Arrow'))) {
      if (sendKeyboardInput(this.client, this.focusedPaneId, e)) {
        this.pendingReveal.add(this.focusedPaneId);
        this.focusedPane()?.revealCursor();
        e.preventDefault();
      }
    }
  }

  private handleMobileDirectKey(e: KeyboardEvent) {
    this.mobileDirectController.handleKeyDown(e);
  }

  private handleMobileDirectInput(e: Event) {
    this.mobileDirectController.handleInput(e);
  }

  private sendMobileDraft() {
    if (!this.client || !this.connected || !this.mobileDraft) return;
    const text = this.mobileDraft;
    if (sendTextInput(this.client, this.focusedPaneId, text)) {
      const enterEvent = typeof KeyboardEvent !== 'undefined'
        ? new KeyboardEvent('keydown', { key: 'Enter', code: 'Enter' })
        : ({ key: 'Enter', code: 'Enter', shiftKey: false, altKey: false, ctrlKey: false, metaKey: false, isComposing: false, repeat: false } as unknown as KeyboardEvent);
      sendKeyboardInput(this.client, this.focusedPaneId, enterEvent);
      this.pendingReveal.add(this.focusedPaneId);
      this.focusedPane()?.revealCursor();
      this.mobileDraft = '';
      const input = this.renderRoot.querySelector<HTMLInputElement>('.mobile-input-bar .mobile-draft-input');
      if (input) input.value = '';
    }
  }

  private executeMobileMacro(macro: Macro) {
    if (!this.client || !this.connected || !this.focusedPaneId) return;
    executeMacro(this.client, this.focusedPaneId, macro);
    this.pendingReveal.add(this.focusedPaneId);
    this.focusedPane()?.revealCursor();
    this.showMacros = false;
  }

  private saveMacrosToServer() {
    if (!this.client || !this.connected) return;
    this.client.send({
      case: 'saveMacros',
      value: {
        macros: this.macros.map(localToWireMacro),
      },
    });
    setPref('macros', this.macros);
  }

  private deleteMacro(index: number) {
    this.macros = this.macros.filter((_, i) => i !== index);
    setPref('macros', this.macros);
  }

  private addDraftStep(type: 'text' | 'key', val: string, ctrl: boolean) {
    if (!val) return;
    if (type === 'text') {
      this.draftMacroSteps = [...this.draftMacroSteps, { text: val }];
    } else {
      this.draftMacroSteps = [...this.draftMacroSteps, {
        key: val,
        code: val.length === 1 ? `Key${val.toUpperCase()}` : val,
        ctrl: ctrl || undefined,
      }];
    }
  }

  private removeDraftStep(index: number) {
    this.draftMacroSteps = this.draftMacroSteps.filter((_, i) => i !== index);
  }

  private resetMacrosToDefault() {
    this.macros = DEFAULT_MACROS;
    setPref('macros', this.macros);
  }

  private openHelp() {
    this.showHelp = true;
    this.showSettings = false;
    this.showCommandMenu = false;
    void this.updateComplete.then(() => {
      this.renderRoot.querySelector<HTMLButtonElement>('.help-dialog .close-btn')?.focus();
    });
  }

  private closeHelp() {
    this.showHelp = false;
    void this.updateComplete.then(() => {
      if (!this.mobile) this.focusedPane()?.focusInput();
    });
  }

  private toggleHelp() {
    if (this.showHelp) {
      this.closeHelp();
    } else {
      this.openHelp();
    }
  }

  private openSettings() {
    this.showSettings = true;
    this.showHelp = false;
    this.showCommandMenu = false;
    void this.updateComplete.then(() => {
      const settingsEl = this.renderRoot.querySelector<WideboiSettings>('wideboi-settings');
      if (settingsEl) {
        void settingsEl.updateComplete.then(() => {
          settingsEl.focusCloseButton();
        });
      }
    });
  }

  private closeSettings() {
    this.showSettings = false;
    void this.updateComplete.then(() => {
      if (!this.mobile) {
        this.focusedPane()?.focusInput();
      } else {
        this.renderRoot.querySelector<HTMLElement>('.mobile-input-bar .mobile-settings-btn')?.focus();
      }
    });
  }

  private toggleSettings() {
    if (this.showSettings) {
      this.closeSettings();
    } else {
      this.openSettings();
    }
  }

  private openCommandMenu() {
    this.showCommandMenu = true;
    this.showCommandPalette = false;
    this.showHelp = false;
    this.showSettings = false;
    void this.updateComplete.then(() => {
      const menuEl = this.renderRoot.querySelector<WideboiCommandMenu>('wideboi-command-menu');
      if (menuEl) {
        void menuEl.updateComplete.then(() => {
          menuEl.focusFirstItem();
        });
      }
    });
  }

  private closeCommandMenu() {
    this.showCommandMenu = false;
    void this.updateComplete.then(() => {
      if (!this.mobile) {
        this.focusedPane()?.focusInput();
      } else {
        this.renderRoot.querySelector<HTMLElement>('.mobile-input-bar .mobile-cmd-btn')?.focus();
      }
    });
  }

  private openCommandPalette(initialQuery = '') {
    this.commandPaletteInitialQuery = initialQuery;
    this.showCommandPalette = true;
    this.showCommandMenu = false;
    this.showHelp = false;
    this.showSettings = false;
    void this.updateComplete.then(() => {
      const paletteEl = this.renderRoot.querySelector<WideboiCommandPalette>('wideboi-command-palette');
      if (paletteEl) {
        void paletteEl.updateComplete.then(() => {
          paletteEl.focusInput();
        });
      }
    });
  }

  private closeCommandPalette() {
    this.showCommandPalette = false;
    this.commandPaletteInitialQuery = '';
    void this.updateComplete.then(() => {
      if (!this.mobile) {
        this.focusedPane()?.focusInput();
      } else {
        this.renderRoot.querySelector<HTMLElement>('.mobile-input-bar .mobile-cmd-btn')?.focus();
      }
    });
  }

  private handlePromptCommand(line: string) {
    line = line.trim();
    if (!line) return;
    const parts = line.split(/\s+/);
    const cmd = parts[0].toLowerCase();
    const rest = line.slice(parts[0].length).trim();

    switch (cmd) {
      case 'new':
      case 'n':
      case 'new-column':
        this.client?.send({
          case: 'splitRequest',
          value: { command: rest, afterPaneId: this.focusedPaneId, keep: false, cwd: '' },
        });
        break;
      case 'split':
        this.client?.send({
          case: 'splitRequest',
          value: { command: rest, afterPaneId: this.focusedPaneId, keep: false, cwd: '' },
        });
        break;
      case 'run':
      case 'sh':
      case '!':
      case 'exec':
        this.client?.send({
          case: 'splitRequest',
          value: { command: rest, afterPaneId: this.focusedPaneId, keep: true, cwd: '' },
        });
        break;
      case 'close':
      case 'kill':
      case 'kill-pane':
      case 'x':
        if (parts[1]) {
          const id = parseInt(parts[1], 10);
          if (!isNaN(id)) {
            this.client?.send({ case: 'closePaneRequest', value: { paneId: id } });
            break;
          }
        }
        this.handleVerbAction(VerbType.KILL_PANE);
        break;
      case 'width':
      case 'set-width': {
        const w = parseInt(parts[1], 10);
        if (!isNaN(w) && this.focusedPaneId) {
          this.client?.send({ case: 'setPaneWidth', value: { paneId: this.focusedPaneId, width: w } });
        }
        break;
      }
      case 'rename-pane':
      case 'title':
      case 'label': {
        if (!this.focusedPaneId) break;
        let targetId = this.focusedPaneId;
        let newTitle = rest;
        let clear = false;
        if (parts[1]) {
          const parsedId = parseInt(parts[1], 10);
          if (!isNaN(parsedId)) {
            targetId = parsedId;
            newTitle = line.slice(parts[0].length).trim().slice(parts[1].length).trim();
          }
        }
        if (
          (newTitle.startsWith('"') && newTitle.endsWith('"')) ||
          (newTitle.startsWith("'") && newTitle.endsWith("'"))
        ) {
          newTitle = newTitle.slice(1, -1);
        }
        if (!newTitle) {
          clear = true;
        }
        this.client?.send({
          case: 'renamePaneRequest',
          value: { paneId: targetId, title: newTitle, clear },
        });
        break;
      }
      case 'cards':
        this.setLayoutMode('cards');
        break;
      case 'scroll':
        this.setLayoutMode('scroll');
        break;
      case 'search':
        this.startSearch();
        break;
      case 'help':
        this.openHelp();
        break;
      case 'settings':
        this.openSettings();
        break;
      case 'paste':
        void this.pasteFromClipboard();
        break;
      case 'quit':
      case 'q':
        this.client?.send({ case: 'shutdown', value: {} });
        break;
      case 'detach':
      case 'd':
        this.client?.send({ case: 'detach', value: {} });
        break;
      case 'move-left':
      case 'ml':
        this.handleVerbAction(VerbType.MOVE_LEFT);
        break;
      case 'move-right':
      case 'mr':
        this.handleVerbAction(VerbType.MOVE_RIGHT);
        break;
      case 'focus-left':
      case 'h':
        this.handleVerbAction(VerbType.FOCUS_LEFT);
        break;
      case 'focus-right':
      case 'l':
        this.handleVerbAction(VerbType.FOCUS_RIGHT);
        break;
      case 'smart-jump':
      case 'j':
        this.handleVerbAction(VerbType.SMART_JUMP);
        break;
      case 'toggle-status':
      case 'status':
      case 's':
        this.handleVerbAction(VerbType.TOGGLE_STATUS);
        break;
      case 'grow-width':
      case '+':
        this.cycleWidth(VerbType.GROW_WIDTH);
        break;
      case 'shrink-width':
      case '-':
        this.cycleWidth(VerbType.SHRINK_WIDTH);
        break;
      case 'cycle-width':
      case 'w':
        this.cycleWidth(VerbType.CYCLE_WIDTH);
        break;
      default:
        console.warn(`[wideboi] unknown prompt command: ${cmd}`);
        break;
    }
  }

  private handleCommandMenuAction(cmd: string) {
    this.closeCommandMenu();
    switch (cmd) {
      case 'palette':
        this.openCommandPalette();
        break;
      case 'prompt':
        this.openCommandPalette(':');
        break;
      case 'paste':
        void this.pasteFromClipboard();
        break;
      case 'new-pane':
        this.handleVerbAction(VerbType.NEW_COLUMN);
        break;
      case 'split':
        this.client?.send({
          case: 'splitRequest',
          value: { command: '', afterPaneId: this.focusedPaneId, keep: false, cwd: '' },
        });
        break;
      case 'close-pane':
        this.handleVerbAction(VerbType.KILL_PANE);
        break;
      case 'search':
        this.startSearch();
        break;
      case 'toggle-cards':
        this.setLayoutMode(this.layoutMode === 'cards' ? 'scroll' : 'cards');
        break;
      case 'toggle-follow-pty':
        this.followPTY = !this.followPTY;
        if (this.followPTY) this.displayWidths = Object.fromEntries(this.columns.map(column => [column.paneId, column.width]));
        break;
      case 'grow-width':
        this.cycleWidth(VerbType.GROW_WIDTH);
        break;
      case 'shrink-width':
        this.cycleWidth(VerbType.SHRINK_WIDTH);
        break;
      case 'cycle-width':
        this.cycleWidth(VerbType.CYCLE_WIDTH);
        break;
      case 'move-left':
        this.handleVerbAction(VerbType.MOVE_LEFT);
        break;
      case 'move-right':
        this.handleVerbAction(VerbType.MOVE_RIGHT);
        break;
      case 'focus-left':
        this.handleVerbAction(VerbType.FOCUS_LEFT);
        break;
      case 'focus-right':
        this.handleVerbAction(VerbType.FOCUS_RIGHT);
        break;
      case 'smart-jump':
        this.handleVerbAction(VerbType.SMART_JUMP);
        break;
      case 'settings':
        this.openSettings();
        break;
      case 'help':
        this.openHelp();
        break;
    }
  }

  private async handleFontChange(e: Event) {
    const select = e.target as HTMLSelectElement;
    termSettings.fontFamily = select.value;
    termSettings.save();
    try { await document.fonts.load(termSettings.font); } catch (e) {}
    this.cellWidth = measureCellWidth();
    this.requestUpdate();
    this.sendResizeIfChanged();
  }

  private applyFontSize(size: number) {
    const clamped = Math.max(8, Math.min(48, size));
    termSettings.fontSize = clamped;
    termSettings.save();
    this.cellWidth = measureCellWidth();
    this.requestUpdate();
    this.sendResizeIfChanged();
  }

  private handleFontSizeChange = (size: number) => {
    this.applyFontSize(size);
  };

  private stepFontSize(delta: number) {
    this.applyFontSize(termSettings.fontSize + delta);
  }

  private resetFontSize() {
    this.applyFontSize(14);
  }

  private handlePrefixChange = (val: string) => {
    this.prefixSetting = val;
    setPref('prefix', val);
    this.keyRouter.setPrefix(val);
  };

  private handleTabKeydown(e: KeyboardEvent, currentId: number) {
    const panes = this.activePanes;
    const currentIndex = panes.indexOf(currentId);
    if (currentIndex === -1) return;

    let targetId: number | null = null;
    if (e.key === 'ArrowRight' || e.key === 'ArrowDown') {
      e.preventDefault();
      targetId = panes[(currentIndex + 1) % panes.length];
    } else if (e.key === 'ArrowLeft' || e.key === 'ArrowUp') {
      e.preventDefault();
      targetId = panes[(currentIndex - 1 + panes.length) % panes.length];
    } else if (e.key === 'Home') {
      e.preventDefault();
      targetId = panes[0];
    } else if (e.key === 'End') {
      e.preventDefault();
      targetId = panes[panes.length - 1];
    }

    if (targetId !== null) {
      this.focusPane(targetId);
      void this.updateComplete.then(() => {
        const nextButton = this.shadowRoot?.querySelector<HTMLButtonElement>(`.pane-tab[data-pane-id="${targetId}"]`);
        nextButton?.focus();
      });
    }
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
      this.stripScrollLeft = this.paneStrip.scrollLeft;
      // Cancel a smooth focus reveal still in progress in the strip.
      this.paneStrip.scrollTo({ left: 0, behavior: 'instant' });
    }
    this.dispatchSession({ type: 'setLayoutMode', mode });
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

  private handleLayoutSelect = (mode: 'cards' | 'scroll') => {
    this.setLayoutMode(mode);
  };

  override willUpdate(changedProperties: PropertyValues) {
    super.willUpdate(changedProperties);
    const cards = this.layoutMode === 'cards' && !this.mobile;
    if (cards && this.columns.length > 0) {
      const displayWidth = (column: ColumnData) =>
        this.displayWidths[column.paneId] ?? column.width;
      const displayColumns = this.columns.map(column => ({ paneId: column.paneId, width: displayWidth(column) }));
      const stackFocusId = this.stackFocusId === null ? null :
        (this.activePanes.includes(this.stackFocusId) ? this.stackFocusId : this.focusedPaneId);
      const layout = cardLayout(
        displayColumns,
        this.focusedPaneId,
        Math.floor(this.cardViewportWidth / this.cellWidth),
        this.cardFirst,
        stackFocusId,
      );
      this.cardFirst = layout.first;
    }
  }

  render() {
    const cards = this.layoutMode === 'cards' && !this.mobile;
    const displayWidth = (column: ColumnData) =>
      this.mobile ? Math.max(1, Math.floor(this.cardViewportWidth / this.cellWidth)) :
      this.displayWidths[column.paneId] ?? column.width;
    const displayColumns = this.columns.map(column => ({ paneId: column.paneId, width: displayWidth(column) }));
    const stackFocusId = this.stackFocusId === null ? null :
      (this.activePanes.includes(this.stackFocusId) ? this.stackFocusId : this.focusedPaneId);
    const layout = cards ? cardLayout(displayColumns, this.focusedPaneId,
      Math.floor(this.cardViewportWidth / this.cellWidth), this.cardFirst, stackFocusId) : undefined;
    const placements = new Map(layout?.placements.map(p => [p.paneId, p]));
    return html`
      <div class="terminal-shell">
        <div class=${this.mobile ? 'pane-strip mobile' : cards ? 'pane-strip cards' : 'pane-strip'}>
          ${repeat(this.columns, column => column.paneId, column => {
            const placement = placements.get(column.paneId);
            return html`
            <wideboi-pane
              style=${`width: ${this.mobile ? '100%' : `${displayWidth(column) * this.cellWidth}px`}; --divider-width: ${cards || this.mobile ? 0 : this.cellWidth}px; ${cards ? `left: ${(placement?.left ?? 0) * this.cellWidth}px; z-index: ${placement?.z ?? 0}; visibility: ${placement?.visible ? 'visible' : 'hidden'}` : ''}`}
              .paneId=${column.paneId}
              .pane=${this.panes.get(column.paneId)}
              .theme=${this.currentTheme}
              .focused=${column.paneId === this.focusedPaneId}
              .cardMode=${cards}
              .cardLabel=${`[${column.paneId}] ${this.paneTitles[column.paneId] || 'Terminal'}`}
              .running=${this.connected}
              .cellWidth=${this.cellWidth}
              .displayCols=${displayWidth(column)}
              .zoom=${this.zoomForPane(column.paneId)}
              .minZoom=${this.minZoomForPane(column.paneId)}
              .stats=${this.stats}
              @zoom-change=${this.handleZoomChange}
              aria-label=${`Pane ${column.paneId}`}
            ></wideboi-pane>
          `; })}
          ${cards && layout?.hiddenLeft ? html`<span class="card-count left">+${layout.hiddenLeft}</span>` : ''}
          ${cards && layout?.hiddenRight ? html`<span class="card-count right">+${layout.hiddenRight}</span>` : ''}
        </div>
      </div>
      ${this.connected ? html`
        <div class="toolbar ${this.searchState ? 'searching' : ''}">
          <div class="pane-tabs-wrapper ${this.searchState ? 'searching' : ''}">
            <div class="pane-tabs" role="tablist" aria-label="Terminal Panes">
              ${repeat(this.activePanes, id => id, id => {
                const isFocused = id === this.focusedPaneId;
                const status = this.paneStatuses[id] ?? PaneStatus.IDLE;
                const pane = this.panes.get(id);
                const scrollInfo = (pane && pane.scrollOffset > 0)
                  ? `+${pane.scrollOffset}${pane.unreadOutput ? ' ⤓' : ''}`
                  : '';
                const glyph = status === PaneStatus.WORKING ? '»'
                  : status === PaneStatus.NEEDS_INPUT ? '!'
                  : status === PaneStatus.DONE ? '✓'
                  : status === PaneStatus.FAILED ? '✗' : '';
                const statusClass = status === PaneStatus.WORKING ? 'working'
                  : status === PaneStatus.NEEDS_INPUT ? 'needs-input'
                  : status === PaneStatus.DONE ? 'done'
                  : status === PaneStatus.FAILED ? 'failed' : '';
                const title = this.paneTitles[id] || 'Terminal';
                const statusLabel = status === PaneStatus.WORKING ? ', working'
                  : status === PaneStatus.NEEDS_INPUT ? ', needs input'
                  : status === PaneStatus.DONE ? ', done'
                  : status === PaneStatus.FAILED ? ', failed' : '';
                const scrollLabel = scrollInfo ? `, scroll ${scrollInfo}` : '';
                const ariaLabel = `Pane ${id}: ${title}${statusLabel}${scrollLabel}`;
                return html`
                  <button
                    type="button"
                    class="pane-tab"
                    role="tab"
                    data-pane-id=${id}
                    aria-selected=${isFocused ? 'true' : 'false'}
                    aria-label=${ariaLabel}
                    title=${title}
                    tabindex=${isFocused ? 0 : -1}
                    @click=${() => this.focusPane(id)}
                    @keydown=${(e: KeyboardEvent) => this.handleTabKeydown(e, id)}
                  >
                    <span class="tab-focus-dot">${isFocused ? '●' : '○'}</span>
                    <span class="tab-id">[${id}]</span>
                    <span class="tab-title">${title}</span>
                    ${glyph ? html`<span class="tab-status ${statusClass}">${glyph}</span>` : ''}
                    ${scrollInfo ? html`<span class="tab-scroll">[${scrollInfo}]</span>` : ''}
                  </button>
                `;
              })}
            </div>
            ${this.searchState ? html`
              <div class="status search-bar">
                <span>search /</span>
                <input
                  class="search-input"
                  type="text"
                  .value=${this.searchState.query}
                  @input=${(e: InputEvent) => {
                    this.searchController.setQuery((e.target as HTMLInputElement).value);
                    this.requestUpdate();
                  }}
                  @keydown=${(e: KeyboardEvent) => {
                    this.searchController.handleInputKeydown(e);
                    this.requestUpdate();
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
            ` : ''}
          </div>
          <label for="pane-width">Pane width:</label>
          <input id="pane-width" aria-label="Pane width" type="number" min="20" max="4096"
            .value=${String(this.displayWidths[this.focusedPaneId] ?? '')} @change=${this.handleWidthInput}>
          <label><input type="checkbox" aria-label="Follow PTY widths" .checked=${this.followPTY}
            @change=${() => { this.followPTY = !this.followPTY; if (this.followPTY) this.displayWidths = Object.fromEntries(this.columns.map(column => [column.paneId, column.width])); }}>Follow PTY</label>
          <button class="claim-size-btn" @click=${this.claimSize} title="Fit session terminal size to this window">Fit to Window</button>
          <button class="claim-size-btn search-btn" @click=${this.startSearch} title="Search pane history (/ or Ctrl+F)">Search</button>
          <button class="settings-btn" @click=${this.toggleSettings} aria-label="Settings" title="Settings (${this.keyRouter.prefixLabel} ,)">⚙ Settings</button>
          <button class="help-btn" @click=${this.toggleHelp} aria-label="Help">Help (?)</button>
          <span class="tip">(Tip: ${this.keyRouter.prefixLabel} then arrows or h/l to switch, ? for help, , for settings)</span>
        </div>
        <wideboi-mobile-bar
          .activePanes=${this.activePanes}
          .focusedPaneId=${this.focusedPaneId}
          .paneTitles=${this.paneTitles}
          .paneStatuses=${this.paneStatuses}
          .paneScrolls=${this.paneScrolls}
          .currentZoom=${this.currentZoom}
          .currentMinZoom=${this.currentMinZoom}
          @pane-move=${(e: CustomEvent<number>) => this.moveMobilePane(e.detail)}
          @pane-select=${(e: CustomEvent<number>) => this.handlePaneSelect(e.detail)}
          @zoom-step=${(e: CustomEvent<number>) => this.stepZoom(e.detail)}
          @zoom-reset=${() => this.resetZoom()}
        ></wideboi-mobile-bar>
        <div class="mobile-dock">
          <div class="mobile-input-bar">
            <div class="mobile-mode-toggle" role="radiogroup" aria-label="Input mode">
              <button
                class=${this.mobileInputMode === 'draft' ? 'active' : ''}
                aria-pressed=${this.mobileInputMode === 'draft'}
                aria-label="Draft mode"
                @click=${() => { this.mobileInputMode = 'draft'; }}>Draft</button>
              <button
                class=${this.mobileInputMode === 'direct' ? 'active' : ''}
                aria-pressed=${this.mobileInputMode === 'direct'}
                aria-label="Direct input mode"
                @click=${() => {
                  this.mobileInputMode = 'direct';
                  void this.updateComplete.then(() => {
                    this.renderRoot.querySelector<HTMLInputElement>('.mobile-input-bar input.mobile-direct-input')?.focus();
                  });
                }}>Direct</button>
            </div>
            ${this.mobileInputMode === 'draft' ? html`
              <input
                type="text"
                class="mobile-draft-input"
                aria-label="Command or response"
                placeholder="Command or response"
                autocapitalize="off"
                autocorrect="off"
                spellcheck="false"
                .value=${this.mobileDraft}
                @input=${(e: Event) => { this.mobileDraft = (e.target as HTMLInputElement).value; }}
                @keydown=${this.handleMobileDraftKey} />
              <button class="mobile-send-btn" aria-label="Send text" ?disabled=${!this.mobileDraft} @click=${this.sendMobileDraft}>Send</button>
            ` : html`
              <input
                type="text"
                class="mobile-direct-input"
                aria-label="Direct terminal input"
                placeholder="Direct terminal keys…"
                autocapitalize="off"
                autocorrect="off"
                spellcheck="false"
                @focus=${(e: FocusEvent) => this.mobileDirectController.handleFocus(e)}
                @blur=${(e: FocusEvent) => this.mobileDirectController.handleBlur(e)}
                @click=${(e: MouseEvent) => this.mobileDirectController.handleClick(e)}
                @keydown=${this.handleMobileDirectKey}
                @beforeinput=${(e: InputEvent) => this.mobileDirectController.handleBeforeInput(e)}
                @input=${this.handleMobileDirectInput}
                @compositionstart=${(e: CompositionEvent) => this.mobileDirectController.handleCompositionStart(e)}
                @compositionend=${(e: CompositionEvent) => this.mobileDirectController.handleCompositionEnd(e)} />
            `}
            <button
              class=${this.showMacros ? 'mobile-macros-btn active' : 'mobile-macros-btn'}
              aria-label="Macros panel"
              aria-expanded=${this.showMacros}
              @click=${() => { this.showMacros = !this.showMacros; }}>Macros</button>
            <button class="mobile-cmd-btn" aria-label="Command menu" title="Command menu" @click=${this.openCommandMenu}>⌘</button>
            <button class="mobile-settings-btn" aria-label="Settings" title="Settings" @click=${this.toggleSettings}>⚙</button>
          </div>
        </div>
        ${this.showMacros ? html`
          <div class="mobile-macros-sheet" role="region" aria-label="Macros list">
            <div class="mobile-macros-header">
              <span>Keys & Macros</span>
              <div class="sheet-actions">
                <select aria-label="Color theme" @change=${this.handleThemeSelect} class="mobile-theme-select">
                  ${listThemes().map(t => html`
                    <option value=${t.id} .selected=${t.id === this.themeId}>${t.name}</option>
                  `)}
                </select>
                <button aria-label="Edit macros" @click=${() => { this.showMacroEditor = true; this.showMacros = false; }}>Edit</button>
                <button class="close-btn" aria-label="Close macros" @click=${() => { this.showMacros = false; }}>✕</button>
              </div>
            </div>
            <div class="mobile-keys-section">
              <div class="mobile-keys" aria-label="Terminal keys">
                <button aria-label="Escape key" @click=${() => this.sendMobileKey('Escape', 'Escape')}>Esc</button>
                <button aria-label="Tab key" @click=${() => this.sendMobileKey('Tab', 'Tab')}>Tab</button>
                <button aria-label="Control modifier" aria-pressed=${this.mobileCtrl}
                  @click=${() => { this.mobileCtrl = !this.mobileCtrl; }}>Ctrl</button>
                <button aria-label="Left arrow key" @click=${() => this.sendMobileKey('ArrowLeft', 'ArrowLeft')}>←</button>
                <button aria-label="Down arrow key" @click=${() => this.sendMobileKey('ArrowDown', 'ArrowDown')}>↓</button>
                <button aria-label="Up arrow key" @click=${() => this.sendMobileKey('ArrowUp', 'ArrowUp')}>↑</button>
                <button aria-label="Right arrow key" @click=${() => this.sendMobileKey('ArrowRight', 'ArrowRight')}>→</button>
                <button aria-label="Backspace key" @click=${() => this.sendMobileKey('Backspace', 'Backspace')}>⌫</button>
                <button aria-label="Enter key" @click=${() => this.sendMobileKey('Enter', 'Enter')}>↵</button>
              </div>
              ${this.mobileCtrl ? html`
                <div class="mobile-ctrl-palette" aria-label="Ctrl shortcuts">
                  <button aria-label="C key" @click=${() => this.sendMobileKey('c', 'KeyC')}>^C</button>
                  <button aria-label="D key" @click=${() => this.sendMobileKey('d', 'KeyD')}>^D</button>
                  <button aria-label="Z key" @click=${() => this.sendMobileKey('z', 'KeyZ')}>^Z</button>
                  <button aria-label="R key" @click=${() => this.sendMobileKey('r', 'KeyR')}>^R</button>
                  <button aria-label="L key" @click=${() => this.sendMobileKey('l', 'KeyL')}>^L</button>
                  <button aria-label="A key" @click=${() => this.sendMobileKey('a', 'KeyA')}>^A</button>
                  <button aria-label="E key" @click=${() => this.sendMobileKey('e', 'KeyE')}>^E</button>
                  <button aria-label="W key" @click=${() => this.sendMobileKey('w', 'KeyW')}>^W</button>
                  <button aria-label="K key" @click=${() => this.sendMobileKey('k', 'KeyK')}>^K</button>
                  <button aria-label="U key" @click=${() => this.sendMobileKey('u', 'KeyU')}>^U</button>
                </div>
              ` : ''}
            </div>
            <div class="mobile-macros-grid">
              ${this.macros.map(macro => html`
                <button class="mobile-macro-item" @click=${() => this.executeMobileMacro(macro)} aria-label=${`Run macro ${macro.name}`}>
                  <span class="mobile-macro-name">${macro.name}</span>
                  ${macroEndsWithEnter(macro) ? html`<span class="mobile-macro-enter" title="Ends with Enter">↵</span>` : ''}
                </button>
              `)}
            </div>
          </div>
        ` : ''}
      ` : ''}
      ${this.showMacroEditor ? html`
        <div class="macro-editor-modal" @click=${() => { this.showMacroEditor = false; }}>
          <div class="macro-editor-dialog" @click=${(e: Event) => e.stopPropagation()} role="dialog" aria-modal="true" aria-labelledby="macro-editor-title">
            <div class="mobile-macros-header">
              <span id="macro-editor-title">Configure Macros</span>
              <button class="close-btn" aria-label="Close editor" @click=${() => { this.showMacroEditor = false; }}>✕</button>
            </div>
            <div class="macro-editor-list" aria-label="Configured macros">
              ${this.macros.map((m, idx) => html`
                <div class="macro-editor-row">
                  <span>${m.name} ${macroEndsWithEnter(m) ? '↵' : ''}</span>
                  <button aria-label=${`Delete macro ${m.name}`} @click=${() => this.deleteMacro(idx)}>Delete</button>
                </div>
              `)}
            </div>
            <form class="macro-editor-form" @submit=${(e: Event) => {
              e.preventDefault();
              const form = e.target as HTMLFormElement;
              const name = (form.elements.namedItem('macroName') as HTMLInputElement).value.trim();
              const type = (form.elements.namedItem('macroType') as HTMLSelectElement).value as 'text' | 'key';
              const val = (form.elements.namedItem('macroVal') as HTMLInputElement).value;
              const ctrl = (form.elements.namedItem('macroCtrl') as HTMLInputElement).checked;
              const enter = (form.elements.namedItem('macroEnter') as HTMLInputElement).checked;

              let steps: MacroStep[] = [];
              if (this.draftMacroSteps.length > 0) {
                steps = [...this.draftMacroSteps];
                if (enter && !macroEndsWithEnter({ name, steps })) {
                  steps.push({ key: 'Enter', code: 'Enter' });
                }
              } else if (name && val) {
                if (type === 'text') {
                  steps.push({ text: val });
                } else {
                  steps.push({
                    key: val,
                    code: val.length === 1 ? `Key${val.toUpperCase()}` : val,
                    ctrl,
                  });
                }
                if (enter) {
                  steps.push({ key: 'Enter', code: 'Enter' });
                }
              }

              if (name && steps.length > 0) {
                this.macros = [...this.macros, { name, steps }];
                this.draftMacroSteps = [];
                setPref('macros', this.macros);
                form.reset();
              }
            }}>
              <strong>Add New Macro</strong>
              <input name="macroName" placeholder="Macro Name (e.g. Git Log or Vim Quit)" required />
              ${this.draftMacroSteps.length > 0 ? html`
                <div class="macro-draft-steps" aria-label="Steps in new macro">
                  <span style="font-size: 11px; color: var(--wb-fg-muted, #aaa);">Sequence steps:</span>
                  ${this.draftMacroSteps.map((step, idx) => html`
                    <div class="macro-draft-step-row">
                      <span>${idx + 1}. ${step.text ? `Text: "${step.text}"` : `Key: ${step.ctrl ? '^' : ''}${step.key}`}</span>
                      <button type="button" @click=${() => this.removeDraftStep(idx)}>✕</button>
                    </div>
                  `)}
                </div>
              ` : ''}
              <div style="display: flex; gap: 0.3rem;">
                <select name="macroType" style="flex: 0 0 110px;">
                  <option value="text">Text</option>
                  <option value="key">Key</option>
                </select>
                <input name="macroVal" placeholder="Text or key name (e.g. git log or r)" style="flex: 1;" />
              </div>
              <div style="display: flex; gap: 0.8rem; font-size: 12px; align-items: center; flex-wrap: wrap;">
                <label><input type="checkbox" name="macroCtrl" /> Ctrl</label>
                <label><input type="checkbox" name="macroEnter" checked /> Include Enter</label>
                <button type="button" aria-label="Add Step to Sequence" style="margin-left: auto; padding: 0.2rem 0.5rem; font-size: 11px; background: var(--wb-bg-btn, #3c3c3c); color: var(--wb-fg-primary, #eee); border: 1px solid var(--wb-border-divider, #555); border-radius: 3px; cursor: pointer;"
                  @click=${(e: Event) => {
                    const btn = e.target as HTMLElement;
                    const form = btn.closest('form') as HTMLFormElement;
                    const type = (form.elements.namedItem('macroType') as HTMLSelectElement).value as 'text' | 'key';
                    const input = form.elements.namedItem('macroVal') as HTMLInputElement;
                    const ctrl = (form.elements.namedItem('macroCtrl') as HTMLInputElement).checked;
                    if (input.value) {
                      this.addDraftStep(type, input.value, ctrl);
                      input.value = '';
                    }
                  }}>+ Add Step</button>
              </div>
              <button type="submit">Add Macro</button>
            </form>
            <div class="macro-editor-actions">
              <button @click=${this.resetMacrosToDefault}>Reset Defaults</button>
              <button class="primary" aria-label="Save to Server" @click=${() => {
                this.saveMacrosToServer();
                this.showMacroEditor = false;
              }}>Save to Server</button>
            </div>
          </div>
        </div>
      ` : ''}
      ${this.stats ? html`<pre class="stats-overlay">${this.statsText || 'stats: collecting…'}</pre>` : ''}
      ${this.showHelp ? html`
        <div class="help-overlay" @click=${this.closeHelp}>
          <div class="help-dialog" role="dialog" aria-modal="true" aria-labelledby="help-title" @click=${(e: Event) => e.stopPropagation()}>
            <h3 id="help-title">
              <span>wideboi Shortcuts</span>
              <button class="close-btn" @click=${this.closeHelp} aria-label="Close help">×</button>
            </h3>
            <p style="margin-top: 0; color: var(--wb-fg-muted, #aaa);">Prefix: <kbd>${this.keyRouter.prefixLabel}</kbd> (press twice to send literal key)</p>

            <table class="help-table">
              <thead><tr><th>Key after prefix</th><th>Action</th></tr></thead>
              <tbody>
                ${this.keyRouter.helpEntries.map(entry => html`
                  <tr>
                    <td>${entry.keys.map((k, i) => html`${i > 0 ? ' / ' : ''}<kbd>${k}</kbd>`)}</td>
                    <td>${entry.description}</td>
                  </tr>
                `)}
              </tbody>
            </table>
          </div>
        </div>
      ` : ''}
      ${this.showSettings ? html`
        <wideboi-settings
          .open=${this.showSettings}
          .themeId=${this.themeId}
          .prefixSetting=${this.prefixSetting}
          .layoutMode=${this.layoutMode}
          .cards=${cards}
          .fontFamily=${termSettings.fontFamily}
          .fontSize=${termSettings.fontSize}
          .fontSpec=${termSettings.font}
          @close=${this.closeSettings}
          @theme-change=${(e: CustomEvent<string>) => this.setTheme(e.detail)}
          @prefix-change=${(e: CustomEvent<string>) => this.handlePrefixChange(e.detail)}
          @layout-change=${(e: CustomEvent<'cards' | 'scroll'>) => this.handleLayoutSelect(e.detail)}
          @font-family-change=${(e: CustomEvent<string>) => {
            this.handleFontChange({ target: { value: e.detail } } as unknown as Event);
          }}
          @font-size-change=${(e: CustomEvent<number>) => this.handleFontSizeChange(e.detail)}
          @font-size-step=${(e: CustomEvent<number>) => this.stepFontSize(e.detail)}
          @font-size-reset=${() => this.resetFontSize()}
        ></wideboi-settings>
      ` : ''}
      ${this.showCommandMenu ? html`
        <wideboi-command-menu
          .open=${this.showCommandMenu}
          .prefixLabel=${this.keyRouter.prefixLabel}
          .layoutMode=${this.layoutMode}
          .followPTY=${this.followPTY}
          @close=${() => this.closeCommandMenu()}
          @command=${(e: CustomEvent<string>) => this.handleCommandMenuAction(e.detail)}
        ></wideboi-command-menu>
      ` : ''}
      ${this.showCommandPalette ? html`
        <wideboi-command-palette
          .open=${this.showCommandPalette}
          .initialQuery=${this.commandPaletteInitialQuery}
          .prefixLabel=${this.keyRouter.prefixLabel}
          .layoutMode=${this.layoutMode}
          .followPTY=${this.followPTY}
          @close=${() => this.closeCommandPalette()}
          @command=${(e: CustomEvent<string>) => this.handleCommandMenuAction(e.detail)}
          @execute-prompt=${(e: CustomEvent<string>) => this.handlePromptCommand(e.detail)}
        ></wideboi-command-palette>
      ` : ''}
      <wideboi-context-menu
        .open=${this.contextMenu.open}
        .x=${this.contextMenu.x}
        .y=${this.contextMenu.y}
        .hasSelection=${this.contextMenu.hasSelection}
        .paneId=${this.contextMenu.paneId}
        @action=${this.handleContextMenuAction}
        @close=${this.closeContextMenu}
      ></wideboi-context-menu>
      ${!this.connected ? html`
        <div class="overlay">
          <div class="connection-box">
            <h2>${desktopSession ? 'Session disconnected' : 'Connect to wideboi'}</h2>
            ${!desktopSession ? html`<input 
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
            />` : ''}
            <button @click=${this.connectClient}>${desktopSession ? 'Reconnect' : 'Connect'}</button>
            ${this.errorMsg ? html`<div class="error">${this.errorMsg}</div>` : ''}
          </div>
        </div>
      ` : ''}
    `;
  }
}
