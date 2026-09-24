import { afterEach, expect, it, vi } from 'vitest';
import { WideboiClient } from './client';

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
    protocols: ['wideboi.v3', 'wideboi-token.c2VjcmV0MTIz'],
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
  expect(offered).toEqual(['wideboi.v3']);
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
