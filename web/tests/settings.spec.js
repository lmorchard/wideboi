import { test, expect } from '@playwright/test';

test('settings modal opens via toolbar, mobile button, and shortcut, and updates settings', async ({ page }) => {
  await page.addInitScript(() => {
    window.testSockets = [];
    window.WebSocket = class {
      static OPEN = 1;
      constructor(url, protocols) {
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
  await expect(page.getByText('Focus Pane:')).toBeVisible();

  await page.evaluate(async () => {
    const { serverBytes } = await import('/tests/browser-fixture.ts');
    const socket = window.testSockets[0];
    socket.message(serverBytes({ case: 'layoutSnapshot', value: {
      columns: [
        { paneId: 1, width: 40, height: 10 },
      ],
      paneTitles: { 1: 'First' },
    } }));
  });

  // 1. Initial state: settings dialog is not visible
  await expect(page.locator('.settings-dialog')).toHaveCount(0);

  // Redundant controls (theme, layout mode, prefix key) are not in the bottom toolbar
  await expect(page.locator('.toolbar #theme-select')).toHaveCount(0);
  await expect(page.locator('.toolbar #layout-mode')).toHaveCount(0);
  await expect(page.locator('.toolbar #prefix-key')).toHaveCount(0);

  // Default theme styling
  const initialBg = await page.evaluate(() => {
    const app = document.querySelector('wideboi-app');
    return app ? getComputedStyle(app).getPropertyValue('--wb-bg-app').trim() : '';
  });
  expect(initialBg).toBe('#1e1e1e');

  // 2. Open via toolbar button
  await page.locator('.toolbar .settings-btn').click();
  await expect(page.locator('.settings-dialog')).toBeVisible();
  await expect(page.locator('#settings-title')).toContainText('wideboi Settings');

  // Verify theme selector in settings dialog reflects active theme
  const settingsTheme = page.locator('#settings-theme');
  await expect(settingsTheme).toBeVisible();
  await expect(settingsTheme).toHaveValue('dark');

  // Select "Nord" theme inside settings dialog
  await settingsTheme.selectOption('nord');
  await expect.poll(() => page.evaluate(() => localStorage.getItem('wideboi.theme'))).toBe('nord');
  const nordBg = await page.evaluate(() => {
    const app = document.querySelector('wideboi-app');
    return app ? getComputedStyle(app).getPropertyValue('--wb-bg-app').trim() : '';
  });
  expect(nordBg).toBe('#2e3440');

  // Change theme to Solarized Light inside settings dialog
  await settingsTheme.selectOption('solarized-light');
  await expect.poll(() => page.evaluate(() => localStorage.getItem('wideboi.theme'))).toBe('solarized-light');
  const lightBg = await page.evaluate(() => {
    const app = document.querySelector('wideboi-app');
    return app ? getComputedStyle(app).getPropertyValue('--wb-bg-app').trim() : '';
  });
  expect(lightBg).toBe('#fdf6e3');

  // Verify prefix key selector in settings dialog
  const settingsPrefix = page.locator('#settings-prefix-key');
  await expect(settingsPrefix).toBeVisible();
  await expect(settingsPrefix).toHaveValue('ctrl+b');
  await settingsPrefix.selectOption('ctrl+a');
  await expect.poll(() => page.evaluate(() => localStorage.getItem('wideboi.prefix'))).toBe('ctrl+a');

  // Verify layout mode selector in settings dialog
  const settingsLayout = page.locator('#settings-layout-mode');
  await expect(settingsLayout).toBeVisible();
  await expect(settingsLayout).toHaveValue('cards');
  await settingsLayout.selectOption('scroll');
  await expect(page.locator('.pane-strip')).not.toHaveClass(/cards/);
  await settingsLayout.selectOption('cards');
  await expect(page.locator('.pane-strip')).toHaveClass(/cards/);

  // Verify font options exist in dropdown
  const fontSelect = page.locator('#settings-font-family');
  await expect(fontSelect).toBeVisible();
  await expect(fontSelect.locator('option')).toHaveCount(13);

  // Select Hack Nerd Font
  await fontSelect.selectOption('Hack Nerd Font Mono');
  await expect.poll(() => page.evaluate(() => localStorage.getItem('wideboi:fontFamily'))).toBe('Hack Nerd Font Mono');

  // Change font size
  const sizeInput = page.locator('#settings-font-size');
  await expect(sizeInput).toHaveValue('14');
  await page.getByRole('button', { name: 'Increase font size' }).click();
  await expect(sizeInput).toHaveValue('15');
  await expect.poll(() => page.evaluate(() => localStorage.getItem('wideboi:fontSize'))).toBe('15');

  // 3. Close via Escape
  await page.keyboard.press('Escape');
  await expect(page.locator('.settings-dialog')).toHaveCount(0);

  // 4. Open via shortcut: prefix is now Ctrl+A then ','
  await page.locator('wideboi-pane canvas').first().focus();
  await page.keyboard.press('Control+a');
  await page.keyboard.press(',');
  await expect(page.locator('.settings-dialog')).toBeVisible();

  // 5. Close via backdrop click
  await page.locator('.settings-overlay').click({ position: { x: 5, y: 5 } });
  await expect(page.locator('.settings-dialog')).toHaveCount(0);

  // 6. Mobile button opens settings
  await page.setViewportSize({ width: 390, height: 700 });
  await page.locator('.mobile-settings-btn').click();
  await expect(page.locator('.settings-dialog')).toBeVisible();
  await page.locator('.settings-dialog .close-btn').click();
  await expect(page.locator('.settings-dialog')).toHaveCount(0);
});
