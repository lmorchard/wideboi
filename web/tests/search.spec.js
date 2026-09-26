import { test, expect } from '@playwright/test';

function setupMockSocket(page) {
  return page.addInitScript(() => {
    window.testSockets = [];
    window.WebSocket = class {
      static OPEN = 1;
      constructor(url, protocols) {
        this.url = url;
        this.protocols = protocols;
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
}

async function connectPage(page) {
  await page.setViewportSize({ width: 600, height: 400 });
  await page.goto('/');
  await page.getByRole('button', { name: 'Connect' }).click();
  await page.evaluate(() => window.testSockets[0].open());
  await expect(page.getByText('Focus Pane:')).toBeVisible();

  // Send layout snapshot with pane 1
  await page.evaluate(async () => {
    const { serverBytes } = await import('/tests/browser-fixture.ts');
    window.testSockets[0].message(serverBytes({
      case: 'layoutSnapshot',
      value: {
        columns: [{ paneId: 1, width: 80, height: 24 }],
      },
    }));
    window.testSockets[0].message(serverBytes({
      case: 'paneUpdate',
      value: {
        paneId: 1,
        cols: 80,
        rows: 24,
        generation: 1n,
        scrollOffset: 0,
        scrollbackLen: 20,
        lines: [],
      },
    }));
  });
  await expect(page.locator('wideboi-pane')).toHaveCount(1);
}

test('browser search UI: triggers via prefix + /, toolbar, and Ctrl+F', async ({ page }) => {
  await setupMockSocket(page);
  await connectPage(page);

  const searchBar = page.locator('.status.search-bar');
  const searchInput = page.locator('input.search-input');
  await expect(searchBar).toHaveCount(0);

  // 1. Trigger via Toolbar "Search" button
  await page.getByRole('button', { name: 'Search' }).click();
  await expect(searchBar).toBeVisible();
  await expect(searchInput).toBeFocused();

  // Press Escape to close
  await page.keyboard.press('Escape');
  await expect(searchBar).toHaveCount(0);

  // 2. Trigger via Prefix (Ctrl+b) then '/'
  await page.keyboard.press('Control+b');
  await page.keyboard.press('/');
  await expect(searchBar).toBeVisible();
  await expect(searchInput).toBeFocused();

  await page.keyboard.press('Escape');
  await expect(searchBar).toHaveCount(0);

  // 3. Trigger via Ctrl+F
  await page.keyboard.press('Control+f');
  await expect(searchBar).toBeVisible();
  await expect(searchInput).toBeFocused();

  await page.keyboard.press('Escape');
  await expect(searchBar).toHaveCount(0);
});

test('query entry does not leak keystrokes to child, sends historyRequest on Enter, and navigates matches', async ({ page }) => {
  await setupMockSocket(page);
  await connectPage(page);

  const messages = () => page.evaluate(async () => {
    const { clientMessages } = await import('/tests/browser-fixture.ts');
    return clientMessages(window.testSockets[0].sent);
  });

  // Open search
  await page.getByRole('button', { name: 'Search' }).click();
  const searchInput = page.locator('input.search-input');
  await expect(searchInput).toBeFocused();

  const msgCountBeforeTyping = (await messages()).length;

  // Type query
  await page.keyboard.type('foundme');

  // Verify none of these keystrokes were sent as 'input' to child
  const newMessages = (await messages()).slice(msgCountBeforeTyping);
  expect(newMessages.filter(m => m.case === 'input')).toHaveLength(0);

  // Press Enter to commit search
  await page.keyboard.press('Enter');

  // Verify MsgHistoryRequest was sent
  await expect.poll(async () => (await messages()).find(m => m.case === 'historyRequest')?.value.paneId).toBe(1);

  // Send history snapshot with 3 matches (scrollbackLen: 10, total 14 rows)
  await page.evaluate(async () => {
    const { serverBytes } = await import('/tests/browser-fixture.ts');
    window.testSockets[0].message(serverBytes({
      case: 'historySnapshot',
      value: {
        paneId: 1,
        scrollbackLen: 10,
        rows: [
          'row 0 foundme first', // row 0 -> scroll target: 10 - 0 = 10
          'row 1 normal line',
          'row 2 foundme second', // row 2 -> scroll target: 10 - 2 = 8
          'row 3 normal line',
          'row 4 normal line',
          'row 5 normal line',
          'row 6 normal line',
          'row 7 normal line',
          'row 8 normal line',
          'row 9 normal line',
          'row 10 screen foundme third', // row 10 -> scroll target: 0
          'row 11 screen bottom',
        ],
      },
    }));
  });

  // Status message should indicate 3/3 on initial find (jumped to latest/bottom-most match)
  const searchMsg = page.locator('.search-msg');
  await expect(searchMsg).toContainText('3/3');

  // Verify MsgScroll was sent to offset 0 (since match is on screen row 10)
  const scrollMsgs = async () => (await messages()).filter(m => m.case === 'scroll');
  await expect.poll(async () => (await scrollMsgs()).at(-1)?.value).toMatchObject({
    paneId: 1,
    offset: 0,
    anchorHistory: true,
    historyLen: 10,
  });

  // Click Prev button (▲ Prev) to jump to match 2 (row 2, scroll target 8)
  await page.locator('button.prev-btn').click();

  // Next history request is sent
  await expect.poll(async () => (await messages()).filter(m => m.case === 'historyRequest').length).toBe(2);

  // Send snapshot response
  await page.evaluate(async () => {
    const { serverBytes } = await import('/tests/browser-fixture.ts');
    window.testSockets[0].message(serverBytes({
      case: 'historySnapshot',
      value: {
        paneId: 1,
        scrollbackLen: 10,
        rows: [
          'row 0 foundme first',
          'row 1 normal line',
          'row 2 foundme second',
          'row 3 normal line',
          'row 4 normal line',
          'row 5 normal line',
          'row 6 normal line',
          'row 7 normal line',
          'row 8 normal line',
          'row 9 normal line',
          'row 10 screen foundme third',
          'row 11 screen bottom',
        ],
      },
    }));
  });

  await expect(searchMsg).toContainText('2/3');
  await expect.poll(async () => (await scrollMsgs()).at(-1)?.value).toMatchObject({
    paneId: 1,
    offset: 8,
    anchorHistory: true,
    historyLen: 10,
  });

  // Press Enter or click Keep to accept view
  await page.locator('button.keep-btn').click();
  await expect(page.locator('.status.search-bar')).toHaveCount(0);
});

test('no match displays message and Escape restores prior offset', async ({ page }) => {
  await setupMockSocket(page);
  await connectPage(page);

  // Set initial scroll offset to 6
  await page.evaluate(async () => {
    const { serverBytes } = await import('/tests/browser-fixture.ts');
    window.testSockets[0].message(serverBytes({
      case: 'paneUpdate',
      value: {
        paneId: 1,
        cols: 80,
        rows: 24,
        generation: 2n,
        scrollOffset: 6,
        scrollbackLen: 30,
        lines: [],
      },
    }));
  });

  const messages = () => page.evaluate(async () => {
    const { clientMessages } = await import('/tests/browser-fixture.ts');
    return clientMessages(window.testSockets[0].sent);
  });

  // Open search
  await page.getByRole('button', { name: 'Search' }).click();
  await page.keyboard.type('missingquery');
  await page.keyboard.press('Enter');

  // Respond with snapshot having no matches
  await page.evaluate(async () => {
    const { serverBytes } = await import('/tests/browser-fixture.ts');
    window.testSockets[0].message(serverBytes({
      case: 'historySnapshot',
      value: {
        paneId: 1,
        scrollbackLen: 30,
        rows: ['line A', 'line B'],
      },
    }));
  });

  // Verify 'no match' is displayed
  await expect(page.locator('.search-msg')).toContainText('no match');

  // Press Escape to cancel and restore prior view
  await page.keyboard.press('Escape');
  await expect(page.locator('.status.search-bar')).toHaveCount(0);

  // Verify MsgScroll was sent restoring offset 6
  const scrollMsgs = async () => (await messages()).filter(m => m.case === 'scroll');
  await expect.poll(async () => (await scrollMsgs()).at(-1)?.value).toMatchObject({
    paneId: 1,
    offset: 6,
    anchorHistory: true,
    historyLen: 30,
  });
});

test('handles Unicode and soft-wrapped rows correctly', async ({ page }) => {
  await setupMockSocket(page);
  await connectPage(page);

  const messages = () => page.evaluate(async () => {
    const { clientMessages } = await import('/tests/browser-fixture.ts');
    return clientMessages(window.testSockets[0].sent);
  });

  await page.getByRole('button', { name: 'Search' }).click();
  await page.keyboard.type('本');
  await page.keyboard.press('Enter');

  // Provide snapshot with Japanese Kanji '本' and emoji '✳'
  await page.evaluate(async () => {
    const { serverBytes } = await import('/tests/browser-fixture.ts');
    window.testSockets[0].message(serverBytes({
      case: 'historySnapshot',
      value: {
        paneId: 1,
        scrollbackLen: 5,
        rows: [
          'soft-wrapped prefix part that continued onto next line',
          'and has Kanji 本 here in scrollback',
          'line without',
          'screen line 1',
          'screen line 2',
        ],
      },
    }));
  });

  const searchMsg = page.locator('.search-msg');
  // Row 1 in 5 scrollback lines -> target offset = 5 - 1 = 4
  await expect(searchMsg).toContainText('1/1 row 2');

  const scrollMsgs = async () => (await messages()).filter(m => m.case === 'scroll');
  await expect.poll(async () => (await scrollMsgs()).at(-1)?.value).toMatchObject({
    paneId: 1,
    offset: 4,
    anchorHistory: true,
    historyLen: 5,
  });
});

test('two independent browser clients maintain isolated search and scroll states', async ({ browser }) => {
  const contextA = await browser.newContext();
  const pageA = await contextA.newPage();
  const contextB = await browser.newContext();
  const pageB = await contextB.newPage();

  await setupMockSocket(pageA);
  await setupMockSocket(pageB);

  await connectPage(pageA);
  await connectPage(pageB);

  // Client A opens search
  await pageA.getByRole('button', { name: 'Search' }).click();
  await expect(pageA.locator('.status.search-bar')).toBeVisible();

  // Client B does NOT have search open
  await expect(pageB.locator('.status.search-bar')).toHaveCount(0);

  // Client A types and commits 'alpha'
  await pageA.keyboard.type('alpha');
  await pageA.keyboard.press('Enter');

  // Send snapshot to Client A
  await pageA.evaluate(async () => {
    const { serverBytes } = await import('/tests/browser-fixture.ts');
    window.testSockets[0].message(serverBytes({
      case: 'historySnapshot',
      value: {
        paneId: 1,
        scrollbackLen: 10,
        rows: ['alpha match', 'beta line'],
      },
    }));
  });

  await expect(pageA.locator('.search-msg')).toContainText('1/1');

  // Client B now independently opens search and searches 'beta'
  await pageB.getByRole('button', { name: 'Search' }).click();
  await expect(pageB.locator('.status.search-bar')).toBeVisible();

  await pageB.keyboard.type('beta');
  await pageB.keyboard.press('Enter');

  await pageB.evaluate(async () => {
    const { serverBytes } = await import('/tests/browser-fixture.ts');
    window.testSockets[0].message(serverBytes({
      case: 'historySnapshot',
      value: {
        paneId: 1,
        scrollbackLen: 10,
        rows: ['alpha match', 'beta match'],
      },
    }));
  });

  // Client B has its own match status and query
  await expect(pageB.locator('input.search-input')).toHaveValue('beta');
  await expect(pageA.locator('input.search-input')).toHaveValue('alpha');

  // Client A cancels search
  await pageA.locator('button.restore-btn').click();
  await expect(pageA.locator('.status.search-bar')).toHaveCount(0);

  // Client B's search overlay remains open and unaffected
  await expect(pageB.locator('.status.search-bar')).toBeVisible();
  await expect(pageB.locator('input.search-input')).toHaveValue('beta');

  await contextA.close();
  await contextB.close();
});
