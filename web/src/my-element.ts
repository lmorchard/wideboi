import { LitElement, html, css } from 'lit';
import { customElement, query } from 'lit/decorators.js';
import { WideboiClient } from './client';
import { GridRenderer } from './renderer';
import type { WSEnvelope } from './protocol';

@customElement('wideboi-app')
export class WideboiApp extends LitElement {
  static styles = css`
    :host {
      display: block;
      width: 100vw;
      height: 100vh;
      overflow: hidden;
      background: #1e1e1e;
    }
    canvas {
      display: block;
      width: 100%;
      height: 100%;
    }
  `;

  @query('canvas')
  private canvas!: HTMLCanvasElement;

  private client: WideboiClient;
  private renderer?: GridRenderer;
  private resizeObserver: ResizeObserver;

  constructor() {
    super();
    
    const wsUrl = `ws://${window.location.hostname}:8080/ws`;
    this.client = new WideboiClient(wsUrl);
    
    this.client.onConnect = () => {
      console.log('Connected to server');
      this.sendAttach();
    };

    this.client.onMessage = (env: WSEnvelope) => {
      if (!this.renderer) return;

      if (env.t === 'MsgLayoutSnapshot') {
        this.renderer.handleLayoutSnapshot(env.p);
      } else if (env.t === 'MsgPaneUpdate') {
        this.renderer.handlePaneUpdate(env.p);
      }
    };

    this.resizeObserver = new ResizeObserver((entries) => {
      if (!this.renderer) return;
      for (const entry of entries) {
        const { width, height } = entry.contentRect;
        this.renderer.resize(width, height);
        
        if (this.client) {
            const size = this.renderer.getGridSize();
            this.client.send('MsgResize', { Cols: size.cols, Rows: size.rows });
        }
      }
    });
  }

  private sendAttach() {
     if (!this.renderer) return;
     const size = this.renderer.getGridSize();
     this.client.send('MsgAttach', { Cols: size.cols, Rows: size.rows });
  }

  firstUpdated() {
    this.renderer = new GridRenderer(this.canvas);
    this.resizeObserver.observe(this.canvas);
    
    const rect = this.canvas.getBoundingClientRect();
    this.renderer.resize(rect.width, rect.height);
    
    this.renderer.start();
    this.client.connect();
  }

  disconnectedCallback() {
    super.disconnectedCallback();
    this.resizeObserver.disconnect();
    if (this.renderer) {
      this.renderer.stop();
    }
    this.client.disconnect();
  }

  render() {
    return html`<canvas></canvas>`;
  }
}
