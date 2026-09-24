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
    protocols: ['wideboi-token.c2VjcmV0MTIz'],
  }]);
  expect(JSON.stringify(opened[0].url)).not.toContain('secret123');
});
