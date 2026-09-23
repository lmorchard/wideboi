import { LitElement, html, css } from 'lit';
import { customElement, state } from 'lit/decorators.js';
import { create } from "@bufbuild/protobuf";
import { WideboiClient } from './client';
import type { ServerEnvelope } from './gen/wideboi_pb';
import { ClientEnvelopeSchema, MsgAttachSchema } from './gen/wideboi_pb';

@customElement('wideboi-app')
export class WideboiApp extends LitElement {
  static styles = css`
    :host {
      display: block;
      font-family: monospace;
      padding: 1rem;
      background: #1e1e1e;
      color: #d4d4d4;
      min-height: 100vh;
    }
    .status {
      margin-bottom: 1rem;
      padding: 0.5rem;
      background: #2d2d2d;
      border-radius: 4px;
    }
    .connected { color: #4ec9b0; }
    .disconnected { color: #f48771; }
    .log {
      white-space: pre-wrap;
      font-size: 12px;
      max-height: 80vh;
      overflow-y: auto;
    }
  `;

  @state()
  private connected = false;

  @state()
  private log: string[] = [];

  private client: WideboiClient;

  constructor() {
    super();
    // Assuming server runs on localhost:8080/ws
    this.client = new WideboiClient('ws://127.0.0.1:8080/ws');
    
    this.client.onConnect = () => {
      this.connected = true;
      this.addLog('Connected to server');
      
      // Send attach message using Protobuf classes
      const attachMsg = create(ClientEnvelopeSchema, {
        payload: {
          case: 'attach',
          value: create(MsgAttachSchema, { cols: 80, rows: 24 })
        }
      });
      this.client.send(attachMsg);
      this.addLog('Sent MsgAttach (80x24)');
    };

    this.client.onDisconnect = () => {
      this.connected = false;
      this.addLog('Disconnected from server');
    };

    this.client.onMessage = (env: ServerEnvelope) => {
      if (env.payload.case === 'layoutSnapshot') {
        this.addLog(`Received LayoutSnapshot: ${env.payload.value.columns.length} columns`);
      } else if (env.payload.case === 'paneUpdate') {
        const update = env.payload.value;
        this.addLog(`Received PaneUpdate for ID ${update.paneId}: ${update.cols}x${update.rows}`);
      } else if (env.payload.case === 'paneClosed') {
        this.addLog(`Received PaneClosed for ID ${env.payload.value.paneId}`);
      }
    };
  }

  firstUpdated() {
    this.client.connect();
  }

  disconnectedCallback() {
    super.disconnectedCallback();
    this.client.disconnect();
  }

  private addLog(msg: string) {
    this.log = [...this.log, `[${new Date().toLocaleTimeString()}] ${msg}`].slice(-50);
  }

  render() {
    return html`
      <div class="status ${this.connected ? 'connected' : 'disconnected'}">
        Status: ${this.connected ? 'Connected' : 'Disconnected'}
      </div>
      <div class="log">
        ${this.log.join('\n')}
      </div>
    `;
  }
}
