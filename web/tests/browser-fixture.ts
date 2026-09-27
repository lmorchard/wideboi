import type { Page } from '@playwright/test';
import { create, fromBinary, toBinary } from '@bufbuild/protobuf';
import {
  ClientMessageSchema, ServerMessageSchema, type ServerMessage,
} from '../src/gen/internal/protocol/wirepb/wideboi_pb';
import { VERSION_PROTOCOL } from '../src/version';

export { VERSION_PROTOCOL } from '../src/version';

export interface TestSocket {
  url: string;
  protocols?: string | string[];
  protocol: string;
  readyState: number;
  sent: Uint8Array[];
  send(data: ArrayBuffer | ArrayBufferView): void;
  close(): void;
  open(): void;
  message(bytes: Uint8Array): void;
  onopen?: () => void;
  onclose?: () => void;
  onmessage?: (event: { data: ArrayBuffer }) => void;
}

declare global {
  interface Window {
    testSockets: TestSocket[];
    paneOne?: any;
    drawnText?: string[];
  }
}

export function serverBytes(msg: NonNullable<ServerMessage['msg']>): Uint8Array {
  return toBinary(ServerMessageSchema, create(ServerMessageSchema, { msg }));
}

export function clientMessages(bytes: Uint8Array[]) {
  return bytes.map(data => fromBinary(ClientMessageSchema, data).msg);
}

export async function installMockWebSocket(page: Page, protocol = VERSION_PROTOCOL): Promise<void> {
  await page.addInitScript((proto) => {
    window.testSockets = [];
    // @ts-expect-error Mocking window.WebSocket
    window.WebSocket = class {
      static OPEN = 1;
      url: string;
      protocols?: string | string[];
      protocol: string;
      readyState: number;
      sent: Uint8Array[];
      onopen?: () => void;
      onclose?: () => void;
      onmessage?: (event: { data: ArrayBuffer }) => void;

      constructor(url: string, protocols?: string | string[]) {
        this.url = url;
        this.protocols = protocols;
        this.protocol = proto;
        this.readyState = 0;
        this.sent = [];
        const list = Array.isArray(protocols) ? protocols : protocols ? [protocols] : [];
        if (list.includes(proto)) {
          window.testSockets.push(this as any);
        }
      }
      send(data: ArrayBuffer | ArrayBufferView) {
        this.sent.push(new Uint8Array(data as any));
      }
      close() {
        this.readyState = 3;
        this.onclose?.();
      }
      open() {
        this.readyState = 1;
        this.onopen?.();
      }
      message(bytes: Uint8Array) {
        this.onmessage?.({ data: bytes.buffer });
      }
    };
  }, protocol);
}
