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
    this.ws = new WebSocket(this.url);

    this.ws.onopen = () => {
      console.log("[WideboiClient] Connected to", this.url);
      if (this.onConnect) this.onConnect();
    };

    this.ws.onclose = () => {
      console.log("[WideboiClient] Disconnected");
      this.ws = null;
      if (this.onDisconnect) this.onDisconnect();
    };

    this.ws.onerror = (err) => {
      console.error("[WideboiClient] WebSocket error:", err);
    };

    this.ws.onmessage = (event: MessageEvent) => {
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
