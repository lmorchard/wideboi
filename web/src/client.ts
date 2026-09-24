import type { WSEnvelope } from "./protocol";

export class WideboiClient {
  private ws: WebSocket | null = null;
  private url: string;
  
  public onMessage?: (envelope: WSEnvelope) => void;
  public onConnect?: () => void;
  public onDisconnect?: () => void;

  constructor(url: string) {
    this.url = url;
  }

  public connect() {
    this.disconnect();
    const ws = new WebSocket(this.url);
    this.ws = ws;

    ws.onopen = () => {
      if (this.ws !== ws) return;
      console.log("[WideboiClient] Connected");
      if (this.onConnect) this.onConnect();
    };

    ws.onclose = () => {
      if (this.ws !== ws) return;
      console.log("[WideboiClient] Disconnected");
      this.ws = null;
      if (this.onDisconnect) this.onDisconnect();
    };

    ws.onerror = () => {
      if (this.ws !== ws) return;
      console.error("[WideboiClient] WebSocket error");
    };

    ws.onmessage = (event: MessageEvent) => {
      if (this.ws !== ws) return;
      try {
        const envelope = JSON.parse(event.data) as WSEnvelope;
        if (this.onMessage) {
          this.onMessage(envelope);
        }
      } catch (err) {
        console.error("[WideboiClient] Failed to decode JSON message:", err);
      }
    };
  }

  public send(type: string, payload: any) {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      console.warn("[WideboiClient] Cannot send, not connected");
      return;
    }
    
    const env: WSEnvelope = { t: type, p: payload };
    this.ws.send(JSON.stringify(env));
  }

  public disconnect() {
    if (this.ws) {
      this.ws.close();
      this.ws = null;
    }
  }
}
