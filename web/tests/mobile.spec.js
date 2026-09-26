import { test, expect } from '@playwright/test';

test.use({ viewport: { width: 390, height: 700 }, hasTouch: true, isMobile: true });

async function connect(page) {
  await page.addInitScript(() => {
    window.testSockets = [];
    window.WebSocket = class {
      static OPEN = 1;
      constructor(_url, protocols) {
        this.protocol = 'wideboi.v14';
        this.readyState = 0;
        this.sent = [];
        if (protocols?.includes('wideboi.v14')) window.testSockets.push(this);
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

test('narrow view shows one pane and sends draft text followed by Enter', async ({ page }) => {
  await connect(page);
  await expect(page.getByRole('combobox', { name: 'Mobile pane' })).toBeVisible();
  await page.setViewportSize({ width: 320, height: 700 });
  await expect(page.getByRole('button', { name: 'Send text' })).toBeInViewport();
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
  expect(inputs).toHaveLength(2);
  expect(inputs[0].value.paneId).toBe(2);
  expect(new TextDecoder().decode(inputs[0].value.data)).toBe('echo hello');
  expect(inputs[1].value.paneId).toBe(2);
  expect(inputs[1].value.key.code).toBe(13);
});

test('sending a mobile draft reveals the cursor after horizontal panning', async ({ page }) => {
  await connect(page);
  await page.getByRole('button', { name: 'Reset zoom' }).click();
  const viewport = page.locator('wideboi-pane').first().locator('.viewport');
  await viewport.evaluate(el => { el.scrollLeft = el.scrollWidth; });
  await expect.poll(() => viewport.evaluate(el => el.scrollLeft)).toBeGreaterThan(0);
  await page.getByRole('textbox', { name: 'Command or response' }).fill('x');
  await page.getByRole('button', { name: 'Send text' }).click();
  await expect.poll(() => viewport.evaluate(el => el.scrollLeft)).toBe(0);
});

test('touch pans without terminal mouse messages and keyboard-sized view does not resize PTY', async ({ page }) => {
  await connect(page);
  await page.getByRole('button', { name: 'Reset zoom' }).click();
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
  await viewport.evaluate(el => {
    el.scrollTop = 0;
    el.dispatchEvent(new Event('scroll'));
  });
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
  await page.getByRole('button', { name: 'Macros panel' }).click();
  const sheet = page.getByRole('region', { name: 'Macros list' });
  await expect(sheet).toBeVisible();
  await page.getByRole('button', { name: 'Escape key' }).click();
  await expect(sheet).toBeVisible();
  await page.getByRole('button', { name: 'Control modifier' }).click();
  await expect(sheet).toBeVisible();
  await page.getByRole('button', { name: 'C key' }).click();
  await expect(sheet).toBeVisible();
  const inputs = (await sent(page)).filter(msg => msg.case === 'input');
  expect(inputs.map(msg => msg.value.key.code)).toEqual([27, 99]);
  expect(inputs[1].value.key.mod & 4).toBe(4);
  await expect(page.getByRole('button', { name: 'Control modifier' })).toHaveAttribute('aria-pressed', 'false');
});

test('Ctrl+R shortcut can be sent from on-screen controls', async ({ page }) => {
  await connect(page);
  await page.getByRole('button', { name: 'Macros panel' }).click();
  const sheet = page.getByRole('region', { name: 'Macros list' });
  await expect(sheet).toBeVisible();
  await page.getByRole('button', { name: 'Control modifier' }).click();
  await expect(page.getByRole('button', { name: 'R key', exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'R key', exact: true }).click();
  await expect(sheet).toBeVisible();
  const inputs = (await sent(page)).filter(msg => msg.case === 'input');
  expect(inputs).toHaveLength(1);
  expect(inputs[0].value.key.code).toBe(114);
  expect(inputs[0].value.key.mod & 4).toBe(4);
  await expect(page.getByRole('button', { name: 'Control modifier' })).toHaveAttribute('aria-pressed', 'false');
});

test('direct input mode forwards keystrokes immediately to the terminal', async ({ page }) => {
  await connect(page);
  await page.getByRole('button', { name: 'Direct input mode' }).click();
  const directInput = page.getByRole('textbox', { name: 'Direct terminal input' });
  await expect(directInput).toBeVisible();
  await directInput.press('q');
  const inputs = (await sent(page)).filter(msg => msg.case === 'input');
  expect(inputs).toHaveLength(1);
  expect(inputs[0].value.key.code).toBe(113);
  await page.getByRole('button', { name: 'Draft mode' }).click();
  await expect(page.getByRole('textbox', { name: 'Command or response' })).toBeVisible();
});

test('macro panel opens, shows macros with Enter indicators, and executes ordered steps without implicit Enter', async ({ page }) => {
  await connect(page);
  await page.getByRole('button', { name: 'Macros panel' }).click();
  const sheet = page.getByRole('region', { name: 'Macros list' });
  await expect(sheet).toBeVisible();

  const gitStatusBtn = page.getByRole('button', { name: 'Run macro Git Status' });
  await expect(gitStatusBtn).toBeVisible();
  await expect(gitStatusBtn.locator('.mobile-macro-enter')).toHaveText('↵');

  const historySearchBtn = page.getByRole('button', { name: 'Run macro History Search' });
  await expect(historySearchBtn).toBeVisible();
  await expect(historySearchBtn.locator('.mobile-macro-enter')).toHaveCount(0);

  await gitStatusBtn.click();
  await expect(sheet).toBeHidden();

  const inputs = (await sent(page)).filter(msg => msg.case === 'input');
  expect(inputs).toHaveLength(2);
  expect(inputs[0].value.paneId).toBe(1);
  expect(new TextDecoder().decode(inputs[0].value.data)).toBe('git status');
  expect(inputs[1].value.paneId).toBe(1);
  expect(inputs[1].value.key.code).toBe(13);
});

test('macro editor allows editing and saving macros to server', async ({ page }) => {
  await connect(page);
  await page.getByRole('button', { name: 'Macros panel' }).click();
  const editBtn = page.getByRole('button', { name: 'Edit macros' });
  const closeBtn = page.getByRole('button', { name: 'Close macros' });
  expect(await editBtn.evaluate(el => el.getBoundingClientRect().height)).toBeGreaterThanOrEqual(40);
  expect(await closeBtn.evaluate(el => el.getBoundingClientRect().height)).toBeGreaterThanOrEqual(40);
  await editBtn.click();

  const dialog = page.getByRole('dialog', { name: 'Configure Macros' });
  await expect(dialog).toBeVisible();

  await dialog.locator('input[name="macroName"]').fill('Test Echo');
  await dialog.locator('input[name="macroVal"]').fill('echo ok');
  await dialog.getByRole('button', { name: 'Add Macro' }).click();

  await expect(dialog.getByText('Test Echo ↵')).toBeVisible();

  await dialog.getByRole('button', { name: 'Save to Server' }).click();
  await expect(dialog).toBeHidden();

  const saveMsgs = (await sent(page)).filter(msg => msg.case === 'saveMacros');
  expect(saveMsgs).toHaveLength(1);
  expect(saveMsgs[0].value.macros.some(m => m.name === 'Test Echo')).toBe(true);
});

test('macro editor allows building multi-step sequences', async ({ page }) => {
  await connect(page);
  await page.getByRole('button', { name: 'Macros panel' }).click();
  await page.getByRole('button', { name: 'Edit macros' }).click();

  const dialog = page.getByRole('dialog', { name: 'Configure Macros' });
  await expect(dialog).toBeVisible();

  await dialog.locator('input[name="macroName"]').fill('Vim Force Quit');

  await dialog.locator('select[name="macroType"]').selectOption('key');
  await dialog.locator('input[name="macroVal"]').fill('Escape');
  await dialog.getByRole('button', { name: 'Add Step to Sequence' }).click();

  await dialog.locator('select[name="macroType"]').selectOption('text');
  await dialog.locator('input[name="macroVal"]').fill(':q!');
  await dialog.getByRole('button', { name: 'Add Step to Sequence' }).click();

  await expect(dialog.getByText('1. Key: Escape')).toBeVisible();
  await expect(dialog.getByText('2. Text: ":q!"')).toBeVisible();

  await dialog.getByRole('button', { name: 'Add Macro' }).click();
  await expect(dialog.getByText('Vim Force Quit ↵')).toBeVisible();

  await dialog.getByRole('button', { name: 'Save to Server' }).click();
  await expect(dialog).toBeHidden();

  const saveMsgs = (await sent(page)).filter(msg => msg.case === 'saveMacros');
  const vimMacro = saveMsgs[0]?.value.macros.find(m => m.name === 'Vim Force Quit');
  expect(vimMacro).toBeDefined();
  expect(vimMacro.steps).toHaveLength(3);
  expect(vimMacro.steps[0].key).toBe('Escape');
  expect(vimMacro.steps[1].text).toBe(':q!');
  expect(vimMacro.steps[2].key).toBe('Enter');
});

test('mobile bar zoom controls scale terminal view per-pane without resizing PTY', async ({ page }) => {
  await connect(page);
  const zoomOut = page.getByRole('button', { name: 'Zoom out' });
  const zoomReset = page.getByRole('button', { name: 'Reset zoom' });
  const zoomIn = page.getByRole('button', { name: 'Zoom in' });

  await expect(zoomOut).toBeVisible();
  // On mobile, fresh terminals start zoomed all the way out by default
  await expect(zoomOut).toBeDisabled();
  const minZoomText = await zoomReset.innerText();
  const minZoomValue = parseFloat(minZoomText) / 100;
  await expect(zoomIn).toBeVisible();

  const pane1 = page.locator('wideboi-pane').first();
  const canvas1 = pane1.locator('canvas');
  const baseWidth = (await canvas1.evaluate(el => parseFloat(el.style.width))) / minZoomValue;

  const resizeBefore = (await sent(page)).filter(msg => msg.case === 'resize').length;

  // Clicking Reset toggles from minZoom to 100%
  await zoomReset.click();
  await expect(zoomReset).toHaveText('100%');
  await expect.poll(() => canvas1.evaluate(el => parseFloat(el.style.width))).toBeCloseTo(baseWidth, 0);
  await expect(zoomOut).toBeEnabled();

  // Zoom in to 1.25x
  await zoomIn.click();
  await expect(zoomReset).toHaveText('125%');
  await expect.poll(() => canvas1.evaluate(el => parseFloat(el.style.width))).toBeCloseTo(baseWidth * 1.25, 0);

  // Zoom in to 2.0x
  await zoomIn.click(); // 1.5x
  await zoomIn.click(); // 1.75x
  await zoomIn.click(); // 2.0x
  await expect(zoomReset).toHaveText('200%');
  await expect(zoomIn).toBeDisabled();

  // Reset zoom resets to minZoom (max zoomed out)
  await zoomReset.click();
  await expect(zoomReset).toHaveText(minZoomText);
  await expect(zoomOut).toBeDisabled();
  await expect.poll(() => canvas1.evaluate(el => parseFloat(el.style.width))).toBeCloseTo(baseWidth * minZoomValue, 0);

  // Clicking Reset zoom from minZoom toggles to 100%
  await zoomReset.click();
  await expect(zoomReset).toHaveText('100%');
  await expect.poll(() => canvas1.evaluate(el => parseFloat(el.style.width))).toBeCloseTo(baseWidth, 0);

  // Per-pane isolation: switch to next pane
  await page.getByRole('button', { name: 'Next pane' }).click();
  const pane2 = page.locator('wideboi-pane').nth(1);
  const canvas2 = pane2.locator('canvas');
  await expect(zoomReset).toHaveText(minZoomText);
  await expect(zoomOut).toBeDisabled();

  // Toggle pane 2 to 100%
  await zoomReset.click();
  await expect(zoomReset).toHaveText('100%');

  // Switch back to first pane: zoom is still 100%
  await page.getByRole('button', { name: 'Previous pane' }).click();
  await expect(zoomReset).toHaveText('100%');
  await expect(zoomOut).toBeEnabled();

  // Verify no MsgResize was sent
  const resizeAfter = (await sent(page)).filter(msg => msg.case === 'resize').length;
  expect(resizeAfter).toBe(resizeBefore);
});

test('two-finger pinch on viewport zooms terminal view without resizing PTY', async ({ page }) => {
  await connect(page);
  await page.getByRole('button', { name: 'Reset zoom' }).click();
  const pane = page.locator('wideboi-pane').first();
  const viewport = pane.locator('.viewport');
  const canvas = pane.locator('canvas');
  const initialWidth = await canvas.evaluate(el => parseFloat(el.style.width));
  const zoomReset = page.getByRole('button', { name: 'Reset zoom' });

  const beforeResize = (await sent(page)).filter(msg => msg.case === 'resize').length;

  const bounds = await viewport.boundingBox();
  const cdp = await page.context().newCDPSession(page);
  const midX = bounds.x + bounds.width / 2;
  const midY = bounds.y + bounds.height / 2;

  // Start with 2 touches 40px apart
  await cdp.send('Input.dispatchTouchEvent', {
    type: 'touchStart',
    touchPoints: [
      { x: midX - 20, y: midY, id: 1 },
      { x: midX + 20, y: midY, id: 2 },
    ],
  });

  // Spread fingers to 60px apart (scale 1.5x)
  await cdp.send('Input.dispatchTouchEvent', {
    type: 'touchMove',
    touchPoints: [
      { x: midX - 30, y: midY, id: 1 },
      { x: midX + 30, y: midY, id: 2 },
    ],
  });
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });

  await expect(zoomReset).toHaveText('150%');
  await expect.poll(() => canvas.evaluate(el => parseFloat(el.style.width))).toBeCloseTo(initialWidth * 1.5, 0);

  const afterResize = (await sent(page)).filter(msg => msg.case === 'resize').length;
  expect(afterResize).toBe(beforeResize);
});

test('zoomed viewport supports pan reachability and preserves position across keyboard resize', async ({ page }) => {
  await connect(page);
  await page.getByRole('button', { name: 'Reset zoom' }).click();
  const pane = page.locator('wideboi-pane').first();
  const viewport = pane.locator('.viewport');
  const canvas = pane.locator('canvas');
  const zoomIn = page.getByRole('button', { name: 'Zoom in' });
  const beforeResize = (await sent(page)).filter(msg => msg.case === 'resize').length;

  // Zoom in to 1.5x
  await zoomIn.click();
  await zoomIn.click();

  const maxScrollLeft = await viewport.evaluate(el => el.scrollWidth - el.clientWidth);
  const maxScrollTop = await viewport.evaluate(el => el.scrollHeight - el.clientHeight);
  expect(maxScrollLeft).toBeGreaterThan(0);
  expect(maxScrollTop).toBeGreaterThan(0);

  // Pan horizontally to rightmost
  await viewport.evaluate(el => { el.scrollLeft = el.scrollWidth; });
  await expect.poll(() => viewport.evaluate(el => el.scrollLeft)).toBeCloseTo(maxScrollLeft, 0);

  // Verify follow-bottom across keyboard resize
  await viewport.evaluate(el => { el.scrollTop = el.scrollHeight; });
  await page.evaluate(() => {
    Object.defineProperty(window.visualViewport, 'height', { configurable: true, value: 420 });
    window.visualViewport.dispatchEvent(new Event('resize'));
  });
  await expect.poll(() => viewport.evaluate(el =>
    Math.abs(el.scrollHeight - el.clientHeight - el.scrollTop))).toBeLessThan(2);

  // Deliberately scroll to top and verify position maintained across keyboard resize
  await viewport.evaluate(el => {
    el.scrollTop = 0;
    el.dispatchEvent(new Event('scroll'));
  });
  await expect.poll(() => viewport.evaluate(el => el.scrollTop)).toBe(0);
  await page.evaluate(() => {
    Object.defineProperty(window.visualViewport, 'height', { configurable: true, value: 360 });
    window.visualViewport.dispatchEvent(new Event('resize'));
  });
  await expect.poll(() => viewport.evaluate(el => el.scrollTop)).toBe(0);

  // Confirm zero MsgResize messages throughout
  const afterResize = (await sent(page)).filter(msg => msg.case === 'resize').length;
  expect(afterResize).toBe(beforeResize);
});

test('button zoom anchors viewport center when scrolled up and preserves bottom when at live output', async ({ page }) => {
  await connect(page);
  await page.setViewportSize({ width: 320, height: 350 });
  await page.getByRole('button', { name: 'Reset zoom' }).click();
  const pane = page.locator('wideboi-pane').first();
  const viewport = pane.locator('.viewport');
  const zoomIn = page.getByRole('button', { name: 'Zoom in' });
  const zoomReset = page.getByRole('button', { name: 'Reset zoom' });

  // 1. When at live bottom: zooming in maintains follow-bottom at the new scrollHeight
  await viewport.evaluate(el => { el.scrollTop = el.scrollHeight; });
  await zoomIn.click();
  await expect.poll(() => viewport.evaluate(el =>
    Math.abs(el.scrollHeight - el.clientHeight - el.scrollTop))).toBeLessThan(2);
  await zoomReset.click(); // resets to minZoom

  // 2. When scrolled up deliberately: zooming anchors visible center
  await zoomReset.click(); // toggle to 100%
  await viewport.evaluate(el => {
    el.scrollTop = 50;
    el.scrollLeft = 50;
    el.dispatchEvent(new Event('scroll'));
  });

  const before = await viewport.evaluate(el => ({
    centerX: el.scrollLeft + el.clientWidth / 2,
    centerY: el.scrollTop + el.clientHeight / 2,
    clientWidth: el.clientWidth,
    clientHeight: el.clientHeight,
  }));

  // Zoom in from 1.0 to 1.25x
  await zoomIn.click();

  const expectedScrollLeft = Math.max(0, before.centerX * 1.25 - before.clientWidth / 2);
  const expectedScrollTop = Math.max(0, before.centerY * 1.25 - before.clientHeight / 2);

  await expect.poll(async () =>
    Math.abs(await viewport.evaluate(el => el.scrollLeft) - expectedScrollLeft)).toBeLessThanOrEqual(1);
  await expect.poll(async () =>
    Math.abs(await viewport.evaluate(el => el.scrollTop) - expectedScrollTop)).toBeLessThanOrEqual(1);
});

test('fresh terminal starts zoomed all the way out with no vertical scroll', async ({ page }) => {
  await connect(page);
  const pane = page.locator('wideboi-pane').first();
  const viewport = pane.locator('.viewport');
  const zoomOut = page.getByRole('button', { name: 'Zoom out' });

  // Starts zoomed all the way out: zoom out button is disabled
  await expect(zoomOut).toBeDisabled();

  // Zero vertical scroll: content fits cleanly in viewport
  await expect.poll(async () => {
    return viewport.evaluate(el => ({
      scrollTop: el.scrollTop,
      scrollDiff: el.scrollHeight - el.clientHeight,
    }));
  }).toEqual({
    scrollTop: 0,
    scrollDiff: 0,
  });
});
