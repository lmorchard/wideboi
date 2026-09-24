import { expect, it, vi } from 'vitest';
import { WideboiClient } from './client';

it('ignores events from a connection replaced during reconnect', () => {
  const sockets: FakeSocket[] = [];
  class FakeSocket {
    onopen?: () => void;
    onclose?: () => void;
    onmessage?: (event: { data: string }) => void;
    onerror?: () => void;
    constructor() { sockets.push(this); }
    close() {}
  }
  vi.stubGlobal('WebSocket', FakeSocket);
  const client = new WideboiClient('ws://localhost/ws');
  const connected = vi.fn();
  const disconnected = vi.fn();
  const message = vi.fn();
  client.onConnect = connected;
  client.onDisconnect = disconnected;
  client.onMessage = message;
  client.connect();
  const old = sockets[0];
  client.connect();
  old.onopen?.();
  old.onclose?.();
  old.onmessage?.({ data: '{"t":"MsgPaneClosed","p":{"PaneID":1}}' });
  expect(connected).not.toHaveBeenCalled();
  expect(disconnected).not.toHaveBeenCalled();
  expect(message).not.toHaveBeenCalled();
  sockets[1].onopen?.();
  expect(connected).toHaveBeenCalledOnce();
  vi.unstubAllGlobals();
});
