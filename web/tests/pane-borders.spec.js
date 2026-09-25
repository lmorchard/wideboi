import { test, expect } from '@playwright/test';

test('terminal canvas does not paint over pane borders in cards and scroll layouts', async ({ page }) => {
  await page.addInitScript(() => {
    window.testSockets = [];
    window.WebSocket = class {
      static OPEN = 1;
      constructor(url, protocols) {
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
  await page.setViewportSize({ width: 500, height: 400 });
  await page.goto('/');
  await page.getByRole('button', { name: 'Connect' }).click();
  await page.evaluate(() => window.testSockets[0].open());
  await expect.poll(() => page.evaluate(() => window.testSockets[0].sent.length)).toBeGreaterThan(0);

  // Send layout snapshot with 2 columns
  await page.evaluate(async () => {
    const { serverBytes } = await import('/tests/browser-fixture.ts');
    window.testSockets[0].message(serverBytes({ case: 'layoutSnapshot', value: {
      columns: [
        { paneId: 1, width: 40, height: 20 },
        { paneId: 2, width: 40, height: 20 },
      ],
    } }));
    window.testSockets[0].message(serverBytes({ case: 'paneUpdate', value: {
      paneId: 1, generation: 1n, cols: 40, rows: 20,
      lines: Array.from({ length: 20 }, () => ({ cells: Array.from({ length: 40 }, () => ({ content: ' ', width: 1 })) })),
    } }));
    // Pane 2 has cell background painted at column 0 to verify border stays visible
    window.testSockets[0].message(serverBytes({ case: 'paneUpdate', value: {
      paneId: 2, generation: 1n, cols: 40, rows: 20,
      lines: Array.from({ length: 20 }, (_, y) => ({
        cells: Array.from({ length: 40 }, (_, x) => ({
          content: ' ',
          width: 1,
          style: x === 0 ? { bg: { kind: 2, r: 0, g: 0, b: 0, a: 255 } } : undefined,
        })),
      })),
    } }));
  });

  const panes = page.locator('wideboi-pane');
  await expect(panes).toHaveCount(2);

  // Helper to sample pixels from a screenshot
  const samplePixels = async () => {
    const screenshot = await page.screenshot();
    const base64 = screenshot.toString('base64');
    return page.evaluate(async (b64) => {
      const img = new Image();
      img.src = 'data:image/png;base64,' + b64;
      await img.decode();
      const c = document.createElement('canvas');
      c.width = img.width;
      c.height = img.height;
      const ctx = c.getContext('2d');
      ctx.drawImage(img, 0, 0);

      const app = document.querySelector('wideboi-app');
      const ps = app.shadowRoot.querySelectorAll('wideboi-pane');
      const p1Rect = ps[0].getBoundingClientRect();
      const p2Rect = ps[1].getBoundingClientRect();

      const getPixel = (x, y) => Array.from(ctx.getImageData(Math.round(x), Math.round(y), 1, 1).data);

      return {
        // Focused card 1: top border (y = p1Rect.top + 1), right border (x = p1Rect.right - 1)
        card1Top: getPixel(p1Rect.left + 50, p1Rect.top + 1),
        card1Right: getPixel(p1Rect.right - 1, p1Rect.top + 100),
        // Unfocused card 2: left border (x = p2Rect.left + 1), top border (y = p2Rect.top + 1)
        card2Left: getPixel(p2Rect.left + 1, p2Rect.top + 100),
        card2Top: getPixel(p2Rect.left + 50, p2Rect.top + 1),
        // Scroll layout divider between pane 1 and pane 2 (p1 has border-right)
        scrollP1Top: getPixel(p1Rect.left + 50, p1Rect.top + 1),
        scrollDivider: getPixel(p1Rect.right - 2, p1Rect.top + 100),
      };
    }, base64);
  };

  // 1. Cards layout (default)
  await expect(page.getByRole('combobox', { name: 'Layout' })).toHaveValue('cards');
  await expect.poll(async () => {
    const p = await samplePixels();
    return {
      card1TopBlue: p.card1Top[0] === 14 && p.card1Top[1] === 154 && p.card1Top[2] === 255,
      card1RightBlue: p.card1Right[0] === 14 && p.card1Right[1] === 154 && p.card1Right[2] === 255,
      card2LeftGray: p.card2Left[0] === 184 && p.card2Left[1] === 184 && p.card2Left[2] === 184,
      card2TopGray: p.card2Top[0] === 184 && p.card2Top[1] === 184 && p.card2Top[2] === 184,
    };
  }).toEqual({
    card1TopBlue: true,
    card1RightBlue: true,
    card2LeftGray: true,
    card2TopGray: true,
  });

  // 2. Scroll layout
  await page.getByRole('combobox', { name: 'Layout' }).selectOption('scroll');
  await expect.poll(async () => {
    const p = await samplePixels();
    return {
      // Focused pane 1 has blue top accent line (#007fd4: 0, 127, 212)
      topBlue: p.scrollP1Top[0] === 0 && p.scrollP1Top[1] === 127 && p.scrollP1Top[2] === 212,
      // Divider between pane 1 and pane 2 is blue (focused pane border-right-color #007fd4)
      dividerBlue: p.scrollDivider[0] === 0 && p.scrollDivider[1] === 127 && p.scrollDivider[2] === 212,
    };
  }).toEqual({
    topBlue: true,
    dividerBlue: true,
  });
});
