import { LitElement, html, css } from 'lit';
import { customElement, query, state } from 'lit/decorators.js';
import { WideboiClient } from './client';
import { GridRenderer } from './renderer';
import { sendKeyboardInput, sendTextInput } from './input';
import { consumeLinkToken } from './token';
import { MouseKind, PaneStatus, VerbType } from './gen/internal/protocol/wirepb/wideboi_pb';

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
    canvas {
      display: block;
      width: 100%;
      flex: 1;
      min-height: 0;
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

  @query('canvas')
  private canvas!: HTMLCanvasElement;

  private client: WideboiClient | null = null;
  private renderer?: GridRenderer;
  private resizeObserver: ResizeObserver;

  @state()
  private connected = false;

  @state()
  private activePanes: number[] = [];

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
  private paneStatuses: Record<number, PaneStatus> = {};
  private listeners?: AbortController;
  private pointer?: { id: number; paneID: number; placement: import('./protocol').PlacementData; button: number; tracking: boolean; focusOnClick: boolean; dragged: boolean; startX: number; startY: number };

  private focusPane(paneID: number) {
    if (!this.renderer || !this.activePanes.includes(paneID)) return;
    if (paneID !== this.focusedPaneId) this.previousFocusId = this.focusedPaneId;
    this.focusedPaneId = paneID;
    this.renderer.setFocusedPaneId(paneID);
  }

  constructor() {
    super();

    this.resizeObserver = new ResizeObserver((entries) => {
      if (!this.renderer) return;
      for (const entry of entries) {
        const { width, height } = entry.contentRect;
        this.renderer.resize(width, height);
        
        if (this.client && this.connected) {
            const size = this.renderer.getGridSize();
            this.client.send({ case: 'resize', value: { cols: size.cols, rows: size.rows } });
        }
      }
    });
  }

  firstUpdated() {
    this.listeners = new AbortController();
    this.renderer = new GridRenderer(this.canvas);
    this.resizeObserver.observe(this.canvas);
    
    const rect = this.canvas.getBoundingClientRect();
    this.renderer.resize(rect.width, rect.height);
    
    this.setupKeyboard();
    this.setupMouse();
  }

  connectedCallback() {
    super.connectedCallback();
    if (this.renderer && !this.listeners) {
      this.listeners = new AbortController();
      this.resizeObserver.observe(this.canvas);
      if (this.connected) this.renderer.start();
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
    this.resizeObserver.disconnect();
    if (this.renderer) {
      this.renderer.stop();
    }
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
    this.inPrefixMode = false;
    this.pendingFocusId = 0;
    
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
      this.renderer?.start();
      this.errorMsg = '';
      this.sendAttach();
    };

    client.onDisconnect = () => {
      if (this.client !== client) return;
      this.connected = false;
      this.inPrefixMode = false;
      this.renderer?.stop();
      this.errorMsg = 'Disconnected from server.';
    }
    client.onMessage = (message) => {
      if (this.client !== client || !this.renderer) return;

      switch (message.msg.case) {
        case 'layoutSnapshot': {
          const snapshot = message.msg.value;
          this.renderer.handleLayoutSnapshot(snapshot);
          this.activePanes = snapshot.columns.map(c => c.paneId);
          this.focusedPaneId = this.renderer.getFocusedPaneId();
          if (this.pendingFocusId && this.activePanes.includes(this.pendingFocusId)) {
            this.focusPane(this.pendingFocusId);
            this.pendingFocusId = 0;
          }
          if (!this.activePanes.includes(this.previousFocusId)) this.previousFocusId = 0;
          this.paneStatuses = snapshot.paneStatuses;
          this.paneTitles = snapshot.paneTitles;
          break;
        }
        case 'paneCreated':
          this.pendingFocusId = message.msg.value.paneId;
          break;
        case 'paneUpdate':
          this.renderer.handlePaneUpdate(message.msg.value);
          break;
        case 'panePatch':
          if (!this.renderer.handlePanePatch(message.msg.value)) {
            client.send({ case: 'paneResync', value: { paneId: message.msg.value.paneId } });
          }
          break;
        case 'paneClosed': {
          const closedId = message.msg.value.paneId;
          this.renderer.handlePaneClosed(closedId);
          this.activePanes = this.activePanes.filter(id => id !== closedId);
          break;
        }
      }
    };

    client.connect();
  }

  private setupKeyboard() {
    document.addEventListener('keydown', (e) => {
      if (!this.connected || !this.renderer || !this.client) return;
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
          this.client.send({ case: 'scroll', value: { paneId: this.renderer.getFocusedPaneId(), delta: -10 } });
        } else if (key === 'k') {
          this.client.send({ case: 'scroll', value: { paneId: this.renderer.getFocusedPaneId(), delta: 10 } });
        } else {
           // Unknown key breaks out of prefix mode
           this.inPrefixMode = false;
        }
        e.preventDefault();
        return;
      }

      
      if (sendKeyboardInput(this.client, this.renderer.getFocusedPaneId(), e)) e.preventDefault();
    }, { signal: this.listeners?.signal });

    document.addEventListener('paste', (e) => {
      if (!this.connected || !this.renderer || !this.client || e.target instanceof HTMLInputElement) return;
      const value = e.clipboardData?.getData('text/plain') || '';
      if (!sendTextInput(this.client, this.renderer.getFocusedPaneId(), value)) return;
      e.preventDefault();
    }, { signal: this.listeners?.signal });

    document.addEventListener('compositionend', (e) => {
      if (!this.connected || !this.renderer || !this.client || e.target instanceof HTMLInputElement) return;
      sendTextInput(this.client, this.renderer.getFocusedPaneId(), (e as CompositionEvent).data);
    }, { signal: this.listeners?.signal });
  }

  private setupMouse() {
    if (!this.canvas) return;
    this.canvas.addEventListener('pointerdown', (e) => {
      if (!this.connected || !this.renderer || !this.client) return;
      const { x, y } = this.renderer.pixelsToCells(e.clientX, e.clientY);
      const hit = this.renderer.getPaneHit(x, y);
      if (!hit.paneID || !hit.placement) return;
      this.pointer = {
        id: e.pointerId, paneID: hit.paneID, placement: hit.placement,
        button: e.button === 0 ? 1 : e.button === 2 ? 3 : 2,
        tracking: hit.paneID === this.focusedPaneId && this.renderer.mouseTracking(hit.paneID),
        focusOnClick: hit.paneID !== this.focusedPaneId, dragged: false,
        startX: x, startY: y
      };
      this.canvas.setPointerCapture(e.pointerId);
      this.renderer.clearSelection();
      if (this.pointer.tracking) this.sendPointerMouse(MouseKind.PRESS, e);
      e.preventDefault();
    }, { signal: this.listeners?.signal });

    this.canvas.addEventListener('pointermove', (e) => {
      const press = this.pointer;
      if (!press || press.id !== e.pointerId || !this.renderer) return;
      const { x, y } = this.renderer.pixelsToCells(e.clientX, e.clientY);
      if (x !== press.startX || y !== press.startY) press.dragged = true;
      if (press.tracking) this.sendPointerMouse(MouseKind.MOTION, e);
      else if (press.button === 1) {
        const start = this.pointerCell(press.startX, press.startY, press.placement);
        const end = this.pointerCell(x, y, press.placement);
        this.renderer.setSelection(press.paneID, start, end);
      }
      e.preventDefault();
    }, { signal: this.listeners?.signal });

    const release = (e: PointerEvent) => {
      if (!this.pointer || this.pointer.id !== e.pointerId) return;
      if (this.pointer.tracking) this.sendPointerMouse(MouseKind.RELEASE, e);
      else if (this.renderer) {
        if (e.type === 'pointerup' && this.pointer.focusOnClick && !this.pointer.dragged) {
          this.renderer.clearSelection();
          this.focusPane(this.pointer.paneID);
        } else {
          const text = this.renderer.selectionText();
          if (text && navigator.clipboard?.writeText) void navigator.clipboard.writeText(text).catch(() => {});
        }
      }
      this.pointer = undefined;
      if (this.canvas.hasPointerCapture(e.pointerId)) this.canvas.releasePointerCapture(e.pointerId);
      e.preventDefault();
    };
    this.canvas.addEventListener('pointerup', release, { signal: this.listeners?.signal });
    this.canvas.addEventListener('pointercancel', release, { signal: this.listeners?.signal });

    this.canvas.addEventListener('wheel', (e) => {
      e.preventDefault();
      if (!this.connected || !this.renderer || !this.client) return;
      
      const { x, y } = this.renderer.pixelsToCells(e.clientX, e.clientY);
      const hit = this.renderer.getPaneHit(x, y);
      
      if (hit.paneID > 0) {
        // e.deltaY > 0 means scrolling down (towards bottom/newer).
        // e.deltaY < 0 means scrolling up (towards top/older).
        // In MsgScroll, Delta > 0 is up (older), Delta < 0 is down (newer).
        // A standard wheel step is often 3 lines.
        const delta = e.deltaY > 0 ? -3 : 3;
        this.client.send({ case: 'scroll', value: { paneId: hit.paneID, delta } });
      }
    }, { passive: false, signal: this.listeners?.signal });
  }

  private sendPointerMouse(kind: MouseKind, e: PointerEvent) {
    const press = this.pointer;
    if (!press || !this.client || !this.renderer || !this.connected) return;
    const { x, y } = this.renderer.pixelsToCells(e.clientX, e.clientY);
    const p = press.placement;
    const { x: localX, y: localY } = this.pointerCell(x, y, p);
    this.client.send({ case: 'mouse', value: {
      paneId: press.paneID, kind, x: localX, y: localY,
      button: press.button,
      mod: (e.shiftKey ? 1 : 0) | (e.altKey ? 2 : 0) | (e.ctrlKey ? 4 : 0)
    } });
  }

  private pointerCell(x: number, y: number, p: import('./protocol').PlacementData) {
    return {
      x: Math.max(p.Src.Min.X, Math.min(p.Src.Max.X - 1, p.Src.Min.X + x - p.Dst.Min.X)),
      y: Math.max(p.Src.Min.Y, Math.min(p.Src.Max.Y - 1, p.Src.Min.Y + y - p.Dst.Min.Y))
    };
  }

  private sendAttach() {
     if (!this.renderer || !this.client) return;
     const size = this.renderer.getGridSize();
     this.client.send({ case: 'attach', value: { cols: size.cols, rows: size.rows } });
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
    this.canvas.focus();
  }

  render() {
    return html`
      ${this.connected ? html`
        <div class="toolbar">
          <label>Focus Pane:</label>
          <select .value=${this.focusedPaneId.toString()} @change=${this.handlePaneSelect}>
            ${this.activePanes.map(id => html`<option value=${id}>[${id}] ${this.paneTitles[id] || 'Terminal'}</option>`)}
          </select>
          <span style="color: #666; margin-left: auto;">(Tip: Ctrl+B then left/right arrow to switch)</span>
        </div>
      ` : ''}
      <canvas tabindex="0"></canvas>
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
