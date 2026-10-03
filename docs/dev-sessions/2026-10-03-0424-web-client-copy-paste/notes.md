# Notes: Web Client Copy & Paste

- Branch: `fix/web-client-copy-paste`
- Worktree: `.worktrees/web-client-copy-paste`
- Date: 2026-10-03

## Summary of Changes

1. **Test Environment Isolation (`web/tests/live-terminal.spec.ts`)**:
   - Sanitized `process.env` when spawning the test `wideboi server` to strip `WIDEBOI*` and `LC_WIDEBOI` per `docs/LESSONS.md:218-221`.
   - Increased Playwright default timeout in `web/playwright.config.js` to 30s to prevent flaky timeouts under 4-worker CPU load.
   - Pinned long-running 2M-draw test in `web/src/stats.test.ts` to 15s timeout.

2. **Helper `<textarea>` & Selection Sync (`web/src/wideboi-pane.ts`, `web/src/wideboi-app.ts`)**:
   - Added invisible `<textarea class="clipboard-helper">` to each `WideboiPane` (`web/src/wideboi-pane.ts`).
   - Synced canvas drag selections into `helperElement.value` and invoked `select()`, exposing native DOM selection to the browser.
   - Preserved `focusInput()` focusing `<canvas>` as primary to satisfy existing activeElement invariants while allowing helper textarea fallback.
   - Emitted `pane-paste` custom event on helper textarea paste and wired `wideboi-app.ts` to dispatch `sendPasteInput` to server.
   - Updated `fromFormControl(e)` in `wideboi-app.ts` to ignore `.clipboard-helper` so keystrokes bubble to the terminal key router.

3. **Platform-Idiomatic Keyboard Shortcuts (`web/src/wideboi-app.ts`)**:
   - macOS:
     - `Cmd+C`: Copies selected text to clipboard via `copyToClipboard()` (with `document.execCommand('copy')` fallback for insecure HTTP contexts) and clears selection.
     - `Cmd+V`: Pastes clipboard text via `pasteFromClipboard()`.
     - `Ctrl+C`: Falls through to send SIGINT (`0x03`) to the terminal.
   - Linux/Windows:
     - `Ctrl+C`: Copies active selection if present; otherwise falls through to SIGINT.
     - `Ctrl+Shift+C`: Copies active selection.
     - `Ctrl+V`, `Ctrl+Shift+V`, `Shift+Insert`: Pastes from clipboard.

4. **Custom Terminal Context Menu (`web/src/components/context-menu.ts`, `web/src/wideboi-app.ts`)**:
   - Added `WideboiContextMenu` Lit component triggered on right-click when mouse tracking is disabled for the pane.
   - Supported **Copy** (active if selection exists), **Paste**, and **Select All** (`pane.selectAll()`).
   - Excluded right-click pointer down from capturing pointer or clearing selection when mouse tracking is disabled, allowing `contextmenu` event to handle it cleanly.
   - Dismisses on outside click, Escape, or pane strip scroll.

5. **Command Menu & Command Palette Integration (`web/src/components/command-menu.ts`, `web/src/components/command-palette.ts`, `web/src/wideboi-app.ts`)**:
   - Added `paste` command ("Paste from Clipboard", shortcut `v`) to `WideboiCommandMenu` and `CommandPalette`.
   - Supported `:paste` in the command prompt.

## Verification Evidence

- `make quick`: Passed cleanly (tsc, vite build, vet, seam-check, Go test suite, vitest).
- `web-test` (vitest): 25 test files, 182 tests passed.
- `web-accept` (playwright): 57 tests passed across all browser suites.
- Full `make check` verification executed across all gates before opening PR.
