import { create, fromBinary, toBinary, type MessageInitShape } from "@bufbuild/protobuf";
import { ClientMessageSchema, ServerMessageSchema, type ServerMessage } from "./gen/internal/protocol/wirepb/wideboi_pb";
import type { RenderStats } from "./stats";

// ClientMsg is one arm of the ClientMessage oneof, e.g.
// { case: "resize", value: { cols, rows } }.
export type ClientMsg = NonNullable<MessageInitShape<typeof ClientMessageSchema>["msg"]>;

export class WideboiClient {
  private ws: WebSocket | null = null;
  private url: string;
  private token: string;
  private stats?: RenderStats;
  
  public onMessage?: (message: ServerMessage) => void;
  public onConnect?: () => void;
  public onDisconnect?: () => void;

  // stats, when given, records the byte size of each message (never its
  // contents). It is undefined unless the page was opened with ?stats=1.
  constructor(url: string, token = "", stats?: RenderStats) {
    this.url = url;
    this.token = token;
    this.stats = stats;
  }

  public connect() {
    this.disconnect();
    // The browser can include a failed WebSocket URL in its own console
    // message. Carry the credential in a subprotocol instead of that URL.
    const bytes = new TextEncoder().encode(this.token);
    let binary = "";
    for (const byte of bytes) binary += String.fromCharCode(byte);
    const protocol = this.token ? "wideboi-token." + btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "") : undefined;
    // The server must select this version before any shift patches arrive.
    // Browsers reject an upgrade that selects no offered subprotocol.
    const versionProtocol = "wideboi.v8";
    const ws = new WebSocket(this.url, protocol ? [versionProtocol, protocol] : [versionProtocol]);
    ws.binaryType = "arraybuffer";
    this.ws = ws;

    ws.onopen = () => {
      if (this.ws !== ws) return;
      if (ws.protocol !== versionProtocol) {
        ws.close();
        this.ws = null;
        if (this.onDisconnect) this.onDisconnect();
        return;
      }
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
      const stats = this.stats;
      stats?.recordMessage((event.data as ArrayBuffer).byteLength);
      let message: ServerMessage;
      try {
        // Decode is timed apart from the renderer's apply: for a full
        // snapshot it is most of the browser's cost.
        const start = stats ? performance.now() : 0;
        message = fromBinary(ServerMessageSchema, new Uint8Array(event.data as ArrayBuffer));
        if (stats) stats.recordDecode(performance.now() - start);
      } catch (err) {
        console.error("[WideboiClient] Failed to decode message:", err);
        return;
      }
      if (this.onMessage) this.onMessage(message);
    };
  }

  public send(msg: ClientMsg) {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      console.warn("[WideboiClient] Cannot send, not connected");
      return;
    }
    
    this.ws.send(toBinary(ClientMessageSchema, create(ClientMessageSchema, { msg })));
  }

  public disconnect() {
    if (this.ws) {
      this.ws.close();
      this.ws = null;
    }
  }
}
