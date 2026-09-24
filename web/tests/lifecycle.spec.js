import { test, expect } from '@playwright/test';

test('browser connects, renders, types, resizes, reconnects, and closes a pane', async ({ page }) => {
  await page.addInitScript(() => {
    window.testSockets = [];
    window.drawnText = [];
    const original = CanvasRenderingContext2D.prototype.fillText;
    CanvasRenderingContext2D.prototype.fillText = function (text, ...args) {
      window.drawnText.push(text);
      return original.call(this, text, ...args);
    };
    window.WebSocket = class {
      static OPEN = 1;
      constructor(url, protocols) {
        this.url = url;
        this.protocols = protocols;
        this.protocol = 'wideboi.v2';
        this.readyState = 0;
        this.sent = [];
        if (url.endsWith('/ws')) window.testSockets.push(this);
      }
      send(data) { this.sent.push(new Uint8Array(data)); }
      close() { this.readyState = 3; this.onclose?.(); }
      open() { this.readyState = 1; this.onopen?.(); }
      message(bytes) { this.onmessage?.({ data: bytes.buffer }); }
    };
  });

  await page.goto('/');
  await page.getByRole('button', { name: 'Connect' }).click();
  await page.evaluate(() => window.testSockets[0].open());
  await expect(page.getByText('Focus Pane:')).toBeVisible();

  const messages = () => page.evaluate(async () => {
    const { clientMessages } = await import('/tests/browser-fixture.ts');
    return clientMessages(window.testSockets.at(-1).sent).map(msg => ({
      case: msg.case, value: msg.value,
    }));
  });
  await expect.poll(async () => (await messages()).find(msg => msg.case === 'attach')?.value.cols).toBeGreaterThan(0);

  await page.evaluate(async () => {
    const { serverBytes } = await import('/tests/browser-fixture.ts');
    const socket = window.testSockets[0];
    socket.message(serverBytes({ case: 'layoutSnapshot', value: {
      columns: [{ paneId: 1, width: 40, height: 10 }], paneTitles: { 1: 'Shell' },
    } }));
    socket.message(serverBytes({ case: 'paneUpdate', value: {
      paneId: 1, generation: 1n, cols: 1, rows: 1,
      lines: [{ cells: [{ content: 'Z', width: 1 }] }],
    } }));
  });
  await expect(page.getByRole('option', { name: '[1] Shell' })).toHaveCount(1);
  await expect.poll(() => page.evaluate(() => window.drawnText.includes('Z'))).toBe(true);

  await page.locator('canvas').focus();
  await page.keyboard.type('x');
  await expect.poll(async () => (await messages()).some(msg => msg.case === 'input' && msg.value.paneId === 1 && msg.value.key?.text === 'x')).toBe(true);

  const resizeCount = (await messages()).filter(msg => msg.case === 'resize').length;
  await page.setViewportSize({ width: 700, height: 500 });
  await expect.poll(async () => (await messages()).filter(msg => msg.case === 'resize').length).toBeGreaterThan(resizeCount);

  await page.evaluate(() => window.testSockets[0].close());
  await expect(page.getByText('Disconnected from server.')).toBeVisible();
  await page.getByRole('button', { name: 'Connect' }).click();
  await page.evaluate(() => window.testSockets[1].open());
  await expect.poll(async () => (await messages()).some(msg => msg.case === 'attach')).toBe(true);

  await page.evaluate(async () => {
    const { serverBytes } = await import('/tests/browser-fixture.ts');
    const socket = window.testSockets[1];
    socket.message(serverBytes({ case: 'layoutSnapshot', value: {
      columns: [{ paneId: 1, width: 40, height: 10 }], paneTitles: { 1: 'Shell' },
    } }));
    socket.message(serverBytes({ case: 'paneClosed', value: { paneId: 1 } }));
  });
  await expect(page.getByRole('option', { name: '[1] Shell' })).toHaveCount(0);
});
