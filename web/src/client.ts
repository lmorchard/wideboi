import type { WSEnvelope } from "./protocol";

export class WideboiClient {
  private ws: WebSocket | null = null;
  private url: string;
  private token: string;
  
  public onMessage?: (envelope: WSEnvelope) => void;
  public onConnect?: () => void;
  public onDisconnect?: () => void;

  constructor(url: string, token = "") {
    this.url = url;
    this.token = token;
  }

  public connect() {
    this.disconnect();
    // The browser can include a failed WebSocket URL in its own console
    // message. Carry the credential in a subprotocol instead of that URL.
    const bytes = new TextEncoder().encode(this.token);
    let binary = "";
    for (const byte of bytes) binary += String.fromCharCode(byte);
    const protocol = this.token ? "wideboi-token." + btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "") : undefined;
    const ws = protocol ? new WebSocket(this.url, [protocol]) : new WebSocket(this.url);
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
