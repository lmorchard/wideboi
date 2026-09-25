import { afterEach, expect, it, vi } from 'vitest';
import { create, toBinary } from '@bufbuild/protobuf';
import { WideboiClient } from './client';
import { RenderStats } from './stats';
import { MsgPaneUpdateSchema, ServerMessageSchema } from './gen/internal/protocol/wirepb/wideboi_pb';

afterEach(() => { vi.unstubAllGlobals(); });

it('keeps the credential out of the browser WebSocket URL', () => {
  const opened: { url: string; protocols?: string[] }[] = [];
  class FakeWebSocket {
    constructor(url: string, protocols?: string[]) {
      opened.push({ url, protocols });
    }
  }
  vi.stubGlobal('WebSocket', FakeWebSocket);

  new WideboiClient('ws://localhost:8080/ws', 'secret123').connect();

  expect(opened).toEqual([{
    url: 'ws://localhost:8080/ws',
    protocols: ['wideboi.v7', 'wideboi-token.c2VjcmV0MTIz'],
  }]);
  expect(JSON.stringify(opened[0].url)).not.toContain('secret123');
});

it('always offers the wire version, even without a token', () => {
  let offered: string[] | undefined;
  class FakeWebSocket {
    constructor(_url: string, protocols?: string[]) { offered = protocols; }
  }
  vi.stubGlobal('WebSocket', FakeWebSocket);
  new WideboiClient('ws://localhost:8080/ws').connect();
  expect(offered).toEqual(['wideboi.v7']);
});

it('refuses an opened connection that selected another protocol', () => {
  class FakeWebSocket {
    onopen?: () => void;
    protocol = 'wideboi.v1';
    binaryType = 'blob';
    close = vi.fn();
  }
  let socket: FakeWebSocket | undefined;
  vi.stubGlobal('WebSocket', class extends FakeWebSocket {
    constructor() { super(); socket = this; }
  });
  const client = new WideboiClient('ws://localhost:8080/ws');
  const connected = vi.fn();
  const disconnected = vi.fn();
  client.onConnect = connected;
  client.onDisconnect = disconnected;
  client.connect();
  socket?.onopen?.();
  expect(connected).not.toHaveBeenCalled();
  expect(disconnected).toHaveBeenCalledOnce();
  expect(socket?.close).toHaveBeenCalledOnce();
});

it('times protobuf decode per message only when given stats', () => {
  class FakeWebSocket {
    onmessage?: (event: { data: ArrayBuffer }) => void;
    protocol = 'wideboi.v7';
    binaryType = 'blob';
    close = vi.fn();
  }
  let socket: FakeWebSocket | undefined;
  vi.stubGlobal('WebSocket', class extends FakeWebSocket {
    constructor() { super(); socket = this; }
  });
  let clock = 10;
  const now = vi.spyOn(performance, 'now').mockImplementation(() => (clock += 3));
  const encoded = toBinary(ServerMessageSchema, create(ServerMessageSchema, {
    msg: { case: 'paneUpdate', value: create(MsgPaneUpdateSchema, { paneId: 7, cols: 1, rows: 1 }) },
  }));
  const data = encoded.buffer.slice(encoded.byteOffset, encoded.byteOffset + encoded.byteLength) as ArrayBuffer;

  const plain = new WideboiClient('ws://localhost:8080/ws');
  const plainReceived = vi.fn();
  plain.onMessage = plainReceived;
  plain.connect();
  socket?.onmessage?.({ data });
  expect(plainReceived).toHaveBeenCalledOnce();
  expect(now).not.toHaveBeenCalled();

  const stats = new RenderStats();
  const client = new WideboiClient('ws://localhost:8080/ws', '', stats);
  const received = vi.fn();
  client.onMessage = received;
  client.connect();
  socket?.onmessage?.({ data });
  socket?.onmessage?.({ data });
  expect(received).toHaveBeenCalledTimes(2);
  expect(received.mock.calls[0][0].msg.value.paneId).toBe(7);

  const s = stats.summary(1000);
  expect([s.messages, s.bytes]).toEqual([2, 2 * encoded.byteLength]);
  expect(s.decode).toEqual({ count: 2, avg: 3, p50: 3, p95: 3, max: 3 });
  expect(now).toHaveBeenCalledTimes(4);
  now.mockRestore();
});
