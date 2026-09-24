import { expect, it, vi } from 'vitest';
import { create, toBinary } from '@bufbuild/protobuf';
import { WideboiClient } from './client';
import { ServerMessageSchema } from './gen/internal/protocol/wirepb/wideboi_pb';

it('ignores events from a connection replaced during reconnect', () => {
  const sockets: FakeSocket[] = [];
  class FakeSocket {
    onopen?: () => void;
    onclose?: () => void;
    onmessage?: (event: { data: ArrayBuffer }) => void;
    binaryType = 'blob';
    protocol = 'wideboi.v4';
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
  const closed = toBinary(ServerMessageSchema, create(ServerMessageSchema, { msg: { case: 'paneClosed', value: { paneId: 1 } } }));
  old.onmessage?.({ data: closed.slice().buffer });
  expect(connected).not.toHaveBeenCalled();
  expect(disconnected).not.toHaveBeenCalled();
  expect(message).not.toHaveBeenCalled();
  sockets[1].onopen?.();
  expect(connected).toHaveBeenCalledOnce();
  expect(sockets[1].binaryType).toBe('arraybuffer');
  sockets[1].onmessage?.({ data: closed.slice().buffer });
  expect(message).toHaveBeenCalledOnce();
  expect(message.mock.lastCall?.[0].msg).toMatchObject({ case: 'paneClosed', value: { paneId: 1 } });
  vi.unstubAllGlobals();
});
