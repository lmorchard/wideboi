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

test('hovering and clicking on explicit OSC 8 hyperlinks', async ({ page }) => {
  await installMockWebSocket(page);
  await page.setViewportSize({ width: 700, height: 400 });
  await page.goto('/');
  await page.getByRole('button', { name: 'Connect' }).click();
  await page.evaluate(() => window.testSockets[0].open());

  await page.evaluate(async () => {
    const { serverBytes } = await import('/tests/browser-fixture.ts');
    window.testSockets[0].message(serverBytes({
      case: 'layoutSnapshot',
      value: { columns: [{ paneId: 1, width: 80, height: 24 }] },
    }));

    const anchorText = 'Explicit Link Here'.split('').map(c => ({ content: c, width: 1, linkId: 1 }));
    const emptyRow = () => ({ cells: Array.from({ length: 80 }, () => ({ content: ' ', width: 1 })) });
    const lines = Array.from({ length: 23 }, emptyRow);
    lines.push({ cells: anchorText });

    window.testSockets[0].message(serverBytes({
      case: 'paneUpdate',
      value: {
        paneId: 1, cols: 80, rows: 24, generation: 1n,
        scrollOffset: 0, scrollbackLen: 0, lines,
        links: ['https://example.com/osc8-target'],
      },
    }));

    (window as any).openedUrls = [];
    window.open = (url: string | URL | undefined) => {
      (window as any).openedUrls.push(String(url));
      return null;
    };
  });

  const canvas = page.locator('wideboi-pane canvas');
  const coords = await page.evaluate(() => {
    const pane = (document.querySelector('wideboi-app') as any).shadowRoot.querySelector('wideboi-pane');
    const rect = pane.canvas.getBoundingClientRect();
    const cellW = pane.cellWidth * pane.zoom;
    const cellH = 16.8 * pane.zoom;
    return {
      x: rect.left + 5 * cellW,
      y: rect.top + 23 * cellH + cellH / 2,
    };
  });

  // Hover over explicit link
  await page.mouse.move(coords.x, coords.y);
  await expect(canvas).toHaveCSS('cursor', 'pointer');
  expect(await canvas.getAttribute('title')).toBe('https://example.com/osc8-target');

  // Click on explicit link
  await page.mouse.click(coords.x, coords.y);
  const opened = await page.evaluate(() => (window as any).openedUrls);
  expect(opened).toEqual(['https://example.com/osc8-target']);
});

test('rejects unsafe javascript: and data: URLs in OSC 8 hyperlinks', async ({ page }) => {
  await installMockWebSocket(page);
  await page.setViewportSize({ width: 700, height: 400 });
  await page.goto('/');
  await page.getByRole('button', { name: 'Connect' }).click();
  await page.evaluate(() => window.testSockets[0].open());

  await page.evaluate(async () => {
    const { serverBytes } = await import('/tests/browser-fixture.ts');
    window.testSockets[0].message(serverBytes({
      case: 'layoutSnapshot',
      value: { columns: [{ paneId: 1, width: 80, height: 24 }] },
    }));

    const anchorText = 'Evil Link'.split('').map(c => ({ content: c, width: 1, linkId: 1 }));
    const emptyRow = () => ({ cells: Array.from({ length: 80 }, () => ({ content: ' ', width: 1 })) });
    const lines = Array.from({ length: 23 }, emptyRow);
    lines.push({ cells: anchorText });

    window.testSockets[0].message(serverBytes({
      case: 'paneUpdate',
      value: {
        paneId: 1, cols: 80, rows: 24, generation: 1n,
        scrollOffset: 0, scrollbackLen: 0, lines,
        links: ['javascript:alert(1)'],
      },
    }));
  });

  const canvas = page.locator('wideboi-pane canvas');
  const coords = await page.evaluate(() => {
    const pane = (document.querySelector('wideboi-app') as any).shadowRoot.querySelector('wideboi-pane');
    const rect = pane.canvas.getBoundingClientRect();
    const cellW = pane.cellWidth * pane.zoom;
    const cellH = 16.8 * pane.zoom;
    return {
      x: rect.left + 3 * cellW,
      y: rect.top + 23 * cellH + cellH / 2,
    };
  });

  await page.mouse.move(coords.x, coords.y);
  await expect(canvas).not.toHaveCSS('cursor', 'pointer');
  expect(await canvas.getAttribute('title')).toBe('');
});

