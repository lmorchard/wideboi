import { test, expect } from '@playwright/test';
import { installMockWebSocket, VERSION_PROTOCOL } from './browser-fixture';

test('desktop session window connects to its named local session automatically', async ({ page }) => {
  await installMockWebSocket(page);
  await page.goto('/?session=project#token=local-secret');
  await expect.poll(() => page.evaluate(() => window.testSockets.length)).toBe(1);
  const connection = await page.evaluate(() => ({
    url: window.testSockets[0].url,
    protocols: window.testSockets[0].protocols,
  }));
  expect(connection.url).toBe('ws://127.0.0.1:4179/ws?session=project');
  expect(connection.protocols).toEqual([VERSION_PROTOCOL, 'wideboi-token.bG9jYWwtc2VjcmV0']);
  await expect(page.getByRole('button', { name: 'Reconnect' })).toBeVisible();
  await expect(page.getByPlaceholder('Token (optional)')).toHaveCount(0);
  await page.evaluate(() => window.testSockets[0].open());
  await expect.poll(() => page.evaluate(async () => {
    const { clientMessages } = await import('/tests/browser-fixture.ts');
    return clientMessages(window.testSockets[0].sent).some(msg => msg.case === 'attach');
  })).toBe(true);
});

test('browser connects, renders, types, resizes, reconnects, and closes a pane', async ({ page }) => {
  await page.addInitScript(() => {
    window.drawnText = [];
    const original = CanvasRenderingContext2D.prototype.fillText;
    CanvasRenderingContext2D.prototype.fillText = function (text, ...args) {
      window.drawnText?.push(text);
      return original.call(this, text, ...args);
    };
  });
  await installMockWebSocket(page);

  await page.goto('/');
  await page.getByRole('button', { name: 'Connect' }).click();
  await page.evaluate(() => window.testSockets[0].open());
  await expect(page.locator('.toolbar')).toBeVisible();
  await expect(page.locator('.terminal-shell .title')).toHaveCount(0);

  const messages = () => page.evaluate(async () => {
    const { clientMessages } = await import('/tests/browser-fixture.ts');
    return clientMessages(window.testSockets.at(-1)!.sent).map(msg => ({
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
  await expect(page.locator('.pane-tab[data-pane-id="1"]')).toContainText('Shell');
  await expect.poll(() => page.evaluate(() => window.drawnText?.includes('Z'))).toBe(true);

  await page.locator('canvas').focus();
  await page.keyboard.type('x');
  await expect.poll(async () => (await messages()).some(msg => msg.case === 'input' && msg.value.paneId === 1 && (msg.value.key as any)?.text === 'x')).toBe(true);

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
  await installMockWebSocket(page);
  await page.setViewportSize({ width: 500, height: 420 });
  await page.goto('/');
  await page.getByRole('button', { name: 'Connect' }).click();
  await page.evaluate(() => window.testSockets[0].open());
  await expect.poll(() => page.evaluate(() => window.testSockets[0].sent.length)).toBeGreaterThan(0);
  await page.locator('.toolbar .settings-btn').click();
  await page.locator('#settings-layout-mode').selectOption('scroll');
  await page.locator('.settings-dialog .close-btn').click();
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
  await page.locator('.pane-tab[data-pane-id="4"]').click();
  await expect.poll(() => page.locator('.pane-strip').evaluate(element => element.scrollLeft)).toBeGreaterThan(0);
  const after = await panes.evaluateAll(elements => elements.map(element => element.getBoundingClientRect().width));
  expect(after).toEqual(before);
  await expect(page.locator('wideboi-pane canvas')).toHaveCount(4);

  const messages = () => page.evaluate(async () => {
    const { clientMessages } = await import('/tests/browser-fixture.ts');
    return clientMessages(window.testSockets[0].sent).map(msg => msg);
  });
  const attachedRows = (await messages()).find(msg => msg.case === 'attach')!.value.rows;
  const canvasHeight = await page.locator('wideboi-pane canvas').nth(3)
    .evaluate(canvas => canvas.getBoundingClientRect().height);
  expect(canvasHeight).toBeGreaterThanOrEqual((attachedRows - 2) * 16.8);
  const resizeCount = (await messages()).filter(msg => msg.case === 'resize').length;
  await page.locator('wideboi-pane canvas').nth(3).click({ position: { x: 20, y: 26 } });
  await expect.poll(async () => (await messages()).find(msg => msg.case === 'mouse')?.value)
    .toMatchObject({ paneId: 4, x: 2, y: 1 });
  expect((await messages()).filter(msg => msg.case === 'resize')).toHaveLength(resizeCount);
  await page.locator('wideboi-pane canvas').first().click({ position: { x: 20, y: 26 } });
  await expect(page.locator('.pane-tab[data-pane-id="1"]')).toHaveAttribute('aria-selected', 'true');
  expect(await page.evaluate(() => {
    const app = document.querySelector('wideboi-app') as any;
    const pane = app.shadowRoot.querySelector('wideboi-pane');
    return app.shadowRoot.activeElement === pane && pane.shadowRoot.activeElement?.tagName === 'CANVAS';
  })).toBe(true);

  await page.evaluate(async () => {
    const { serverBytes } = await import('/tests/browser-fixture.ts');
    (window as any).paneCanvas = (document.querySelector('wideboi-app') as any).shadowRoot.querySelector('wideboi-pane').shadowRoot.querySelector('canvas');
    window.testSockets[0].message(serverBytes({ case: 'layoutSnapshot', value: {
      columns: [4, 3, 2, 1].map(paneId => ({ paneId, width: 40, height: 20 })),
    } }));
  });
  await expect.poll(() => panes.evaluateAll(elements => elements.map(element => (element as any).paneId)))
    .toEqual([4, 3, 2, 1]);
  expect(await page.evaluate(() => (window as any).paneCanvas === (document.querySelector('wideboi-app') as any).shadowRoot
    .querySelectorAll('wideboi-pane')[3].shadowRoot.querySelector('canvas'))).toBe(true);

  await page.locator('.pane-tab[data-pane-id="4"]').click();
  await page.evaluate(async () => {
    const { serverBytes } = await import('/tests/browser-fixture.ts');
    window.testSockets[0].message(serverBytes({ case: 'paneClosed', value: { paneId: 4 } }));
  });
  await expect(page.locator('.pane-tab[data-pane-id="3"]')).toHaveAttribute('aria-selected', 'true');
  expect(await page.evaluate(() => (document.querySelector('wideboi-app') as any).focusedPaneId)).toBe(3);
  expect(await page.evaluate(() => {
    const app = document.querySelector('wideboi-app') as any;
    const pane = [...app.shadowRoot.querySelectorAll('wideboi-pane')].find(element => (element as any).paneId === 3);
    return app.shadowRoot.activeElement === pane && pane.shadowRoot.activeElement?.tagName === 'CANVAS';
  })).toBe(true);
  await page.keyboard.type('q');
  await expect.poll(async () => (await messages()).some(msg =>
    msg.case === 'input' && msg.value.paneId === 3 && (msg.value.key as any)?.text === 'q')).toBe(true);
});

test('?stats=1 shows the stats overlay and reports periodically', async ({ page }) => {
  await page.clock.install();
  await installMockWebSocket(page);
  const reports: string[] = [];
  page.on('console', msg => {
    if (msg.type() === 'info' && msg.text().startsWith('[wideboi stats]')) reports.push(msg.text());
  });

  await page.goto('/?stats=1');
  await page.getByRole('button', { name: 'Connect' }).click();
  await page.evaluate(() => window.testSockets[0].open());
  const overlay = page.locator('.stats-overlay');
  await expect(overlay).toHaveText('stats: collecting…');

  await page.evaluate(async () => {
    const { serverBytes } = await import('/tests/browser-fixture.ts');
    const socket = window.testSockets[0];
    socket.message(serverBytes({ case: 'layoutSnapshot', value: {
      columns: [{ paneId: 1, width: 40, height: 10 }],
    } }));
    socket.message(serverBytes({ case: 'paneUpdate', value: {
      paneId: 1, generation: 1n, cols: 1, rows: 1,
      lines: [{ cells: [{ content: 'Z', width: 1 }] }],
    } }));
  });
  await page.clock.runFor(5000);
  await expect.poll(() => reports.length).toBeGreaterThan(0);
  await expect(overlay).not.toHaveText('stats: collecting…');
  // The overlay shows the same summary, one " | " part per line.
  expect(await overlay.textContent()).toBe(reports[0].replace('[wideboi stats] ', '').split(' | ').join('\n'));
});

test('without ?stats=1 there is no stats overlay', async ({ page }) => {
  await installMockWebSocket(page);
  await page.goto('/');
  await page.getByRole('button', { name: 'Connect' }).click();
  await page.evaluate(() => window.testSockets[0].open());
  await expect(page.locator('.toolbar')).toBeVisible();
  await expect(page.locator('.stats-overlay')).toHaveCount(0);
});

test('client handles prefix, double prefix, column focus, layout switch, and help overlay', async ({ page }) => {
  await installMockWebSocket(page);
  await page.goto('/');
  await page.getByRole('button', { name: 'Connect' }).click();
  await page.evaluate(() => window.testSockets[0].open());
  await expect(page.locator('.toolbar')).toBeVisible();

  const messages = () => page.evaluate(async () => {
    const { clientMessages } = await import('/tests/browser-fixture.ts');
    return clientMessages(window.testSockets.at(-1)!.sent).map(msg => ({
      case: msg.case, value: msg.value,
    }));
  });

  await page.evaluate(async () => {
    const { serverBytes } = await import('/tests/browser-fixture.ts');
    const socket = window.testSockets[0];
    socket.message(serverBytes({ case: 'layoutSnapshot', value: {
      columns: [
        { paneId: 1, width: 40, height: 10 },
        { paneId: 2, width: 40, height: 10 },
        { paneId: 3, width: 40, height: 10 },
      ],
      paneTitles: { 1: 'First', 2: 'Second', 3: 'Third' },
    } }));
  });

  await page.locator('wideboi-pane canvas').first().focus();

  // 1. Double prefix: Ctrl+B then Ctrl+B sends literal Ctrl+B key
  await page.keyboard.press('Control+b');
  await page.keyboard.press('Control+b');
  await expect.poll(async () => (await messages()).some(msg =>
    msg.case === 'input' && msg.value.paneId === 1 && (msg.value.key as any)?.code === 98 && (msg.value.key as any)?.mod === 4
  )).toBe(true);

  // 2. Column jump: Ctrl+B then '2' focuses pane 2
  await page.keyboard.press('Control+b');
  await page.keyboard.press('2');
  await expect(page.locator('.pane-tab[data-pane-id="2"]')).toHaveAttribute('aria-selected', 'true');

  // Jump to last column: Ctrl+B then '0' focuses pane 3
  await page.keyboard.press('Control+b');
  await page.keyboard.press('0');
  await expect(page.locator('.pane-tab[data-pane-id="3"]')).toHaveAttribute('aria-selected', 'true');

  // 3. Layout toggle: Ctrl+B then 'c' toggles between cards and scroll
  await expect(page.locator('.pane-strip')).toHaveClass(/cards/);
  await page.keyboard.press('Control+b');
  await page.keyboard.press('c');
  await expect(page.locator('.pane-strip')).not.toHaveClass(/cards/);
  await page.keyboard.press('Control+b');
  await page.keyboard.press('c');
  await expect(page.locator('.pane-strip')).toHaveClass(/cards/);

  // 4. Help overlay: Ctrl+B then '?' opens help dialog
  await expect(page.locator('.help-dialog')).toHaveCount(0);
  await page.keyboard.press('Control+b');
  await page.keyboard.press('?');
  await expect(page.locator('.help-dialog')).toBeVisible();
  await expect(page.locator('.help-dialog')).toContainText('wideboi Shortcuts');
  await page.keyboard.press('Escape');
  await expect(page.locator('.help-dialog')).toHaveCount(0);

  // 5. Configurable prefix: Change prefix to Ctrl+A
  await page.locator('.toolbar .settings-btn').click();
  await page.locator('#settings-prefix-key').selectOption('ctrl+a');
  await page.locator('.settings-dialog .close-btn').click();
  await expect.poll(() => page.evaluate(() => localStorage.getItem('wideboi.prefix'))).toBe('ctrl+a');
  await page.locator('wideboi-pane canvas').nth(2).focus();
  await page.keyboard.press('Control+a');
  await page.keyboard.press('1');
  await expect(page.locator('.pane-tab[data-pane-id="1"]')).toHaveAttribute('aria-selected', 'true');
});

test('toolbar pane selector tabs display title, status glyphs, and focus state', async ({ page }) => {
  await installMockWebSocket(page);
  await page.goto('/');
  await page.getByRole('button', { name: 'Connect' }).click();
  await page.evaluate(() => window.testSockets[0].open());
  await expect(page.locator('.toolbar')).toBeVisible();

  // Verify standalone title and static status elements are gone
  await expect(page.locator('.terminal-shell > .title')).toHaveCount(0);
  await expect(page.locator('.terminal-shell > .status')).toHaveCount(0);

  // Send layout with 4 panes with different statuses
  // PaneStatus: 0 = IDLE, 1 = WORKING, 2 = NEEDS_INPUT, 3 = DONE, 4 = FAILED
  await page.evaluate(async () => {
    const { serverBytes } = await import('/tests/browser-fixture.ts');
    const socket = window.testSockets[0];
    socket.message(serverBytes({ case: 'layoutSnapshot', value: {
      columns: [
        { paneId: 1, width: 40, height: 10 },
        { paneId: 2, width: 40, height: 10 },
        { paneId: 3, width: 40, height: 10 },
        { paneId: 4, width: 40, height: 10 },
      ],
      paneTitles: { 1: 'editor', 2: 'build', 3: 'prompt', 4: 'lint' },
      paneStatuses: { 1: 0, 2: 1, 3: 2, 4: 4 },
    } }));
  });

  const tab1 = page.locator('.pane-tab[data-pane-id="1"]');
  const tab2 = page.locator('.pane-tab[data-pane-id="2"]');
  const tab3 = page.locator('.pane-tab[data-pane-id="3"]');
  const tab4 = page.locator('.pane-tab[data-pane-id="4"]');

  await expect(tab1).toBeVisible();
  await expect(tab1).toContainText('editor');
  await expect(tab1).toHaveAttribute('aria-selected', 'true');
  await expect(tab1).toHaveAttribute('aria-label', 'Pane 1: editor');

  await expect(tab2).toContainText('build');
  await expect(tab2.locator('.tab-status.working')).toHaveText('»');
  await expect(tab2).toHaveAttribute('aria-selected', 'false');
  await expect(tab2).toHaveAttribute('aria-label', 'Pane 2: build, working');

  await expect(tab3).toContainText('prompt');
  await expect(tab3.locator('.tab-status.needs-input')).toHaveText('!');
  await expect(tab3).toHaveAttribute('aria-label', 'Pane 3: prompt, needs input');

  await expect(tab4).toContainText('lint');
  await expect(tab4.locator('.tab-status.failed')).toHaveText('✗');
  await expect(tab4).toHaveAttribute('aria-label', 'Pane 4: lint, failed');

  // Click tab 3 to focus
  await tab3.click();
  await expect(tab3).toHaveAttribute('aria-selected', 'true');
  await expect(tab1).toHaveAttribute('aria-selected', 'false');

  // Tablist keyboard navigation
  await tab3.focus();
  await page.keyboard.press('ArrowRight');
  await expect(tab4).toBeFocused();
  await expect(tab4).toHaveAttribute('aria-selected', 'true');

  await page.keyboard.press('Home');
  await expect(tab1).toBeFocused();
  await expect(tab1).toHaveAttribute('aria-selected', 'true');

  await page.keyboard.press('ArrowLeft');
  await expect(tab4).toBeFocused();
  await expect(tab4).toHaveAttribute('aria-selected', 'true');
});
