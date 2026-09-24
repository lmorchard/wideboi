import { LitElement, html, css } from 'lit';
import { customElement, query, state } from 'lit/decorators.js';
import { WideboiClient } from './client';
import { GridRenderer } from './renderer';
import type { WSEnvelope } from './protocol';

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
  private wsUrl = `ws://${window.location.hostname}:8080/ws`;

  @state()
  private errorMsg = '';

  private inPrefixMode = false;
  private previousFocusId = 0;
  private pendingFocusId = 0;
  private paneStatuses: Record<number, number> = {};

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
            this.client.send('MsgResize', { Cols: size.cols, Rows: size.rows });
        }
      }
    });
  }

  firstUpdated() {
    this.renderer = new GridRenderer(this.canvas);
    this.resizeObserver.observe(this.canvas);
    
    const rect = this.canvas.getBoundingClientRect();
    this.renderer.resize(rect.width, rect.height);
    
    this.renderer.start();
    this.setupKeyboard();
    this.setupMouse();
  }

  disconnectedCallback() {
    super.disconnectedCallback();
    this.resizeObserver.disconnect();
    if (this.renderer) {
      this.renderer.stop();
    }
    if (this.client) {
      this.client.disconnect();
    }
  }

  private connectClient() {
    if (this.client) {
      this.client.disconnect();
    }
    
    this.errorMsg = '';
    this.client = new WideboiClient(this.wsUrl);
    
    this.client.onConnect = () => {
      console.log('Connected to server');
      this.connected = true;
      this.errorMsg = '';
      this.sendAttach();
    };

    this.client.onDisconnect = () => {
      this.connected = false;
      this.errorMsg = 'Disconnected from server.';
    }
    this.client.onMessage = (env: WSEnvelope) => {
      if (!this.renderer) return;

      if (env.t === 'MsgLayoutSnapshot') {
        this.renderer.handleLayoutSnapshot(env.p);
        this.activePanes = env.p.Columns?.map((c: any) => c.PaneID) || [];
        this.focusedPaneId = this.renderer.getFocusedPaneId();
        if (this.pendingFocusId && this.activePanes.includes(this.pendingFocusId)) {
          this.focusPane(this.pendingFocusId);
          this.pendingFocusId = 0;
        }
        if (!this.activePanes.includes(this.previousFocusId)) this.previousFocusId = 0;
        this.paneStatuses = env.p.PaneStatuses || {};
        this.paneTitles = env.p.PaneTitles || {};
      } else if (env.t === 'MsgPaneCreated') {
        this.pendingFocusId = env.p.PaneID;
      } else if (env.t === 'MsgPaneUpdate') {
        this.renderer.handlePaneUpdate(env.p);
      }
    };

    this.client.connect();
  }

  private setupKeyboard() {
    document.addEventListener('keydown', (e) => {
      if (!this.connected || !this.renderer || !this.client) return;
      if (e.target instanceof HTMLInputElement) return; 

      // Intercept the default prefix (ctrl+b) locally to drive verbs.
      // 1 = VerbFocusLeft, 2 = VerbFocusRight, 3 = VerbNewColumn, 5 = VerbKillPane
      if (e.ctrlKey && e.key === 'b') {
        this.inPrefixMode = true;
        e.preventDefault();
        return;
      }
      if (this.inPrefixMode) {
        let verb = 0;
        // Handle normal key presses and also handle if Ctrl is held down while pressing the key
        const key = e.key.toLowerCase();
        
        // Escape or Ctrl+C immediately drops out of prefix mode
        if (key === 'escape' || (e.ctrlKey && key === 'c')) {
           this.inPrefixMode = false;
           e.preventDefault();
           return;
        }

        switch (key) {
          case 'h': case 'arrowleft': verb = 1; break;  // FocusLeft
          case 'l': case 'arrowright': verb = 2; break; // FocusRight
          case 'n': verb = 3; break;  // NewColumn
          case 'w': verb = 4; break;  // CycleWidth
          case 'x': verb = 5; break;  // KillPane
          case 'a': verb = 6; break;  // SmartJump
          case 'p': verb = 8; break;  // GrowWidth
          case 'o': verb = 9; break;  // ShrinkWidth
          case 'y': verb = 10; break; // MoveLeft
          case 'u': verb = 11; break; // MoveRight
          case 'tab': verb = 12; break; // FocusLast
        }
        
        if (verb > 0) {
          const index = this.activePanes.indexOf(this.focusedPaneId);
          if (verb === 1 && index > 0) this.focusPane(this.activePanes[index - 1]);
          else if (verb === 2 && index >= 0 && index < this.activePanes.length - 1) this.focusPane(this.activePanes[index + 1]);
          else if (verb === 12) this.focusPane(this.previousFocusId);
          else if (verb === 6) {
            const rank = (status: number) => status === 4 ? 3 : status === 3 ? 2 : status === 2 ? 1 : 0;
            const target = this.activePanes.reduce((best, id) => {
              const score = rank(this.paneStatuses[id]);
              return score > rank(this.paneStatuses[best]) ||
                (score > 0 && score === rank(this.paneStatuses[best]) && id < best) ? id : best;
            }, 0);
            if (target) this.focusPane(target);
          } else if (![1, 2, 6, 12].includes(verb)) {
            this.client.send('MsgVerb', { Verb: verb, PaneID: this.focusedPaneId });
          }
          
          // If they held Ctrl while pressing the key (e.g. Ctrl-b, then held Ctrl and pressed 'l'),
          // stay in prefix mode so they can repeat it.
          // Note: KillPane ('x') does not repeat in the CLI.
          const isRepeatable = (verb === 1 || verb === 2 || verb === 8 || verb === 9 || verb === 10 || verb === 11);
          if (!(e.ctrlKey && isRepeatable)) {
             this.inPrefixMode = false;
          }
          
        } else if (key === 'j') {
          this.client.send('MsgScroll', { PaneID: this.renderer.getFocusedPaneId(), Delta: -10 });
        } else if (key === 'k') {
          this.client.send('MsgScroll', { PaneID: this.renderer.getFocusedPaneId(), Delta: 10 });
        } else {
           // Unknown key breaks out of prefix mode
           this.inPrefixMode = false;
        }
        e.preventDefault();
        return;
      }

      
      const keyData = {
        Text: e.key.length === 1 ? e.key : "",
        Mod: (e.shiftKey ? 1 : 0) | (e.altKey ? 2 : 0) | (e.ctrlKey ? 4 : 0),
        Code: e.key.length === 1 ? e.key.charCodeAt(0) : 0,
        ShiftedCode: 0,
        BaseCode: 0,
        IsRepeat: e.repeat
      };

      let data = "";
      if (e.key === "Enter") { keyData.Code = 13; keyData.Text = "\r"; }
      else if (e.key === "Backspace") { keyData.Code = 127; keyData.Text = "\x7f"; }
      else if (e.key === "Escape") { keyData.Code = 27; keyData.Text = "\x1b"; }
      else if (e.key === "Tab") { keyData.Code = 9; keyData.Text = "\t"; }
      else if (e.key === "ArrowUp") { data = "\x1b[A"; }
      else if (e.key === "ArrowDown") { data = "\x1b[B"; }
      else if (e.key === "ArrowRight") { data = "\x1b[C"; }
      else if (e.key === "ArrowLeft") { data = "\x1b[D"; }
      else if (e.key === "Home") { data = "\x1b[H"; }
      else if (e.key === "End") { data = "\x1b[F"; }
      else if (e.key === "PageUp") { data = "\x1b[5~"; }
      else if (e.key === "PageDown") { data = "\x1b[6~"; }
      else if (e.key === "Insert") { data = "\x1b[2~"; }
      else if (e.key === "Delete") { data = "\x1b[3~"; }

      const inputMsg = {
        PaneID: this.renderer.getFocusedPaneId(),
        Key: keyData,
        Data: data ? btoa(data) : "" // MsgInput Data is []byte so JSON might expect base64? Let's check!
      };
      
      this.client.send('MsgInput', inputMsg);
      e.preventDefault();
    });
  }

  private setupMouse() {
    if (!this.canvas) return;
    this.canvas.addEventListener('mousedown', (e) => {
      if (!this.connected || !this.renderer || !this.client) return;
      
      const { x, y } = this.renderer.pixelsToCells(e.clientX, e.clientY);
      const hit = this.renderer.getPaneHit(x, y);
      
      if (hit.paneID > 0 && hit.placement) {
        if (hit.paneID !== this.focusedPaneId) {
          this.focusPane(hit.paneID);
          return;
        }
        
        const localX = hit.placement.Src.Min.X + (x - hit.placement.Dst.Min.X);
        const localY = hit.placement.Src.Min.Y + (y - hit.placement.Dst.Min.Y);
        
        this.client.send('MsgMouse', {
            PaneID: hit.paneID,
            Kind: 0, 
            X: localX,
            Y: localY,
            Button: e.button === 0 ? 1 : e.button === 2 ? 3 : 2,
            Mod: (e.shiftKey ? 1 : 0) | (e.altKey ? 2 : 0) | (e.ctrlKey ? 4 : 0)
        });
      }
    });

    this.canvas.addEventListener('mouseup', (e) => {
      if (!this.connected || !this.renderer || !this.client) return;
      const { x, y } = this.renderer.pixelsToCells(e.clientX, e.clientY);
      const hit = this.renderer.getPaneHit(x, y);
      if (hit.paneID > 0 && hit.placement) {
        const localX = hit.placement.Src.Min.X + (x - hit.placement.Dst.Min.X);
        const localY = hit.placement.Src.Min.Y + (y - hit.placement.Dst.Min.Y);
        
        this.client.send('MsgMouse', {
            PaneID: hit.paneID,
            Kind: 1, 
            X: localX,
            Y: localY,
            Button: e.button === 0 ? 1 : e.button === 2 ? 3 : 2,
            Mod: (e.shiftKey ? 1 : 0) | (e.altKey ? 2 : 0) | (e.ctrlKey ? 4 : 0)
        });
      }
    });

    this.canvas.addEventListener('mousemove', (e) => {
      if (!this.connected || !this.renderer || !this.client) return;
      if (e.buttons === 0) return; // Only send drags
      
      const { x, y } = this.renderer.pixelsToCells(e.clientX, e.clientY);
      const hit = this.renderer.getPaneHit(x, y);
      if (hit.paneID > 0 && hit.placement) {
        const localX = hit.placement.Src.Min.X + (x - hit.placement.Dst.Min.X);
        const localY = hit.placement.Src.Min.Y + (y - hit.placement.Dst.Min.Y);
        
        this.client.send('MsgMouse', {
            PaneID: hit.paneID,
            Kind: 2, 
            X: localX,
            Y: localY,
            Button: e.button === 0 ? 1 : e.button === 2 ? 3 : 2,
            Mod: (e.shiftKey ? 1 : 0) | (e.altKey ? 2 : 0) | (e.ctrlKey ? 4 : 0)
        });
      }
    });
  }

  private sendAttach() {
     if (!this.renderer || !this.client) return;
     const size = this.renderer.getGridSize();
     this.client.send('MsgAttach', { Cols: size.cols, Rows: size.rows });
  }

  private handleUrlChange(e: Event) {
    this.wsUrl = (e.target as HTMLInputElement).value;
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
              placeholder="ws://localhost:8080/ws"
            />
            <button @click=${this.connectClient}>Connect</button>
            ${this.errorMsg ? html`<div class="error">${this.errorMsg}</div>` : ''}
          </div>
        </div>
      ` : ''}
    `;
  }
}