test('soft-wrapped rows copy without intermediate newline', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  await installMockWebSocket(page);
  await page.setViewportSize({ width: 700, height: 400 });
  await page.goto('/');
  await page.getByRole('button', { name: 'Connect' }).click();
  await page.evaluate(() => window.testSockets[0].open());

  await page.evaluate(async () => {
    const { serverBytes } = await import('/tests/browser-fixture.ts');
    window.testSockets[0].message(serverBytes({
      case: 'layoutSnapshot',
      value: { columns: [{ paneId: 1, width: 80, height: 24 }] },
    }));

    const textRow22 = 'Line 1 long text wrapping'.split('').map(c => ({ content: c, width: 1 }));
    const textRow23 = 'continuation text here'.split('').map(c => ({ content: c, width: 1 }));
    const emptyRow = () => ({ cells: Array.from({ length: 80 }, () => ({ content: ' ', width: 1 })) });

    const lines = Array.from({ length: 22 }, emptyRow);
    lines.push({ cells: textRow22, wrapped: true });
    lines.push({ cells: textRow23, wrapped: false });

    window.testSockets[0].message(serverBytes({
      case: 'paneUpdate',
      value: {
        paneId: 1, cols: 80, rows: 24, generation: 1n,
        scrollOffset: 0, scrollbackLen: 0, lines,
      },
    }));
  });

  const coords = await page.evaluate(() => {
    const pane = (document.querySelector('wideboi-app') as any).shadowRoot.querySelector('wideboi-pane');
    const rect = pane.canvas.getBoundingClientRect();
    const cellW = pane.cellWidth * pane.zoom;
    const cellH = 16.8 * pane.zoom;
    return {
      startX: rect.left + 0.5 * cellW,
      startY: rect.top + 22 * cellH + cellH / 2,
      endX: rect.left + 22.5 * cellW,
      endY: rect.top + 23 * cellH + cellH / 2,
    };
  });

  // Drag select from row 22 start to row 23 end
  await page.mouse.move(coords.startX, coords.startY);
  await page.mouse.down();
  await page.mouse.move(coords.endX, coords.endY);
  await page.mouse.up();

  const selected = await page.evaluate(() => {
    const pane = (document.querySelector('wideboi-app') as any).shadowRoot.querySelector('wideboi-pane');
    return pane.selectedText();
  });

  expect(selected).toBe('Line 1 long text wrappingcontinuation text here');
});

test('keyboard shortcut Control+c and Meta+c copy active selection', async ({ page, context }) => {
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

  // Drag select "Welcome"
  await page.mouse.move(coords.startX, coords.y);
  await page.mouse.down();
  await page.mouse.move(coords.endX, coords.y);
  await page.mouse.up();

  // Overwrite clipboard
  await page.evaluate(() => navigator.clipboard.writeText('dummy'));
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe('dummy');

  // Press Control+c
  await page.keyboard.press('Control+c');
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe('Welcome');

  // Select again and test Meta+c (macOS with Apple platform mocked)
  await page.evaluate(() => {
    Object.defineProperty(navigator, 'platform', { value: 'MacIntel', configurable: true });
  });
  await page.mouse.move(coords.startX, coords.y);
  await page.mouse.down();
  await page.mouse.move(coords.endX, coords.y);
  await page.mouse.up();
  await page.evaluate(() => navigator.clipboard.writeText('dummy2'));
  await page.keyboard.press('Meta+c');
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe('Welcome');
});

test('keyboard shortcuts Control+v, Meta+v, and Control+Shift+v paste clipboard text', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  await installMockWebSocket(page);
  await setupPageWithUrls(page);

  // Focus the pane
  await page.locator('wideboi-pane').click();

  // Test Control+v
  await page.evaluate(() => navigator.clipboard.writeText('hello-ctrl-v'));
  await page.evaluate(() => { window.testSockets[0].sent = []; });
  await page.keyboard.press('Control+v');

  await expect.poll(async () => {
    return await page.evaluate(async () => {
      const { clientMessages } = await import('/tests/browser-fixture.ts');
      const msgs = clientMessages(window.testSockets[0].sent);
      const input = msgs.find(m => m.case === 'input' && m.value?.paste);
      if (!input?.value?.data) return '';
      return new TextDecoder().decode(input.value.data);
    });
  }).toBe('hello-ctrl-v');

  // Test Meta+v (macOS Cmd+V with Apple platform mocked)
  await page.evaluate(() => {
    Object.defineProperty(navigator, 'platform', { value: 'MacIntel', configurable: true });
  });
  await page.evaluate(() => navigator.clipboard.writeText('hello-meta-v'));
  await page.evaluate(() => { window.testSockets[0].sent = []; });
  await page.keyboard.press('Meta+v');

  await expect.poll(async () => {
    return await page.evaluate(async () => {
      const { clientMessages } = await import('/tests/browser-fixture.ts');
      const msgs = clientMessages(window.testSockets[0].sent);
      const input = msgs.find(m => m.case === 'input' && m.value?.paste);
      if (!input?.value?.data) return '';
      return new TextDecoder().decode(input.value.data);
    });
  }).toBe('hello-meta-v');

  // Verify non-Apple platform does not hijack Meta+v (Win+V on Windows)
  await page.evaluate(() => {
    Object.defineProperty(navigator, 'platform', { value: 'Win32', configurable: true });
  });
  await page.evaluate(() => { window.testSockets[0].sent = []; });
  await page.keyboard.press('Meta+v');
  await page.waitForTimeout(100);
  const nonAppleSent = await page.evaluate(async () => {
    const { clientMessages } = await import('/tests/browser-fixture.ts');
    return clientMessages(window.testSockets[0].sent).filter(m => m.case === 'input' && m.value?.paste);
  });
  expect(nonAppleSent).toHaveLength(0);

  // Test Control+Shift+v
  await page.evaluate(() => navigator.clipboard.writeText('hello-ctrl-shift-v'));
  await page.evaluate(() => { window.testSockets[0].sent = []; });
  await page.keyboard.press('Control+Shift+v');

  await expect.poll(async () => {
    return await page.evaluate(async () => {
      const { clientMessages } = await import('/tests/browser-fixture.ts');
      const msgs = clientMessages(window.testSockets[0].sent);
      const input = msgs.find(m => m.case === 'input' && m.value?.paste);
      if (!input?.value?.data) return '';
      return new TextDecoder().decode(input.value.data);
    });
  }).toBe('hello-ctrl-shift-v');
});

