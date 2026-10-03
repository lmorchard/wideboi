# Web Client Copy & Paste Spec

**Goal:** Provide reliable, platform-idiomatic copy and paste in the wideboi web UI client across desktop and mobile, supporting macOS (`Cmd+C`/`Cmd+V`), Linux/Windows (`Ctrl+C`/`Ctrl+V`/`Ctrl+Shift+C`/`Ctrl+Shift+V`/`Shift+Insert`), a lightweight terminal context menu, and command menu/palette integration without failing in insecure contexts or browser permission restrictions.

**Source:** User request from 2026-10-03

## Current state

The web UI client relies on `setupKeyboard()` (`web/src/wideboi-app.ts:720-808`), canvas-level selection (`web/src/wideboi-pane.ts:343-346`), and document-level `copy`/`paste` event listeners (`web/src/wideboi-app.ts:810-827`):
- Keystroke filtering in `keydown` (`web/src/wideboi-app.ts:762-799`) explicitly requires `!e.metaKey && e.ctrlKey`, meaning `Cmd+C` and `Cmd+V` on macOS are completely ignored.
- For `Ctrl+V` on Linux/Windows, line 796 returns early without action, assuming the browser will dispatch a native `paste` event. However, because `<canvas>` is uneditable, browsers never fire a `paste` event on `Ctrl+V`.
- `Ctrl+Shift+V` calls `navigator.clipboard?.readText()` (`line 784`), which fails in Firefox (unsupported by default), fails in insecure contexts (`http://` on non-localhost where `navigator.clipboard` is `undefined`), and triggers permission prompts in Chrome.
- For copy, `Ctrl+C` and `Ctrl+Shift+C` only invoke `navigator.clipboard?.writeText(text)` and `e.preventDefault()`, which fails silently on insecure origins (HTTP) and disables native copy fallback.
- Right-clicking in a terminal pane invokes `e.preventDefault()` (`web/src/wideboi-app.ts:958`), suppressing browser context menus with no custom alternative.
- In `web/tests/links-and-clipboard.spec.ts:180-182`, copy was only tested with synthetic `dispatchEvent(new Event('copy'))`, masking keyboard and browser event routing failures.
- In `web/tests/live-terminal.spec.ts:41-50`, out-of-process server spawning inherited `process.env`, violating `LESSONS.md` regarding stripping `WIDEBOI*` environment variables.

## Desired end state

1. **Helper Textarea per Pane (xterm.js / VS Code pattern)**:
   - Each `WideboiPane` (`web/src/wideboi-pane.ts`) contains an invisible `<textarea class="clipboard-helper">` positioned within the pane.
   - When the pane is focused via `pane.focusInput()`, the helper textarea receives focus.
   - Keyboard events originating from the helper textarea bubble up to `setupKeyboard()`. `fromFormControl()` (`web/src/wideboi-app.ts:721`) is updated so the clipboard helper is recognized as terminal input rather than an overlay form control.
   - When text is selected in a pane (via mouse drag or programmatically), the helper textarea's content is synchronized to the selected text and selected (`textarea.select()`).
   - Browser native `copy` and `paste` events fire naturally on the helper textarea across all browsers (Chrome, Firefox, Safari, Edge) and origins (HTTP and HTTPS).
   - The `paste` event listener on the helper textarea captures `e.clipboardData.getData('text/plain')`, dispatches `sendPasteInput(client, paneId, text)`, and clears the helper value.

2. **Keyboard Shortcuts Across Platforms**:
   - **macOS**:
     - `Cmd+C`: Copies selected text to clipboard. If no selection exists, does nothing.
     - `Cmd+V`: Pastes clipboard text into the focused pane.
     - `Ctrl+C`: Sends SIGINT (`0x03`) to the terminal process.
   - **Linux / Windows**:
     - `Ctrl+C`: If text is selected in the active pane, copies it to clipboard and clears selection. If no selection exists, sends SIGINT (`0x03`) to the terminal.
     - `Ctrl+Shift+C`: Copies active selection to clipboard.
     - `Ctrl+V`, `Ctrl+Shift+V`, and `Shift+Insert`: Pastes clipboard text into the focused pane.
   - **Fallback Execution**:
     - On copy: Tries native copy via textarea selection, falls back to `navigator.clipboard?.writeText()` and `document.execCommand('copy')`.
     - On paste: Handles native `paste` event on helper textarea. If triggered via shortcut where native event doesn't fire, falls back to `navigator.clipboard?.readText()`.

