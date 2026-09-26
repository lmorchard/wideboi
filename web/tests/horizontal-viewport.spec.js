import { test, expect } from '@playwright/test';

test('a narrow browser card pans across a wider terminal grid without resizing it', async ({ page }) => {
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
        this.protocol = 'wideboi.v13';
        this.readyState = 0;
        this.sent = [];
        if (protocols?.includes('wideboi.v13')) window.testSockets.push(this);
      }
      send(data) { this.sent.push(new Uint8Array(data)); }
      close() { this.readyState = 3; this.onclose?.(); }
      open() { this.readyState = 1; this.onopen?.(); }
      message(bytes) { this.onmessage?.({ data: bytes.buffer }); }
    };
  });

  await page.setViewportSize({ width: 500, height: 320 });
  await page.goto('/');
  await page.getByRole('button', { name: 'Connect' }).click();
  await page.evaluate(() => window.testSockets[0].open());
  await expect(page.getByRole('combobox', { name: 'Layout' })).toBeVisible();
  await page.evaluate(async () => {
    const { serverBytes } = await import('/tests/browser-fixture.ts');
    window.testSockets[0].message(serverBytes({ case: 'layoutSnapshot', value: {
      columns: [{ paneId: 1, width: 80, height: 10 }],
    } }));
    window.testSockets[0].message(serverBytes({ case: 'paneUpdate', value: {
      paneId: 1, generation: 1n, cols: 80, rows: 10,
      lines: Array.from({ length: 10 }, () => ({
        cells: Array.from({ length: 80 }, (_, x) => ({ content: x === 79 ? 'R' : ' ', width: 1 })),
      })),
      mouseTracking: true,
    } }));
  });

  const resizeBeforeChoice = await page.evaluate(async () => {
    const { clientMessages } = await import('/tests/browser-fixture.ts');
    return clientMessages(window.testSockets[0].sent).filter(msg => msg.case === 'resize').length;
  });
  const cardWidth = page.getByRole('spinbutton', { name: 'Pane width' });
  const controlBounds = await cardWidth.boundingBox();
  expect(controlBounds.x + controlBounds.width).toBeLessThanOrEqual(500);
  await cardWidth.fill('40');
  await cardWidth.dispatchEvent('change');
  await expect.poll(() => page.evaluate(async () => {
    const { clientMessages } = await import('/tests/browser-fixture.ts');
    return clientMessages(window.testSockets[0].sent).find(msg => msg.case === 'setPaneWidth')?.value.width;
  })).toBe(40);

  const pane = page.locator('wideboi-pane');
  const viewport = pane.locator('.viewport');
  await expect.poll(() => viewport.evaluate(el => el.scrollWidth > el.clientWidth)).toBe(true);
  expect(await pane.evaluate(el => el.getBoundingClientRect().width)).toBeLessThan(
    await pane.locator('canvas').evaluate(el => el.getBoundingClientRect().width));
  await expect.poll(() => page.evaluate(() => window.drawnText.includes('R'))).toBe(true);
  await page.evaluate(async () => {
    const { serverBytes } = await import('/tests/browser-fixture.ts');
    window.testSockets[0].message(serverBytes({ case: 'layoutSnapshot', value: {
      columns: [{ paneId: 1, width: 80, height: 10 }],
    } }));
  });
  await expect(cardWidth).toHaveValue('40');
  await page.getByRole('button', { name: 'Fit to Window' }).click();
  await expect.poll(() => page.evaluate(async () => {
    const { clientMessages } = await import('/tests/browser-fixture.ts');
    return clientMessages(window.testSockets[0].sent).findLast(msg => msg.case === 'verb')?.value.widths?.[1];
  })).toBe(40);
  await expect.poll(() => viewport.evaluate(el => el.scrollWidth > el.clientWidth)).toBe(true);
  const counts = async () => page.evaluate(async () => {
    const { clientMessages } = await import('/tests/browser-fixture.ts');
    const messages = clientMessages(window.testSockets[0].sent);
    return {
      resize: messages.filter(msg => msg.case === 'resize').length,
      stripLeft: document.querySelector('wideboi-app').shadowRoot.querySelector('.pane-strip').scrollLeft,
    };
  });
  const before = await counts();
  expect(before.resize).toBe(resizeBeforeChoice);
  await viewport.hover();
  await page.mouse.wheel(300, 0);
  await expect.poll(() => viewport.evaluate(el => el.scrollLeft)).toBeGreaterThan(0);
  await viewport.evaluate(el => { el.scrollLeft = el.scrollWidth; });
  await expect.poll(() => viewport.evaluate(el =>
    Math.abs(el.scrollWidth - el.clientWidth - el.scrollLeft))).toBeLessThan(2);
  const bounds = await viewport.boundingBox();
  await page.mouse.click(bounds.x + bounds.width - 15, bounds.y + 30);
  await expect.poll(async () => page.evaluate(async () => {
    const { clientMessages } = await import('/tests/browser-fixture.ts');
    return clientMessages(window.testSockets[0].sent).find(msg => msg.case === 'mouse')?.value.x;
  })).toBeGreaterThan(40);
  const after = await counts();
  expect(after).toEqual(before);
  await cardWidth.fill('80');
  await cardWidth.dispatchEvent('change');
  await expect.poll(() => viewport.evaluate(el => el.scrollLeft)).toBe(0);
  expect(await counts()).toEqual(before);
});
