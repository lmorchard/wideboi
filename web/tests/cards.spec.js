import { test, expect } from '@playwright/test';

test('card layout overlaps persistent panes without resizing the terminal', async ({ page }) => {
  await page.addInitScript(() => {
    window.testSockets = [];
    window.WebSocket = class {
      static OPEN = 1;
      constructor(url, protocols) {
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
  await page.setViewportSize({ width: 500, height: 420 });
  await page.goto('/');
  await page.getByRole('button', { name: 'Connect' }).click();
  await page.evaluate(() => window.testSockets[0].open());
  await expect.poll(() => page.evaluate(() => window.testSockets[0].sent.length)).toBeGreaterThan(0);
  await page.evaluate(async () => {
    const { serverBytes } = await import('/tests/browser-fixture.ts');
    window.testSockets[0].message(serverBytes({ case: 'layoutSnapshot', value: {
      columns: [1, 2, 3, 4, 5, 6, 7].map(paneId => ({ paneId, width: 40, height: 20 })),
    } }));
  });
  await expect(page.locator('wideboi-pane')).toHaveCount(7);
  await page.evaluate(() => {
    window.paneOne = document.querySelector('wideboi-app').shadowRoot.querySelector('wideboi-pane');
  });
  const messages = () => page.evaluate(async () => {
    const { clientMessages } = await import('/tests/browser-fixture.ts');
    return clientMessages(window.testSockets[0].sent);
  });
  const settleLayout = () => page.evaluate(() => new Promise(resolve => {
    requestAnimationFrame(() => requestAnimationFrame(resolve));
  }));
  const beforeResize = (await messages()).filter(msg => msg.case === 'resize').length;
  await expect(page.getByRole('combobox', { name: 'Layout' })).toHaveValue('cards');
  const panes = page.locator('wideboi-pane');
  await expect(panes).toHaveCount(7);
  await expect(panes.first()).toHaveAttribute('card-mode', '');
  await expect(panes.nth(1)).toHaveCSS('box-shadow', /rgb\(184, 184, 184\)/);
  const labelPosition = await panes.nth(1).evaluate(pane => ({
    host: pane.getBoundingClientRect().left,
    label: pane.shadowRoot.querySelector('.card-label').getBoundingClientRect().left,
    position: getComputedStyle(pane).position,
  }));
  expect(labelPosition.position).toBe('absolute');
  expect(labelPosition.label).toBeCloseTo(labelPosition.host, 0);
  expect(await page.evaluate(() => window.paneOne === document.querySelector('wideboi-app').shadowRoot.querySelector('wideboi-pane'))).toBe(true);
  await expect(page.locator('.card-count.right')).toContainText('+');
  await settleLayout();
  expect((await messages()).filter(msg => msg.case === 'resize')).toHaveLength(beforeResize);
  await expect.poll(() => panes.nth(2).evaluate(element => element.getBoundingClientRect().left)).toBeLessThan(500);

  const geometry = await panes.evaluateAll(elements => elements.slice(0, 3).map(element => {
    const rect = element.getBoundingClientRect();
    return { left: rect.left, right: rect.right, top: rect.top, width: rect.width, z: Number(getComputedStyle(element).zIndex) };
  }));
  expect(geometry[1].right).toBeGreaterThan(geometry[2].left);
  expect(geometry[2].z).toBeGreaterThan(geometry[1].z);
  await page.mouse.click(geometry[1].left + 10, geometry[1].top + 20);
  await expect(page.getByRole('combobox', { name: 'Focus Pane:' })).toHaveValue('2');
  await page.evaluate(async () => {
    const { serverBytes } = await import('/tests/browser-fixture.ts');
    window.testSockets[0].message(serverBytes({ case: 'paneUpdate', value: {
      paneId: 2, generation: 1n, cols: 40, rows: 20,
      lines: Array.from({ length: 20 }, () => ({ cells: Array.from({ length: 40 }, () => ({ content: ' ', width: 1 })) })),
      mouseTracking: true,
    } }));
  });
  await panes.nth(1).locator('canvas').click({ position: { x: 24, y: 26 } });
  await expect.poll(async () => (await messages()).find(msg => msg.case === 'mouse')?.value)
    .toMatchObject({ paneId: 2, x: 2, y: 1 });
  await page.getByRole('combobox', { name: 'Focus Pane:' }).selectOption('7');
  await expect(panes.nth(6)).toHaveAttribute('focused', '');
  await expect(page.locator('.card-count.left')).toContainText('+');
  await expect(panes.nth(6)).toHaveCSS('visibility', 'visible');
  expect(await page.evaluate(() => window.paneOne === document.querySelector('wideboi-app').shadowRoot.querySelector('wideboi-pane'))).toBe(true);
  await settleLayout();
  expect((await messages()).filter(msg => msg.case === 'resize')).toHaveLength(beforeResize);

  await page.getByRole('combobox', { name: 'Focus Pane:' }).selectOption('5');
  await expect.poll(() => panes.nth(4).evaluate(element => element.getAnimations().length)).toBe(0);
  // Pause both directions and inspect the three adjacent cards mid-slide.
  await page.evaluate(() => {
    window.pausedMoves = [];
    window.originalAnimate = Element.prototype.animate;
    Element.prototype.animate = function (...args) {
      const animation = window.originalAnimate.apply(this, args);
      if (this.matches?.('wideboi-pane')) {
        animation.pause();
        window.pausedMoves.push(animation);
      }
      return animation;
    };
  });
  const zOrder = () => panes.evaluateAll(elements => [3, 4, 5].map(index => Number(getComputedStyle(elements[index]).zIndex)));
  for (const target of ['4', '5']) {
    await page.getByRole('combobox', { name: 'Focus Pane:' }).selectOption(target);
    await expect.poll(() => page.evaluate(() => window.pausedMoves.length)).toBeGreaterThan(0);
    const [left, middle, right] = await zOrder();
    expect(left).toBeLessThan(middle);
    expect(middle).toBeLessThan(right);
    await page.evaluate(() => window.pausedMoves.splice(0).forEach(animation => animation.finish()));
    const focusedIndex = Number(target) - 4;
    await expect.poll(async () => (await zOrder())[focusedIndex]).toBeGreaterThan(right);
  }
  await page.evaluate(() => { Element.prototype.animate = window.originalAnimate; });

  await page.getByRole('combobox', { name: 'Layout' }).selectOption('scroll');
  await expect(panes.first()).not.toHaveAttribute('card-mode', '');
  expect(await page.evaluate(() => window.paneOne === document.querySelector('wideboi-app').shadowRoot.querySelector('wideboi-pane'))).toBe(true);
  await settleLayout();
  expect((await messages()).filter(msg => msg.case === 'resize')).toHaveLength(beforeResize);
  await expect.poll(() => page.locator('.pane-strip').evaluate(element => element.scrollLeft)).toBeGreaterThan(0);
  await page.getByRole('combobox', { name: 'Layout' }).selectOption('cards');
  await expect.poll(() => page.locator('.pane-strip').evaluate(element => element.scrollLeft)).toBe(0);
  await expect.poll(() => panes.nth(6).evaluate(element => element.getAnimations().length)).toBe(0);
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.evaluate(async () => {
    window.cardAnimations = 0;
    const animate = Element.prototype.animate;
    Element.prototype.animate = function (...args) {
      window.cardAnimations++;
      return animate.apply(this, args);
    };
    const { serverBytes } = await import('/tests/browser-fixture.ts');
    window.testSockets[0].message(serverBytes({ case: 'layoutSnapshot', value: {
      columns: [7, 6, 5, 4, 3, 2, 1].map(paneId => ({ paneId, width: 40, height: 20 })),
    } }));
  });
  await expect.poll(() => panes.evaluateAll(elements => elements.map(element => element.paneId)))
    .toEqual([7, 6, 5, 4, 3, 2, 1]);
  expect(await page.evaluate(() => window.cardAnimations)).toBe(0);
  expect(await page.evaluate(() => window.paneOne === [...document.querySelector('wideboi-app').shadowRoot.querySelectorAll('wideboi-pane')].at(-1))).toBe(true);

  await page.emulateMedia({ reducedMotion: 'no-preference' });
  await page.evaluate(async () => {
    window.closeAnimations = 0;
    const animate = Element.prototype.animate;
    Element.prototype.animate = function (...args) {
      if (this.matches?.('wideboi-pane')) window.closeAnimations++;
      return animate.apply(this, args);
    };
    const { serverBytes } = await import('/tests/browser-fixture.ts');
    window.testSockets[0].message(serverBytes({ case: 'paneClosed', value: { paneId: 4 } }));
  });
  await expect(panes).toHaveCount(6);
  await expect.poll(() => page.evaluate(() => window.closeAnimations)).toBeGreaterThan(0);

  await expect.poll(() => panes.evaluateAll(elements => elements.some(element => element.getAnimations().length))).toBe(false);
  await page.evaluate(() => {
    window.pausedMoves = [];
    const animate = Element.prototype.animate;
    Element.prototype.animate = function (...args) {
      const animation = animate.apply(this, args);
      if (this.matches?.('wideboi-pane')) {
        animation.pause();
        window.pausedMoves.push(animation);
      }
      return animation;
    };
  });
  await page.getByRole('combobox', { name: 'Focus Pane:' }).selectOption('6');
  await expect.poll(() => page.evaluate(() => window.pausedMoves.length)).toBeGreaterThan(0);
  await page.evaluate(async () => {
    const { serverBytes } = await import('/tests/browser-fixture.ts');
    window.testSockets[0].message(serverBytes({ case: 'layoutSnapshot', value: {
      columns: [7, 5, 3, 2, 1].map(paneId => ({ paneId, width: 40, height: 20 })),
    } }));
  });
  await expect.poll(() => page.evaluate(() => {
    const app = document.querySelector('wideboi-app');
    return app.stackFocusId === app.focusedPaneId && app.stackFocusId !== null;
  })).toBe(true);
});