3. **Custom Terminal Context Menu**:
   - When mouse tracking is OFF for a pane, right-clicking (`contextmenu` or right pointer release) displays a lightweight, dark-themed context menu at the click position.
   - Menu options:
     - **Copy**: Enabled when text is selected; copies selection and closes menu.
     - **Paste**: Pastes from clipboard (using `navigator.clipboard.readText()` or helper paste) and closes menu.
     - **Select All**: Selects all text on the visible terminal screen.
   - Clicking outside, pressing Escape, or scrolling closes the context menu.
   - When mouse tracking is ON (e.g. vim/htop), mouse events are forwarded to the child application without opening the context menu (`web/src/wideboi-app.ts:951, 1065`).

4. **Command Menu & Command Palette Integration**:
   - Add a `Paste` command (`id: 'paste'`) to `WideboiCommandMenu` (`web/src/components/command-menu.ts`) and `CommandPalette` (`web/src/components/command-palette.ts`).
   - Selecting `Paste` reads clipboard text and calls `sendPasteInput(client, focusedPaneId, text)`, giving mobile users a direct paste mechanism without needing a physical keyboard.

5. **Test Harness Fix & E2E Coverage**:
   - In `web/tests/live-terminal.spec.ts`, strip `WIDEBOI*` environment variables from the spawned server environment per `LESSONS.md`.
   - In `web/tests/links-and-clipboard.spec.ts` (and unit tests), add real browser test cases for:
     - `Cmd+C` / `Ctrl+C` copying selected text to clipboard.
     - `Cmd+V` / `Ctrl+V` / `Ctrl+Shift+V` pasting text into active pane.
     - Right-click context menu opening, Copy, Paste, and Select All.
     - Command menu / command palette Paste action.

## Design decisions

- **Decision:** Use an invisible helper `<textarea>` in each `WideboiPane` rather than intercepting raw canvas events alone.
  - **Why:** Browsers restrict clipboard access (`e.clipboardData`) during `paste` events strictly to editable elements or active user gestures. Relying on canvas focus broke `Ctrl+V` and `Cmd+V` in all browsers and made `Ctrl+Shift+V` dependent on `navigator.clipboard.readText()` (which fails on Firefox and HTTP). A helper textarea provides synchronous `clipboardData` access everywhere without permission prompts.
  - **Rejected:** Pure `navigator.clipboard` API without an editable element. Rejected because Firefox blocks `readText()` by default, HTTP origins have no `navigator.clipboard`, and Chrome triggers jarring permission prompts.

- **Decision:** Render a custom lightweight context menu on right-click instead of attempting to position the hidden textarea under the mouse for native browser context menu.
  - **Why:** Cross-browser native context menu placement across Shadow DOM, canvas transforms, and zoom scale factors is fragile and often shows browser options irrelevant to a terminal ("Inspect Element", "Save Image As..."). A custom menu guarantees consistent styling, keyboard dismissability, and exact terminal actions ("Copy", "Paste", "Select All").
  - **Rejected:** Right-click to paste immediately (PuTTY style). Rejected because accidental right-clicks would dump clipboard contents into shells, and users expect to be able to right-click to copy or select.

- **Decision:** Support dual copy/paste shortcuts on Linux/Windows (`Ctrl+C`/`Ctrl+V` and `Ctrl+Shift+C`/`Ctrl+Shift+V`).
  - **Why:** Web users instinctively use `Ctrl+C` and `Ctrl+V`, while terminal users use `Ctrl+Shift+C` and `Ctrl+Shift+V`. Since `Ctrl+C` only copies when text is actively selected and falls through to SIGINT otherwise, there is no conflict.

## Patterns to follow

- Helper input controller pattern: `web/src/mobile-direct-input.ts:6-14, 34-59`.
- Input message generation: `web/src/input.ts:43-47` (`sendPasteInput`).
- Text extraction and selection: `web/src/pane-state.ts:105-138` (`selectionText`).
- Modal overlay and keyboard traps: `web/src/components/command-menu.ts:52-84`.
- Mouse tracking guard: `web/src/wideboi-app.ts:951, 1065` (`this.panes.mouseTracking(pane.paneId)`).
- Environment stripping for test server processes: `LESSONS.md:218-221` and `scripts/ptylib.py:pinned_env`.

## What we're NOT doing

- We are NOT modifying the wire protocol or Go server (`protocol.MsgInput` already supports `Paste bool`, bump #312).
- We are NOT implementing OSC 52 clipboard reading over WebSocket in this session (this is browser client copy/paste).
- We are NOT modifying desktop manager Wails clipboard integration (`cmd/wideboi/main.go` already handles OSC 52 and local clipboard).
- We are NOT changing drag-to-select visual rendering or mouse tracking event formats.

## Open questions

- None. All questions resolved during brainstorm.