test('right-click terminal context menu offers Copy, Paste, and Select All', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  await installMockWebSocket(page);
  await setupPageWithUrls(page);

  const pane = page.locator('wideboi-pane');
  const contextMenu = page.locator('wideboi-context-menu .context-menu');

  // Initially context menu is not visible
  await expect(contextMenu).toHaveCount(0);

  // Right-click on pane opens context menu
  await pane.click({ button: 'right' });
  await expect(contextMenu).toBeVisible();

  // "Copy" button is disabled because there is no selection
  const copyBtn = contextMenu.getByRole('menuitem', { name: /Copy/ });
  await expect(copyBtn).toBeDisabled();

  // "Paste" button is enabled
  const pasteBtn = contextMenu.getByRole('menuitem', { name: /Paste/ });
  await expect(pasteBtn).toBeEnabled();

  // Test Paste from context menu
  await page.evaluate(() => navigator.clipboard.writeText('context-menu-pasted'));
  await page.evaluate(() => { window.testSockets[0].sent = []; });
  await pasteBtn.click();
  await expect(contextMenu).toHaveCount(0);

  await expect.poll(async () => {
    return await page.evaluate(async () => {
      const { clientMessages } = await import('/tests/browser-fixture.ts');
      const msgs = clientMessages(window.testSockets[0].sent);
      const input = msgs.find(m => m.case === 'input' && m.value?.paste);
      if (!input?.value?.data) return '';
      return new TextDecoder().decode(input.value.data);
    });
  }).toBe('context-menu-pasted');

  // Right-click again and test "Select All"
  await page.evaluate(() => navigator.clipboard.writeText('prior-clipboard'));
  await pane.click({ button: 'right' });
  await expect(contextMenu).toBeVisible();
  const selectAllBtn = contextMenu.getByRole('menuitem', { name: 'Select All' });
  await selectAllBtn.click();
  await expect(contextMenu).toHaveCount(0);

  // Active selection should now exist and cover the text, while clipboard remains untouched
  const selectedText = await page.evaluate(() => {
    const p = (document.querySelector('wideboi-app') as any).shadowRoot.querySelector('wideboi-pane');
    return p.selectedText();
  });
  expect(selectedText).toContain('Welcome to https://example.com/docs');
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe('prior-clipboard');

  // Right-click now that selection exists: Copy button should be enabled
  await pane.click({ button: 'right' });
  await expect(contextMenu).toBeVisible();
  await expect(contextMenu.getByRole('menuitem', { name: /Copy/ })).toBeEnabled();

  // Click Copy
  await page.evaluate(() => navigator.clipboard.writeText('dummy'));
  await contextMenu.getByRole('menuitem', { name: /Copy/ }).click();
  await expect(contextMenu).toHaveCount(0);
  expect(await page.evaluate(() => navigator.clipboard.readText())).toContain('Welcome to https://example.com/docs');
});

test('native paste into helper textarea works when navigator.clipboard.readText is unavailable', async ({ page }) => {
  await installMockWebSocket(page);
  await setupPageWithUrls(page);

  // Focus the pane
  await page.locator('wideboi-pane').click();

  // Make navigator.clipboard.readText reject (simulating Firefox or insecure HTTP origin)
  await page.evaluate(() => {
    if (navigator.clipboard) {
      (navigator.clipboard as any).readText = () => Promise.reject(new Error('Permission denied'));
    }
  });

  await page.evaluate(() => { window.testSockets[0].sent = []; });

  // Dispatch a native paste event directly to the helper textarea
  await page.evaluate(() => {
    const app = document.querySelector('wideboi-app') as any;
    const pane = app.shadowRoot.querySelector('wideboi-pane');
    const helper = pane.shadowRoot.querySelector('.clipboard-helper');
    const dt = new DataTransfer();
    dt.setData('text/plain', 'native-helper-paste');
    const pasteEv = new ClipboardEvent('paste', { bubbles: true, cancelable: true, clipboardData: dt });
    helper.dispatchEvent(pasteEv);
  });

  await expect.poll(async () => {
    return await page.evaluate(async () => {
      const { clientMessages } = await import('/tests/browser-fixture.ts');
      const msgs = clientMessages(window.testSockets[0].sent);
      const input = msgs.find(m => m.case === 'input' && m.value?.paste);
      if (!input?.value?.data) return '';
      return new TextDecoder().decode(input.value.data);
    });
  }).toBe('native-helper-paste');
});
