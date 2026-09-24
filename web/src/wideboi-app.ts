import { LitElement, html, css } from 'lit';
import { customElement, query, state } from 'lit/decorators.js';
import { repeat } from 'lit/directives/repeat.js';
import { WideboiClient } from './client';
import { CELL_HEIGHT, measureCellWidth, PaneStore, selectionText, type CellPoint } from './pane-state';
import { reconcileFocus } from './focus';
import { WideboiPane } from './wideboi-pane';
import { sendKeyboardInput, sendTextInput } from './input';
import { consumeLinkToken } from './token';
import { MouseKind, PaneStatus, VerbType, type ColumnData } from './gen/internal/protocol/wirepb/wideboi_pb';

const linkToken = consumeLinkToken(window.location, window.history);

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
    }
    .title, .status {
      height: 16.8px;
      line-height: 16.8px;
      flex: none;
      overflow: hidden;
      white-space: nowrap;
      text-overflow: ellipsis;
    }
    .pane-strip {
      display: flex;
      flex: 1;
      min-height: 0;
      min-width: 0;
      overflow-x: auto;
      overflow-y: hidden;
      scrollbar-width: thin;
      overscroll-behavior-x: contain;
      background: #1e1e1e;
    }

    .toolbar {
      background: #252526;
      border-bottom: 1px solid #3c3c3c;
      padding: 0.5rem 1rem;
      display: flex;
      gap: 1rem;
      align-items: center;
      z-index: 5;
      font-size: 13px;
    }
    .toolbar select {
      background: #3c3c3c;
      color: #cccccc;
      border: 1px solid #555;
      padding: 0.3rem;
      border-radius: 3px;
      outline: none;
    }
    .toolbar label {
      color: #aaa;
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
    .error {
      color: #f14c4c;
      font-size: 0.9rem;
      margin-top: -0.5rem;
    }
  `;

  @query('.pane-strip')
  private paneStrip!: HTMLElement;

  private client: WideboiClient | null = null;
  private panes = new PaneStore();
  private resizeObserver: ResizeObserver;
  private cellWidth = 1;

  @state()
  private connected = false;

  @state()
  private activePanes: number[] = [];

  @state()
  private columns: ColumnData[] = [];

  @state()
  private paneTitles: Record<number, string> = {};

  @state()
  private focusedPaneId = 0;

  @state()
  private wsUrl = `${window.location.protocol === 'https:' ? 'wss:' : 'ws:'}//${window.location.host}/ws`;

  @state()
  private token = linkToken;

  @state()
  private errorMsg = '';

  private inPrefixMode = false;
  private previousFocusId = 0;
  private pendingFocusId = 0;
  @state()
  private paneStatuses: Record<number, PaneStatus> = {};
  private listeners?: AbortController;
  private pointer?: { id: number; pane: WideboiPane; button: number; tracking: boolean; focusOnClick: boolean; dragged: boolean; start: CellPoint };
  private selectedPane?: WideboiPane;
  private movement = new Map<number, Animation>();
  private lastSentSize?: { cols: number; rows: number };

  private focusPane(paneID: number) {
    if (!this.activePanes.includes(paneID)) return;
    if (paneID !== this.focusedPaneId) this.previousFocusId = this.focusedPaneId;
    this.focusedPaneId = paneID;
    void this.updateComplete.then(() => {
      this.focusedPane()?.focusInput();
      this.revealFocus();
    });
  }

  private focusedPane(): WideboiPane | undefined {
    return Array.from(this.paneStrip?.querySelectorAll('wideboi-pane') || [])
      .find(element => element.paneId === this.focusedPaneId);
  }

  private revealFocus() {
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

    this.resizeObserver = new ResizeObserver(() => this.sendResizeIfChanged());
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
    this.inPrefixMode = false;
    for (const animation of this.movement.values()) animation.cancel();
    this.movement.clear();
    this.resizeObserver.disconnect();
    if (this.client) {
      this.client.disconnect();
      this.client = null;
    }
    this.connected = false;
  }

  private connectClient() {
    if (this.client) {
      this.client.disconnect();
    }
    this.connected = false;
    this.lastSentSize = undefined;
    this.inPrefixMode = false;
    this.pendingFocusId = 0;
    this.panes = new PaneStore();
    this.selectedPane = undefined;
    this.columns = [];
    this.activePanes = [];
    this.focusedPaneId = 0;
    this.previousFocusId = 0;
    
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
    const client = new WideboiClient(url.toString(), token);
    this.client = client;
    
    client.onConnect = () => {
      if (this.client !== client) return;
      console.log('Connected to server');
      this.connected = true;
      this.errorMsg = '';
      void this.updateComplete.then(() => {
        if (this.client === client && this.connected) this.sendAttach();
      });
    };

    client.onDisconnect = () => {
      if (this.client !== client) return;
      this.connected = false;
      this.inPrefixMode = false;
      this.errorMsg = 'Disconnected from server.';
    }
    client.onMessage = (message) => {
      if (this.client !== client) return;

      switch (message.msg.case) {
        case 'layoutSnapshot': {
          const snapshot = message.msg.value;
          const previous = this.panePositions();
          this.focusedPaneId = reconcileFocus(this.columns, snapshot.columns, this.focusedPaneId);
          this.columns = snapshot.columns;
          this.activePanes = snapshot.columns.map(c => c.paneId);
          if (this.pendingFocusId && this.activePanes.includes(this.pendingFocusId)) {
            this.focusPane(this.pendingFocusId);
            this.pendingFocusId = 0;
          }
          if (!this.activePanes.includes(this.previousFocusId)) this.previousFocusId = 0;
          this.paneStatuses = snapshot.paneStatuses;
          this.paneTitles = snapshot.paneTitles;
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
          break;
        case 'panePatch':
          if (!this.panes.patch(message.msg.value)) {
            client.send({ case: 'paneResync', value: { paneId: message.msg.value.paneId } });
          }
          this.requestUpdate();
          break;
        case 'paneClosed': {
          const closedId = message.msg.value.paneId;
          this.panes.close(closedId);
          const previousColumns = this.columns;
          const nextColumns = previousColumns.filter(column => column.paneId !== closedId);
          const nextFocus = reconcileFocus(previousColumns, nextColumns, this.focusedPaneId);
          const focusChanged = nextFocus !== this.focusedPaneId;
          this.columns = nextColumns;
          this.activePanes = nextColumns.map(column => column.paneId);
          this.focusedPaneId = nextFocus;
          if (this.previousFocusId === closedId) this.previousFocusId = 0;
          if (this.pendingFocusId === closedId) this.pendingFocusId = 0;
          if (this.pointer?.pane.paneId === closedId) this.pointer = undefined;
          if (this.selectedPane?.paneId === closedId) this.selectedPane = undefined;
          void this.updateComplete.then(() => {
            if (focusChanged) {
              this.focusedPane()?.focusInput();
              this.revealFocus();
            }
            this.sendResizeIfChanged();
          });
          break;
        }
      }
    };

    client.connect();
  }

  private setupKeyboard() {
    document.addEventListener('keydown', (e) => {
      if (!this.connected || !this.client) return;
      if (e.target instanceof HTMLInputElement) return; 
      if (e.isComposing || e.key === 'Process' || e.key === 'Dead') return;

      // Intercept the default prefix (ctrl+b) locally to drive verbs.
      if (e.ctrlKey && e.key === 'b') {
        this.inPrefixMode = true;
        e.preventDefault();
        return;
      }
      if (this.inPrefixMode) {
        let verb = VerbType.UNSPECIFIED;
        // Handle normal key presses and also handle if Ctrl is held down while pressing the key
        const key = e.key.toLowerCase();
        
        // Escape or Ctrl+C immediately drops out of prefix mode
        if (key === 'escape' || (e.ctrlKey && key === 'c')) {
           this.inPrefixMode = false;
           e.preventDefault();
           return;
        }

        switch (key) {
          case 'h': case 'arrowleft': verb = VerbType.FOCUS_LEFT; break;
          case 'l': case 'arrowright': verb = VerbType.FOCUS_RIGHT; break;
          case 'n': verb = VerbType.NEW_COLUMN; break;
          case 'w': verb = VerbType.CYCLE_WIDTH; break;
          case 'x': verb = VerbType.KILL_PANE; break;
          case 'a': verb = VerbType.SMART_JUMP; break;
          case 'p': verb = VerbType.GROW_WIDTH; break;
          case 'o': verb = VerbType.SHRINK_WIDTH; break;
          case 'y': verb = VerbType.MOVE_LEFT; break;
          case 'u': verb = VerbType.MOVE_RIGHT; break;
          case 'tab': verb = VerbType.FOCUS_LAST; break;
        }
        
        if (verb !== VerbType.UNSPECIFIED) {
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
          } else if (![VerbType.FOCUS_LEFT, VerbType.FOCUS_RIGHT, VerbType.SMART_JUMP, VerbType.FOCUS_LAST].includes(verb)) {
            this.client.send({ case: 'verb', value: { verb, paneId: this.focusedPaneId } });
          }
          
          // If they held Ctrl while pressing the key (e.g. Ctrl-b, then held Ctrl and pressed 'l'),
          // stay in prefix mode so they can repeat it.
          // Note: KillPane ('x') does not repeat in the CLI.
          const isRepeatable = [VerbType.FOCUS_LEFT, VerbType.FOCUS_RIGHT, VerbType.GROW_WIDTH,
            VerbType.SHRINK_WIDTH, VerbType.MOVE_LEFT, VerbType.MOVE_RIGHT].includes(verb);
          if (!(e.ctrlKey && isRepeatable)) {
             this.inPrefixMode = false;
          }
          
        } else if (key === 'j') {
          this.client.send({ case: 'scroll', value: { paneId: this.focusedPaneId, delta: -10 } });
        } else if (key === 'k') {
          this.client.send({ case: 'scroll', value: { paneId: this.focusedPaneId, delta: 10 } });
        } else {
           // Unknown key breaks out of prefix mode
           this.inPrefixMode = false;
        }
        e.preventDefault();
        return;
      }

      
      if (sendKeyboardInput(this.client, this.focusedPaneId, e)) e.preventDefault();
    }, { signal: this.listeners?.signal });

    document.addEventListener('paste', (e) => {
      if (!this.connected || !this.client || e.target instanceof HTMLInputElement) return;
      const value = e.clipboardData?.getData('text/plain') || '';
      if (!sendTextInput(this.client, this.focusedPaneId, value)) return;
      e.preventDefault();
    }, { signal: this.listeners?.signal });

    document.addEventListener('compositionend', (e) => {
      if (!this.connected || !this.client || e.target instanceof HTMLInputElement) return;
      sendTextInput(this.client, this.focusedPaneId, (e as CompositionEvent).data);
    }, { signal: this.listeners?.signal });
  }

  private setupMouse() {
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
      // Leave horizontal wheel and trackpad gestures to the native strip.
      if (e.shiftKey || Math.abs(e.deltaX) > Math.abs(e.deltaY)) return;
      const pane = this.eventPane(e);
      if (pane) {
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

  render() {
    return html`
      ${this.connected ? html`
        <div class="toolbar">
          <label>Focus Pane:</label>
          <select @change=${this.handlePaneSelect}>
            ${repeat(this.activePanes, id => id, id => html`
              <option value=${id} .selected=${id === this.focusedPaneId}>[${id}] ${this.paneTitles[id] || 'Terminal'}</option>
            `)}
          </select>
          <span style="color: #666; margin-left: auto;">(Tip: Ctrl+B then left/right arrow to switch)</span>
        </div>
      ` : ''}
      <div class="terminal-shell">
        <div class="title">${this.paneTitles[this.focusedPaneId] ||
          (this.focusedPaneId ? `Pane ${this.focusedPaneId}` : '')}</div>
        <div class="pane-strip">
          ${repeat(this.columns, column => column.paneId, column => html`
            <wideboi-pane
              style=${`width: ${column.width * this.cellWidth}px; --divider-width: ${this.cellWidth}px`}
              .paneId=${column.paneId}
              .pane=${this.panes.get(column.paneId)}
              .focused=${column.paneId === this.focusedPaneId}
              .running=${this.connected}
              .cellWidth=${this.cellWidth}
              aria-label=${`Pane ${column.paneId}`}
            ></wideboi-pane>
          `)}
        </div>
        <div class="status">${this.columns.map(column =>
          `[${column.paneId}] ${PaneStatus[this.paneStatuses[column.paneId] ?? PaneStatus.IDLE] || ''}`
        ).join('  ')}</div>
      </div>
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
