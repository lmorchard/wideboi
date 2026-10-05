import { test, expect, type Page } from '@playwright/test';
import { installMockWebSocket } from './browser-fixture';

async function connectWithPane(page: Page) {
  await page.goto('/');
  await page.getByRole('button', { name: 'Connect' }).click();
  await page.evaluate(() => window.testSockets[0].open());
  await expect(page.locator('.toolbar')).toBeVisible();
  await page.evaluate(async () => {
    const { serverBytes } = await import('/tests/browser-fixture.ts');
    window.testSockets[0].message(serverBytes({
      case: 'layoutSnapshot',
      value: { columns: [{ paneId: 1, width: 80, height: 24 }] },
    }));
  });
}

async function paneCopies(page: Page, text: string) {
  await page.evaluate(async (text) => {
    const { serverBytes } = await import('/tests/browser-fixture.ts');
    window.testSockets[0].message(serverBytes({
      case: 'paneClipboard',
      value: { paneId: 1, title: 'claude', text },
    }));
  }, text);
}

const clipboard = (page: Page) => page.evaluate(() => navigator.clipboard.readText());

test('a pane copy waits for Copy, and Dismiss leaves the clipboard alone', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  await installMockWebSocket(page);
  await connectWithPane(page);
  await page.evaluate(() => navigator.clipboard.writeText('before'));

  const toast = page.locator('wideboi-clipboard-toast');
  await paneCopies(page, 'line1\nline2\nline3\nline4');
  await expect(toast).toBeVisible();
  await expect(toast).toContainText('claude (pane 1) wants to copy');
  await expect(toast).toContainText('23 characters');
  await expect(toast.locator('.preview')).toContainText('line3');
  await expect(toast.locator('.preview')).toContainText('…');
  await expect(toast.locator('.preview')).not.toContainText('line4');
  // Nothing reaches the clipboard without the click.
  expect(await clipboard(page)).toBe('before');

  await toast.getByRole('button', { name: 'Copy' }).click();
  await expect(toast).toHaveCount(0);
  expect(await clipboard(page)).toBe('line1\nline2\nline3\nline4');

  await paneCopies(page, 'unwanted');
  await expect(toast).toBeVisible();
  await toast.getByRole('button', { name: 'Dismiss' }).click();
  await expect(toast).toHaveCount(0);
  expect(await clipboard(page)).toBe('line1\nline2\nline3\nline4');
});

test('a newer pane copy replaces the pending one', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  await installMockWebSocket(page);
  await connectWithPane(page);

  const toast = page.locator('wideboi-clipboard-toast');
  await paneCopies(page, 'first');
  await paneCopies(page, 'second');
  await expect(toast).toHaveCount(1);
  await expect(toast.locator('.preview')).toHaveText('second');
  await toast.getByRole('button', { name: 'Copy' }).click();
  expect(await clipboard(page)).toBe('second');
});

test('turning off "Allow panes to copy" suppresses the toast', async ({ page }) => {
  await installMockWebSocket(page);
  await page.addInitScript(() => localStorage.setItem('wideboi:paneClipboard', 'false'));
  await connectWithPane(page);

  await paneCopies(page, 'ignored');
  // A later layout message proves the copy message was processed first.
  await page.evaluate(async () => {
    const { serverBytes } = await import('/tests/browser-fixture.ts');
    window.testSockets[0].message(serverBytes({
      case: 'layoutSnapshot',
      value: { columns: [{ paneId: 1, width: 80, height: 24 }], paneTitles: { 1: 'after' } },
    }));
  });
  await expect(page.locator('wideboi-pane')).toHaveCount(1);
  await expect(page.locator('wideboi-clipboard-toast')).toHaveCount(0);
});

test('the settings dialog toggles "Allow panes to copy"', async ({ page }) => {
  await installMockWebSocket(page);
  await connectWithPane(page);
  await page.locator('.toolbar .settings-btn').click();
  const toggle = page.locator('#settings-pane-clipboard');
  await expect(toggle).toContainText('Enabled');
  await toggle.click();
  await expect(toggle).toContainText('Disabled');
  expect(await page.evaluate(() => localStorage.getItem('wideboi:paneClipboard'))).toBe('false');
});

test('turning the setting off drops a pending copy', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  await installMockWebSocket(page);
  await connectWithPane(page);
  await page.evaluate(() => navigator.clipboard.writeText('before'));

  const toast = page.locator('wideboi-clipboard-toast');
  await paneCopies(page, 'arrived before the opt-out');
  await expect(toast).toBeVisible();

  await page.locator('.toolbar .settings-btn').click();
  await page.locator('#settings-pane-clipboard').click();
  await expect(page.locator('#settings-pane-clipboard')).toContainText('Disabled');
  await expect(toast).toHaveCount(0);
  expect(await clipboard(page)).toBe('before');
});
