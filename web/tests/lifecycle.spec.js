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
        this.protocol = 'wideboi.v3';
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

test('pane elements keep their widths and browser scrolling reveals focus', async ({ page }) => {
  await page.addInitScript(() => {
    window.testSockets = [];
    window.WebSocket = class {
      static OPEN = 1;
      constructor(url) {
        this.protocol = 'wideboi.v3';
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
  await page.setViewportSize({ width: 500, height: 420 });
  await page.goto('/');
  await page.getByRole('button', { name: 'Connect' }).click();
  await page.evaluate(() => window.testSockets[0].open());
  await expect.poll(() => page.evaluate(() => window.testSockets[0].sent.length)).toBeGreaterThan(0);
  await page.evaluate(async () => {
    const { serverBytes } = await import('/tests/browser-fixture.ts');
    window.testSockets[0].message(serverBytes({ case: 'layoutSnapshot', value: {
      columns: [1, 2, 3, 4].map(paneId => ({ paneId, width: 40, height: 20 })),
    } }));
    window.testSockets[0].message(serverBytes({ case: 'paneUpdate', value: {
      paneId: 4, generation: 1n, cols: 40, rows: 20,
      lines: Array.from({ length: 20 }, () => ({ cells: Array.from({ length: 40 }, () => ({ content: ' ', width: 1 })) })),
      mouseTracking: true,
    } }));
  });
  const panes = page.locator('wideboi-pane');
  await expect(panes).toHaveCount(4);
  const before = await panes.evaluateAll(elements => elements.map(element => element.getBoundingClientRect().width));
  expect(before.every(width => width === before[0])).toBe(true);
  expect(before[0]).toBeGreaterThan(250);
  await page.getByRole('combobox', { name: 'Focus Pane:' }).selectOption('4');
  await expect.poll(() => page.locator('.pane-strip').evaluate(element => element.scrollLeft)).toBeGreaterThan(0);
  const after = await panes.evaluateAll(elements => elements.map(element => element.getBoundingClientRect().width));
  expect(after).toEqual(before);
  await expect(page.locator('wideboi-pane canvas')).toHaveCount(4);

  const messages = () => page.evaluate(async () => {
    const { clientMessages } = await import('/tests/browser-fixture.ts');
    return clientMessages(window.testSockets[0].sent).map(msg => msg);
  });
  const attachedRows = (await messages()).find(msg => msg.case === 'attach').value.rows;
  const canvasHeight = await page.locator('wideboi-pane canvas').nth(3)
    .evaluate(canvas => canvas.getBoundingClientRect().height);
  expect(canvasHeight).toBeGreaterThanOrEqual((attachedRows - 2) * 16.8);
  const resizeCount = (await messages()).filter(msg => msg.case === 'resize').length;
  await page.locator('wideboi-pane canvas').nth(3).click({ position: { x: 20, y: 26 } });
  await expect.poll(async () => (await messages()).find(msg => msg.case === 'mouse')?.value)
    .toMatchObject({ paneId: 4, x: 2, y: 1 });
  expect((await messages()).filter(msg => msg.case === 'resize')).toHaveLength(resizeCount);
  await page.locator('wideboi-pane canvas').first().click({ position: { x: 20, y: 26 } });
  await expect(page.getByRole('combobox', { name: 'Focus Pane:' })).toHaveValue('1');
  expect(await page.evaluate(() => {
    const app = document.querySelector('wideboi-app');
    const pane = app.shadowRoot.querySelector('wideboi-pane');
    return app.shadowRoot.activeElement === pane && pane.shadowRoot.activeElement?.tagName === 'CANVAS';
  })).toBe(true);

  await page.evaluate(async () => {
    const { serverBytes } = await import('/tests/browser-fixture.ts');
    window.paneCanvas = document.querySelector('wideboi-app').shadowRoot.querySelector('wideboi-pane').shadowRoot.querySelector('canvas');
    window.testSockets[0].message(serverBytes({ case: 'layoutSnapshot', value: {
      columns: [4, 3, 2, 1].map(paneId => ({ paneId, width: 40, height: 20 })),
    } }));
  });
  await expect.poll(() => panes.evaluateAll(elements => elements.map(element => element.paneId)))
    .toEqual([4, 3, 2, 1]);
  expect(await page.evaluate(() => window.paneCanvas === document.querySelector('wideboi-app').shadowRoot
    .querySelectorAll('wideboi-pane')[3].shadowRoot.querySelector('canvas'))).toBe(true);

  await page.getByRole('combobox', { name: 'Focus Pane:' }).selectOption('4');
  await page.evaluate(async () => {
    const { serverBytes } = await import('/tests/browser-fixture.ts');
    window.testSockets[0].message(serverBytes({ case: 'paneClosed', value: { paneId: 4 } }));
  });
  await expect(page.getByRole('combobox', { name: 'Focus Pane:' })).toHaveValue('3');
  expect(await page.evaluate(() => document.querySelector('wideboi-app').focusedPaneId)).toBe(3);
  expect(await page.evaluate(() => {
    const app = document.querySelector('wideboi-app');
    const pane = [...app.shadowRoot.querySelectorAll('wideboi-pane')].find(element => element.paneId === 3);
    return app.shadowRoot.activeElement === pane && pane.shadowRoot.activeElement?.tagName === 'CANVAS';
  })).toBe(true);
  await page.keyboard.type('q');
  await expect.poll(async () => (await messages()).some(msg =>
    msg.case === 'input' && msg.value.paneId === 3 && msg.value.key?.text === 'q')).toBe(true);
});
