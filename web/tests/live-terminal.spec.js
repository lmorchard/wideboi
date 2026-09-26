import { test, expect } from '@playwright/test';
import { spawn } from 'child_process';
import net from 'net';
import https from 'https';
import path from 'path';
import fs from 'fs';
import os from 'os';

function getFreePort() {
  return new Promise((resolve, reject) => {
    const srv = net.createServer();
    srv.listen(0, '127.0.0.1', () => {
      const addr = srv.address();
      const port = typeof addr === 'object' && addr ? addr.port : 0;
      srv.close(err => (err ? reject(err) : resolve(port)));
    });
  });
}

test.describe('Live Server Terminal & Input Parity', () => {
  let port;
  let token;
  let sockPath;
  let serverProc;

  test.beforeAll(async () => {
    port = await getFreePort();
    token = `live-test-token-${Date.now()}`;
    sockPath = path.join(os.tmpdir(), `wideboi-live-${Date.now()}-${Math.random().toString(36).slice(2)}.sock`);

    const binPath = path.resolve(process.cwd(), '../bin/wideboi');
    serverProc = spawn(
      binPath,
      [
        '-s', sockPath,
        'server',
        '--websocket', `127.0.0.1:${port}`,
        '--websocket-token', token,
      ],
      {
        env: {
          ...process.env,
          SHELL: '/bin/sh',
          TERM: 'xterm-256color',
          PS1: '$ ',
          LANG: 'C.UTF-8',
          LC_ALL: 'C.UTF-8',
        },
        stdio: ['ignore', 'pipe', 'pipe'],
      }
    );

    // Wait until HTTPS endpoint is answering
    const deadline = Date.now() + 10_000;
    let up = false;
    while (Date.now() < deadline) {
      try {
        const ok = await new Promise((resolve) => {
          const req = https.get(`https://127.0.0.1:${port}/`, { rejectUnauthorized: false }, (res) => {
            resolve(res.statusCode === 200);
          });
          req.on('error', () => resolve(false));
          req.setTimeout(500, () => {
            req.destroy();
            resolve(false);
          });
        });
        if (ok) {
          up = true;
          break;
        }
      } catch {
        // wait
      }
      await new Promise(r => setTimeout(r, 100));
    }
    if (!up) {
      serverProc.kill('SIGKILL');
      throw new Error(`Server failed to start on 127.0.0.1:${port}`);
    }
  });

  test.afterAll(async () => {
    if (serverProc && serverProc.exitCode === null) {
      serverProc.kill('SIGTERM');
      await new Promise(r => {
        const timeout = setTimeout(() => {
          serverProc.kill('SIGKILL');
          r();
        }, 2000);
        serverProc.on('exit', () => {
          clearTimeout(timeout);
          r();
        });
      });
    }
    try {
      if (fs.existsSync(sockPath)) fs.unlinkSync(sockPath);
      const lockPath = sockPath + '.lock';
      if (fs.existsSync(lockPath)) fs.unlinkSync(lockPath);
    } catch {
      // ignore cleanup errors
    }
  });

  test('types text into a live shell, interacts with cat, and uses prefix actions', async ({ page }) => {
    await page.addInitScript(() => {
      window.drawnText = [];
      const original = CanvasRenderingContext2D.prototype.fillText;
      CanvasRenderingContext2D.prototype.fillText = function (text, ...args) {
        window.drawnText.push(text);
        return original.call(this, text, ...args);
      };
    });

    await page.goto(`https://127.0.0.1:${port}/#token=${token}`);
    await page.getByRole('button', { name: 'Connect' }).click();

    // Verify connection and panes
    await expect(page.getByText('Focus Pane:')).toBeVisible();
    const panes = page.locator('wideboi-pane');
    await expect(panes).toHaveCount(2);

    // Wait for prompt to render on canvas
    await expect.poll(() => page.evaluate(() => window.drawnText.join(''))).toContain('$');

    // Focus active canvas
    await page.locator('wideboi-pane canvas').first().focus();

    // 1. Type a shell command and verify stdout reached canvas
    await page.keyboard.type('echo "WB_TEST_PARITY_148"');
    await page.keyboard.press('Enter');
    await expect.poll(() => page.evaluate(() => window.drawnText.join(''))).toContain('WB_TEST_PARITY_148');

    // 2. Full-screen interactive application: run cat
    await page.keyboard.type('cat');
    await page.keyboard.press('Enter');
    // Type interactive lines in cat
    await page.keyboard.type('interactive_input_line');
    await page.keyboard.press('Enter');
    await expect.poll(() => page.evaluate(() => window.drawnText.join(''))).toContain('interactive_input_line');

    // Exit cat using Ctrl+C
    await page.keyboard.press('Control+c');
    await page.keyboard.type('echo "POST_CAT_OK"');
    await page.keyboard.press('Enter');
    await expect.poll(() => page.evaluate(() => window.drawnText.join(''))).toContain('POST_CAT_OK');

    // 3. Test Prefix Action: Column Jump
    // Currently focused on Pane 1
    await expect(page.getByRole('combobox', { name: 'Focus Pane:' })).toHaveValue('1');
    await page.keyboard.press('Control+b');
    await page.keyboard.press('2');
    await expect(page.getByRole('combobox', { name: 'Focus Pane:' })).toHaveValue('2');

    // Focus back to Pane 1
    await page.keyboard.press('Control+b');
    await page.keyboard.press('1');
    await expect(page.getByRole('combobox', { name: 'Focus Pane:' })).toHaveValue('1');

    // 4. Test Prefix Action: Layout Toggle
    await expect(page.locator('.pane-strip')).toHaveClass(/cards/);
    await page.keyboard.press('Control+b');
    await page.keyboard.press('c');
    await expect(page.locator('.pane-strip')).not.toHaveClass(/cards/);
    await page.keyboard.press('Control+b');
    await page.keyboard.press('c');
    await expect(page.locator('.pane-strip')).toHaveClass(/cards/);

    // 5. Test Prefix Action: Help Overlay
    await expect(page.locator('.help-dialog')).toHaveCount(0);
    await page.keyboard.press('Control+b');
    await page.keyboard.press('?');
    await expect(page.locator('.help-dialog')).toBeVisible();
    await expect(page.locator('.help-dialog')).toContainText('wideboi Shortcuts');
    await page.keyboard.press('Escape');
    await expect(page.locator('.help-dialog')).toHaveCount(0);
  });
});
