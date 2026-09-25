import { test, expect } from '@playwright/test';

test.use({ viewport: { width: 390, height: 700 }, hasTouch: true, isMobile: true });

async function connect(page) {
  await page.addInitScript(() => {
    window.testSockets = [];
    window.WebSocket = class {
      static OPEN = 1;
      constructor(_url, protocols) {
        this.protocol = 'wideboi.v10';
        this.readyState = 0;
        this.sent = [];
        if (protocols?.includes('wideboi.v10')) window.testSockets.push(this);
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
  await page.evaluate(async () => {
    const { serverBytes } = await import('/tests/browser-fixture.ts');
    window.testSockets[0].message(serverBytes({ case: 'layoutSnapshot', value: {
      columns: [{ paneId: 1, width: 80, height: 24 }, { paneId: 2, width: 80, height: 24 }],
      paneTitles: { 1: 'First', 2: 'Second' },
    } }));
    for (const paneId of [1, 2]) {
      window.testSockets[0].message(serverBytes({ case: 'paneUpdate', value: {
        paneId, generation: 1n, cols: 80, rows: 24,
        lines: Array.from({ length: 24 }, () => ({
          cells: Array.from({ length: 80 }, () => ({ content: 'x', width: 1 })),
        })),
        mouseTracking: true,
      } }));
    }
  });
}

async function sent(page) {
  return page.evaluate(async () => {
    const { clientMessages } = await import('/tests/browser-fixture.ts');
    return clientMessages(window.testSockets[0].sent).map(msg => ({
      case: msg.case,
      value: msg.value,
    }));
  });
}

test('narrow view shows one pane and sends draft text separately from Enter', async ({ page }) => {
  await connect(page);
  await expect(page.getByRole('combobox', { name: 'Mobile pane' })).toBeVisible();
  await page.setViewportSize({ width: 320, height: 700 });
  await expect(page.getByRole('button', { name: 'Enter key' })).toBeInViewport();
  const panes = page.locator('wideboi-pane');
  await expect(panes.nth(0)).toBeVisible();
  await expect(panes.nth(1)).toBeHidden();
  await page.getByRole('button', { name: 'Next pane' }).click();
  await expect(panes.nth(1)).toBeVisible();
  await expect(panes.nth(0)).toBeHidden();

  await page.getByRole('textbox', { name: 'Command or response' }).fill('echo hello');
  expect((await sent(page)).filter(msg => msg.case === 'input')).toHaveLength(0);
  await page.getByRole('button', { name: 'Send text' }).click();
  const afterSend = await sent(page);
  const inputs = afterSend.filter(msg => msg.case === 'input');
  expect(inputs).toHaveLength(1);
  expect(inputs[0].value.paneId).toBe(2);
  expect(new TextDecoder().decode(inputs[0].value.data)).toBe('echo hello');
  await page.getByRole('button', { name: 'Enter key' }).click();
  const afterEnter = (await sent(page)).filter(msg => msg.case === 'input');
  expect(afterEnter).toHaveLength(2);
  expect(afterEnter[1].value.key.code).toBe(13);
});

test('touch pans without terminal mouse messages and keyboard-sized view does not resize PTY', async ({ page }) => {
  await connect(page);
  const pane = page.locator('wideboi-pane').first();
  const viewport = pane.locator('.viewport');
  await expect.poll(() => viewport.evaluate(el => el.scrollWidth > el.clientWidth)).toBe(true);
  const before = (await sent(page)).filter(msg => msg.case === 'resize').length;
  await viewport.tap();
  await expect(page.getByRole('textbox', { name: 'Command or response' })).toBeFocused();
  const bounds = await viewport.boundingBox();
  const cdp = await page.context().newCDPSession(page);
  const y = bounds.y + Math.min(bounds.height / 2, 100);
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x: bounds.x + 300, y, id: 1 }] });
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x: bounds.x + 180, y, id: 1 }] });
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x: bounds.x + 80, y, id: 1 }] });
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
  await expect.poll(() => viewport.evaluate(el => el.scrollLeft)).toBeGreaterThan(0);
  expect((await sent(page)).filter(msg => msg.case === 'mouse')).toHaveLength(0);
  await page.evaluate(() => {
    Object.defineProperty(window.visualViewport, 'height', { configurable: true, value: 420 });
    window.visualViewport.dispatchEvent(new Event('resize'));
  });
  await expect.poll(() => page.locator('wideboi-app').evaluate(el =>
    Math.round(el.getBoundingClientRect().height))).toBe(420);
  await expect.poll(() => pane.evaluate(el => el.getBoundingClientRect().height)).toBeLessThan(420);
  await expect.poll(() => viewport.evaluate(el =>
    Math.abs(el.scrollHeight - el.clientHeight - el.scrollTop))).toBeLessThan(2);
  await viewport.evaluate(el => { el.scrollTop = 0; });
  await expect.poll(() => viewport.evaluate(el => el.scrollTop)).toBe(0);
  await page.evaluate(() => {
    Object.defineProperty(window.visualViewport, 'height', { configurable: true, value: 360 });
    window.visualViewport.dispatchEvent(new Event('resize'));
  });
  await expect.poll(() => viewport.evaluate(el => el.scrollTop)).toBe(0);
  expect((await sent(page)).filter(msg => msg.case === 'resize')).toHaveLength(before);
});

test('terminal key buttons work and composing draft text stays in the draft', async ({ page }) => {
  await connect(page);
  const draft = page.getByRole('textbox', { name: 'Command or response' });
  await expect(draft).toHaveAttribute('autocapitalize', 'off');
  await expect(draft).toHaveAttribute('autocorrect', 'off');
  await expect(draft).toHaveAttribute('spellcheck', 'false');
  await draft.focus();
  await draft.press('a');
  await draft.evaluate(el => {
    el.dispatchEvent(new CompositionEvent('compositionend', { bubbles: true, composed: true, data: '文' }));
    const data = new DataTransfer();
    data.setData('text/plain', 'pasted');
    el.dispatchEvent(new ClipboardEvent('paste', { bubbles: true, composed: true, clipboardData: data }));
  });
  expect((await sent(page)).filter(msg => msg.case === 'input')).toHaveLength(0);
  await page.getByRole('button', { name: 'Escape key' }).click();
  await page.getByRole('button', { name: 'Control modifier' }).click();
  await page.getByRole('button', { name: 'C key' }).click();
  const inputs = (await sent(page)).filter(msg => msg.case === 'input');
  expect(inputs.map(msg => msg.value.key.code)).toEqual([27, 99]);
  expect(inputs[1].value.key.mod & 4).toBe(4);
  await expect(page.getByRole('button', { name: 'Control modifier' })).toHaveAttribute('aria-pressed', 'false');
});
