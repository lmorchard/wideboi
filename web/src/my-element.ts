import { LitElement, html, css } from 'lit';
import { customElement, query } from 'lit/decorators.js';
import { create } from "@bufbuild/protobuf";
import { WideboiClient } from './client';
import { GridRenderer } from './renderer';
import type { ServerEnvelope } from './gen/wideboi_pb';
import { ClientEnvelopeSchema, MsgAttachSchema, MsgResizeSchema } from './gen/wideboi_pb';

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
    
    // Default fallback, should probably be configurable
    const wsUrl = `ws://${window.location.hostname}:8080/ws`;
    this.client = new WideboiClient(wsUrl);
    
    this.client.onConnect = () => {
      console.log('Connected to server');
      this.sendAttach();
    };

    this.client.onMessage = (env: ServerEnvelope) => {
      if (!this.renderer) return;

      if (env.payload.case === 'layoutSnapshot') {
        this.renderer.handleLayoutSnapshot(env.payload.value);
      } else if (env.payload.case === 'paneUpdate') {
        this.renderer.handlePaneUpdate(env.payload.value);
      }
    };

    this.resizeObserver = new ResizeObserver((entries) => {
      if (!this.renderer) return;
      for (const entry of entries) {
        const { width, height } = entry.contentRect;
        this.renderer.resize(width, height);
        
        // Let the server know our new dimensions in cells
        if (this.client) {
            const size = this.renderer.getGridSize();
            const resizeMsg = create(ClientEnvelopeSchema, {
                payload: {
                  case: 'resize',
                  value: create(MsgResizeSchema, { cols: size.cols, rows: size.rows })
                }
            });
            this.client.send(resizeMsg);
        }
      }
    });
  }

  private sendAttach() {
     if (!this.renderer) return;
     const size = this.renderer.getGridSize();
     const attachMsg = create(ClientEnvelopeSchema, {
        payload: {
          case: 'attach',
          value: create(MsgAttachSchema, { cols: size.cols, rows: size.rows })
        }
      });
      this.client.send(attachMsg);
  }

  firstUpdated() {
    this.renderer = new GridRenderer(this.canvas);
    this.resizeObserver.observe(this.canvas);
    
    // Initial size
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
