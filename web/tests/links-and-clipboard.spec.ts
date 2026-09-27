import { test, expect, type Page } from '@playwright/test';
import { installMockWebSocket } from './browser-fixture';

async function setupPageWithUrls(page: Page) {
  await page.setViewportSize({ width: 700, height: 400 });
  await page.goto('/');
  await page.getByRole('button', { name: 'Connect' }).click();
  await page.evaluate(() => window.testSockets[0].open());
  await expect(page.locator('.toolbar')).toBeVisible();

  // Send layout snapshot with pane 1
  await page.evaluate(async () => {
    const { serverBytes } = await import('/tests/browser-fixture.ts');
    window.testSockets[0].message(serverBytes({
      case: 'layoutSnapshot',
      value: {
        columns: [{ paneId: 1, width: 80, height: 24 }],
      },
    }));

    const textRow22 = 'Welcome to https://example.com/docs for docs'.split('').map(c => ({ content: c, width: 1 }));
    const textRow23 = 'Next line with no urls here'.split('').map(c => ({ content: c, width: 1 }));
    const emptyRow = () => ({ cells: Array.from({ length: 80 }, () => ({ content: ' ', width: 1 })) });

    const lines = Array.from({ length: 22 }, emptyRow);
    lines.push({ cells: textRow22 });
    lines.push({ cells: textRow23 });

    window.testSockets[0].message(serverBytes({
      case: 'paneUpdate',
      value: {
        paneId: 1,
        cols: 80,
        rows: 24,
        generation: 1n,
        scrollOffset: 0,
        scrollbackLen: 0,
        lines,
      },
    }));
  });
  await expect(page.locator('wideboi-pane')).toHaveCount(1);
}

test('clicking inside a focused pane does not overwrite system clipboard', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  await installMockWebSocket(page);
  await setupPageWithUrls(page);

  // Set initial clipboard content
  await page.evaluate(() => navigator.clipboard.writeText('keep-my-clipboard-intact'));
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe('keep-my-clipboard-intact');

  const canvas = page.locator('wideboi-pane canvas');

  // Compute exact coordinates for row 23
  const target = await page.evaluate(() => {
    const pane = (document.querySelector('wideboi-app') as any).shadowRoot.querySelector('wideboi-pane');
    const rect = pane.canvas.getBoundingClientRect();
    const cellW = pane.cellWidth * pane.zoom;
    const cellH = 16.8 * pane.zoom;
    return {
      x: rect.left + 5 * cellW,
      y: rect.top + 23 * cellH + cellH / 2,
    };
  });

  // Single click inside focused pane at row 23 (over "Next")
  await page.mouse.click(target.x, target.y);

  // Ensure clipboard text was NOT overwritten with a single character!
  const clipboardText = await page.evaluate(() => navigator.clipboard.readText());
  expect(clipboardText).toBe('keep-my-clipboard-intact');
});

test('hovering over a URL shows pointer cursor and title tooltip', async ({ page }) => {
  await installMockWebSocket(page);
  await setupPageWithUrls(page);

  const canvas = page.locator('wideboi-pane canvas');

  // Compute exact coordinates for row 22, col 15 (inside the URL) and col 2 (outside)
  const coords = await page.evaluate(() => {
    const pane = (document.querySelector('wideboi-app') as any).shadowRoot.querySelector('wideboi-pane');
    const rect = pane.canvas.getBoundingClientRect();
    const cellW = pane.cellWidth * pane.zoom;
    const cellH = 16.8 * pane.zoom;
    return {
      urlX: rect.left + 15 * cellW,
      urlY: rect.top + 22 * cellH + cellH / 2,
      nonUrlX: rect.left + 2 * cellW,
      nonUrlY: rect.top + 22 * cellH + cellH / 2,
    };
  });

  // Move over URL (row 22, col 15)
  await page.mouse.move(coords.urlX, coords.urlY);

  // Check cursor and title on canvas
  await expect(canvas).toHaveCSS('cursor', 'pointer');
  const title = await canvas.getAttribute('title');
  expect(title).toBe('https://example.com/docs');

  // Move to col 2 (over "Welcome" - not a URL)
  await page.mouse.move(coords.nonUrlX, coords.nonUrlY);
  await expect(canvas).not.toHaveCSS('cursor', 'pointer');
  expect(await canvas.getAttribute('title')).toBe('');
});

test('clicking on a URL opens it in a new window', async ({ page }) => {
  await installMockWebSocket(page);
  await setupPageWithUrls(page);

  // Intercept window.open
  await page.evaluate(() => {
    (window as any).openedUrls = [];
    window.open = (url: string | URL | undefined) => {
      (window as any).openedUrls.push(String(url));
      return null;
    };
  });

  const coords = await page.evaluate(() => {
    const pane = (document.querySelector('wideboi-app') as any).shadowRoot.querySelector('wideboi-pane');
    const rect = pane.canvas.getBoundingClientRect();
    const cellW = pane.cellWidth * pane.zoom;
    const cellH = 16.8 * pane.zoom;
    return {
      x: rect.left + 15 * cellW,
      y: rect.top + 22 * cellH + cellH / 2,
    };
  });

  // Click on URL
  await page.mouse.click(coords.x, coords.y);

  const opened = await page.evaluate(() => (window as any).openedUrls);
  expect(opened).toEqual(['https://example.com/docs']);
});

test('drag-select copies text to clipboard and copy event copies active selection', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  await installMockWebSocket(page);
  await setupPageWithUrls(page);

  const coords = await page.evaluate(() => {
    const pane = (document.querySelector('wideboi-app') as any).shadowRoot.querySelector('wideboi-pane');
    const rect = pane.canvas.getBoundingClientRect();
    const cellW = pane.cellWidth * pane.zoom;
    const cellH = 16.8 * pane.zoom;
    return {
      startX: rect.left + 0.5 * cellW,
      endX: rect.left + 6.5 * cellW,
      y: rect.top + 22 * cellH + cellH / 2,
    };
  });

  // Drag select across "Welcome" (cols 0 to 6 on row 22)
  await page.mouse.move(coords.startX, coords.y);
  await page.mouse.down();
  await page.mouse.move(coords.endX, coords.y);
  await page.mouse.up();

  // On release of drag, clipboard should have the dragged text
  const textAfterDrag = await page.evaluate(() => navigator.clipboard.readText());
  expect(textAfterDrag).toBe('Welcome');

  // Verify active selection on pane
  const selectionText = await page.evaluate(() => {
    const pane = (document.querySelector('wideboi-app') as any).shadowRoot.querySelector('wideboi-pane');
    return pane.selectedText();
  });
  expect(selectionText).toBe('Welcome');

  // Overwrite clipboard with dummy text
  await page.evaluate(() => navigator.clipboard.writeText('dummy'));
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe('dummy');

  // Trigger copy event (simulating Cmd+C or browser menu Edit -> Copy)
  await page.evaluate(() => {
    document.dispatchEvent(new Event('copy', { bubbles: true, cancelable: true }));
  });

  // Clipboard should now contain the active selection again
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe('Welcome');
});
