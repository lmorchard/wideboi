import { fromBinary, toBinary } from "@bufbuild/protobuf";
import type { ClientEnvelope, ServerEnvelope } from "./gen/wideboi_pb";
import { ServerEnvelopeSchema, ClientEnvelopeSchema } from "./gen/wideboi_pb";

export class WideboiClient {
  private ws: WebSocket | null = null;
  private url: string;
  
  public onMessage?: (envelope: ServerEnvelope) => void;
  public onConnect?: () => void;
  public onDisconnect?: () => void;

  constructor(url: string) {
    this.url = url;
  }

  public connect() {
    this.ws = new WebSocket(this.url);
    this.ws.binaryType = "arraybuffer"; // Important: we want ArrayBuffer for protobuf

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
      if (!(event.data instanceof ArrayBuffer)) {
        console.warn("[WideboiClient] Expected binary frame, got text");
        return;
      }

      try {
        const bytes = new Uint8Array(event.data);
        const envelope = fromBinary(ServerEnvelopeSchema, bytes);
        
        if (this.onMessage) {
          this.onMessage(envelope);
        }
      } catch (err) {
        console.error("[WideboiClient] Failed to decode protobuf message:", err);
      }
    };
  }

  public send(envelope: ClientEnvelope) {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      console.warn("[WideboiClient] Cannot send, not connected");
      return;
    }
    
    const bytes = toBinary(ClientEnvelopeSchema, envelope);
    this.ws.send(bytes);
  }

  public disconnect() {
    if (this.ws) {
      this.ws.close();
      this.ws = null;
    }
  }
}
