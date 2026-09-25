import { test, expect } from '@playwright/test';

test('a short browser pane can reach the bottom of a taller terminal without resizing it', async ({ page }) => {
  await page.addInitScript(() => {
    window.testSockets = [];
    window.drawnText = [];
    const fillText = CanvasRenderingContext2D.prototype.fillText;
    CanvasRenderingContext2D.prototype.fillText = function (text, ...args) {
      window.drawnText.push(text);
      return fillText.call(this, text, ...args);
    };
    window.WebSocket = class {
      static OPEN = 1;
      constructor(_url, protocols) {
        this.protocol = 'wideboi.v7';
        this.readyState = 0;
        this.sent = [];
        if (protocols?.includes('wideboi.v7')) window.testSockets.push(this);
      }
      send(data) { this.sent.push(new Uint8Array(data)); }
      close() { this.readyState = 3; this.onclose?.(); }
      open() { this.readyState = 1; this.onopen?.(); }
      message(bytes) { this.onmessage?.({ data: bytes.buffer }); }
    };
  });

  await page.setViewportSize({ width: 600, height: 320 });
  await page.goto('/');
  await page.getByRole('button', { name: 'Connect' }).click();
  await page.evaluate(() => window.testSockets[0].open());
  await expect(page.getByRole('combobox', { name: 'Layout' })).toBeVisible();
  await page.evaluate(async () => {
    const { serverBytes } = await import('/tests/browser-fixture.ts');
    window.testSockets[0].message(serverBytes({ case: 'layoutSnapshot', value: {
      columns: [{ paneId: 1, width: 40, height: 50 }],
    } }));
    window.testSockets[0].message(serverBytes({ case: 'paneUpdate', value: {
      paneId: 1, generation: 1n, cols: 40, rows: 50,
      lines: Array.from({ length: 50 }, (_, y) => ({
        cells: Array.from({ length: 40 }, (_, x) => ({ content: x === 0 ? `row-${y}` : ' ', width: 1 })),
      })),
      mouseTracking: true,
    } }));
  });

  const pane = page.locator('wideboi-pane');
  const viewport = pane.locator('.viewport');
  await expect.poll(() => viewport.evaluate(el => el.scrollHeight > el.clientHeight)).toBe(true);
  await expect.poll(() => page.evaluate(() => window.drawnText.includes('row-49'))).toBe(true);
  const sizeBefore = await page.evaluate(async () => {
    const { clientMessages } = await import('/tests/browser-fixture.ts');
    return clientMessages(window.testSockets[0].sent).filter(msg => msg.case === 'resize').length;
  });
  await expect.poll(() => viewport.evaluate(el => el.scrollTop)).toBeGreaterThan(0);
  await viewport.evaluate(el => { el.scrollTop = 0; });
  await expect.poll(() => viewport.evaluate(el => el.scrollTop)).toBe(0);
  await viewport.hover();
  await page.mouse.wheel(0, 120);
  await expect.poll(() => viewport.evaluate(el => el.scrollTop)).toBeGreaterThan(0);
  const historyCount = async () => page.evaluate(async () => {
    const { clientMessages } = await import('/tests/browser-fixture.ts');
    return clientMessages(window.testSockets[0].sent).filter(msg => msg.case === 'scroll').length;
  });
  expect(await historyCount()).toBe(0);
  await page.keyboard.down('Alt');
  await page.mouse.wheel(0, -120);
  await page.keyboard.up('Alt');
  await expect.poll(historyCount).toBe(1);
  await viewport.evaluate(el => { el.scrollTop = el.scrollHeight; });
  await expect.poll(() => viewport.evaluate(el =>
    Math.abs(el.scrollHeight - el.clientHeight - el.scrollTop))).toBeLessThan(2);

  const mouseY = await page.evaluate(() => {
    const pane = document.querySelector('wideboi-app').shadowRoot.querySelector('wideboi-pane');
    const viewport = pane.shadowRoot.querySelector('.viewport');
    const rect = viewport.getBoundingClientRect();
    return pane.cellAt(rect.left + 20, rect.bottom - 10).y;
  });
  expect(mouseY).toBeGreaterThan(30);
  const bounds = await viewport.boundingBox();
  await page.mouse.click(bounds.x + 20, bounds.y + bounds.height - 10);
  await expect.poll(async () => page.evaluate(async () => {
    const { clientMessages } = await import('/tests/browser-fixture.ts');
    return clientMessages(window.testSockets[0].sent).find(msg => msg.case === 'mouse')?.value.y;
  })).toBeGreaterThan(30);
  const sizeAfter = await page.evaluate(async () => {
    const { clientMessages } = await import('/tests/browser-fixture.ts');
    return clientMessages(window.testSockets[0].sent).filter(msg => msg.case === 'resize').length;
  });
  expect(sizeAfter).toBe(sizeBefore);

  // Click "Fit to Window" button and verify VerbClaimSize is sent
  await page.getByRole('button', { name: 'Fit to Window' }).click();
  const messages = () => page.evaluate(async () => {
    const { clientMessages } = await import('/tests/browser-fixture.ts');
    return clientMessages(window.testSockets[0].sent);
  });
  await expect.poll(async () => (await messages()).some(msg => msg.case === 'verb' && msg.value.verb === 14)).toBe(true);
});
