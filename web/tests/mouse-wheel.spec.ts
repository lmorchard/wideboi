import { test, expect } from '@playwright/test';
import { installMockWebSocket } from './browser-fixture';

test.describe('Mouse Wheel Forwarding', () => {
  test('forwards wheel events to mouse tracking panes as MouseKind.WHEEL', async ({ page }) => {
    await installMockWebSocket(page);
    await page.setViewportSize({ width: 800, height: 600 });
    await page.goto('/');
    await page.getByRole('button', { name: 'Connect' }).click();
    await page.evaluate(() => window.testSockets[0].open());
    await expect(page.locator('.toolbar')).toBeVisible();

    // Set up pane 1 with mouseTracking: true
    await page.evaluate(async () => {
      const { serverBytes } = await import('/tests/browser-fixture.ts');
      window.testSockets[0].message(serverBytes({
        case: 'layoutSnapshot',
        value: {
          columns: [{ paneId: 1, width: 80, height: 24 }],
        },
      }));

      const emptyRow = () => ({ cells: Array.from({ length: 80 }, () => ({ content: ' ', width: 1 })) });
      const lines = Array.from({ length: 24 }, emptyRow);

      window.testSockets[0].message(serverBytes({
        case: 'paneUpdate',
        value: {
          paneId: 1,
          cols: 80,
          rows: 24,
          generation: 1n,
          scrollOffset: 0,
          scrollbackLen: 0,
          mouseTracking: true,
          lines,
        },
      }));
    });

    await expect(page.locator('wideboi-pane')).toHaveCount(1);
    const canvas = page.locator('wideboi-pane canvas');
    await canvas.hover();

    // Clear initial messages
    await page.evaluate(() => { window.testSockets[0].sent = []; });

    // Wheel up -> button 4 (MouseWheelUp)
    await page.mouse.wheel(0, -100);

    await expect.poll(async () => page.evaluate(async () => {
      const { clientMessages } = await import('/tests/browser-fixture.ts');
      const msgs = clientMessages(window.testSockets[0].sent);
      return msgs.find(m => m.case === 'mouse');
    })).toMatchObject({
      case: 'mouse',
      value: {
        paneId: 1,
        kind: 3, // MouseKind.WHEEL
        button: 4, // MouseWheelUp
      },
    });

    // Clear and wheel down -> button 5 (MouseWheelDown)
    await page.evaluate(() => { window.testSockets[0].sent = []; });
    await page.mouse.wheel(0, 100);

    await expect.poll(async () => page.evaluate(async () => {
      const { clientMessages } = await import('/tests/browser-fixture.ts');
      const msgs = clientMessages(window.testSockets[0].sent);
      return msgs.find(m => m.case === 'mouse');
    })).toMatchObject({
      case: 'mouse',
      value: {
        paneId: 1,
        kind: 3, // MouseKind.WHEEL
        button: 5, // MouseWheelDown
      },
    });

    // Alt+wheel bypasses mouse tracking and sends scroll (history)
    await page.evaluate(() => { window.testSockets[0].sent = []; });
    await page.keyboard.down('Alt');
    await page.mouse.wheel(0, -100);
    await page.keyboard.up('Alt');

    await expect.poll(async () => page.evaluate(async () => {
      const { clientMessages } = await import('/tests/browser-fixture.ts');
      const msgs = clientMessages(window.testSockets[0].sent);
      return msgs.find(m => m.case === 'scroll');
    })).toMatchObject({
      case: 'scroll',
      value: {
        paneId: 1,
        delta: 3,
      },
    });
  });

  test('sends scroll message when mouse tracking is disabled', async ({ page }) => {
    await installMockWebSocket(page);
    await page.setViewportSize({ width: 800, height: 600 });
    await page.goto('/');
    await page.getByRole('button', { name: 'Connect' }).click();
    await page.evaluate(() => window.testSockets[0].open());
    await expect(page.locator('.toolbar')).toBeVisible();

    // Set up pane 1 with mouseTracking: false
    await page.evaluate(async () => {
      const { serverBytes } = await import('/tests/browser-fixture.ts');
      window.testSockets[0].message(serverBytes({
        case: 'layoutSnapshot',
        value: {
          columns: [{ paneId: 1, width: 80, height: 24 }],
        },
      }));

      const emptyRow = () => ({ cells: Array.from({ length: 80 }, () => ({ content: ' ', width: 1 })) });
      const lines = Array.from({ length: 24 }, emptyRow);

      window.testSockets[0].message(serverBytes({
        case: 'paneUpdate',
        value: {
          paneId: 1,
          cols: 80,
          rows: 24,
          generation: 1n,
          scrollOffset: 0,
          scrollbackLen: 50,
          mouseTracking: false,
          lines,
        },
      }));
    });

    await expect(page.locator('wideboi-pane')).toHaveCount(1);
    const canvas = page.locator('wideboi-pane canvas');
    await canvas.hover();

    await page.evaluate(() => { window.testSockets[0].sent = []; });

    // Wheel up -> scroll delta 3
    await page.mouse.wheel(0, -100);

    await expect.poll(async () => page.evaluate(async () => {
      const { clientMessages } = await import('/tests/browser-fixture.ts');
      const msgs = clientMessages(window.testSockets[0].sent);
      return msgs.find(m => m.case === 'scroll');
    })).toMatchObject({
      case: 'scroll',
      value: {
        paneId: 1,
        delta: 3,
      },
    });
  });
});
