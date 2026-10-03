# Research: Web Client Copy & Paste

Research conducted via documentarian subagent across `fix/web-client-copy-paste` worktree.

## 1. Event Capture and Handling (`wideboi-app.ts`)

Registered during `WideboiApp.firstUpdated()` (`web/src/wideboi-app.ts:415`) and `WideboiApp.connectedCallback()` (`web/src/wideboi-app.ts:430`) via `setupKeyboard()` (`web/src/wideboi-app.ts:720-833`) on `document` with `{ signal: this.listeners?.signal }`.

### `keydown` Listener (`web/src/wideboi-app.ts:724-808`)
1. **Connection check** (`line 725`): If `!this.connected || !this.client`, returns.
2. **Overlay dismiss checks** (`lines 726-753`):
   - `this.showHelp` (`lines 726-732`): `Escape`, `?`, `Ctrl+C` calls `closeHelp()`, `e.preventDefault()`, returns.
   - `this.showSettings` (`lines 733-739`): `Escape`, `,`, `Ctrl+C` calls `closeSettings()`, `e.preventDefault()`, returns.
   - `this.showCommandPalette` (`lines 740-746`): `Escape`, `Ctrl+C` calls `closeCommandPalette()`, `e.preventDefault()`, returns.
   - `this.showCommandMenu` (`lines 747-753`): `Escape`, `Ctrl+C` calls `closeCommandMenu()`, `e.preventDefault()`, returns.
3. **Form control check** (`line 754`): If `fromFormControl(e)`, returns.
4. **Search invocation** (`lines 755-759`): `(e.ctrlKey || e.metaKey) && (e.key === 'f' || e.code === 'KeyF')` opens search, `e.preventDefault()`, returns.
5. **IME composition check** (`line 760`): If `e.isComposing || e.key === 'Process' || e.key === 'Dead'`, returns.
6. **Non-prefix Ctrl shortcuts** (`lines 762-799`): Guarded by `!this.keyRouter.inPrefix && e.ctrlKey && !e.metaKey && !e.altKey`:
   - `Ctrl+Shift+C` (`lines 763-772`): Reads `text = this.selectedPane?.selectedText() || this.focusedPane()?.selectedText() || ''`. If non-empty, calls `navigator.clipboard.writeText(text)` (`line 766`), `clearSelection()`, sets `this.selectedPane = undefined`, `e.preventDefault()`, returns.
   - `Ctrl+C` (`lines 773-782`): If selection text non-empty, writes to `navigator.clipboard`, clears selection, `e.preventDefault()`, returns. If empty, falls through to terminal input (sends SIGINT / Ctrl+C).
   - `Ctrl+Shift+V` (`lines 783-795`): Calls `navigator.clipboard.readText()`. If text present, calls `sendPasteInput(this.client, this.focusedPaneId, text)`, `revealCursor()`, `e.preventDefault()`, returns.
   - `Ctrl+V` (`lines 796-798`): Returns immediately without `e.preventDefault()`. (Native browser `paste` event does not fire because `<canvas>` is not an editable element).
7. **Terminal search navigation** (`lines 801-804`): `searchController.handleTerminalKeydown(e)`.
8. **KeyRouter dispatch** (`lines 806-807`): `keyRouter.handle(e)` dispatches via `dispatchKeyAction`. Forwarded keys call `forwardKeyEvent(e)` (`lines 835-841`) -> `sendKeyboardInput(...)` (`web/src/input.ts:19-35`). Note: `sendKeyboardInput` drops any event with `metaKey: true` (`web/src/input.ts:21`).

### `copy` Listener (`web/src/wideboi-app.ts:810-818`)
- Guard (`line 811`): `if (!this.connected || !this.client || fromFormControl(e)) return;`
- Lookup (`line 812`): `text = this.selectedPane?.selectedText() || this.focusedPane()?.selectedText() || ''`
- Action (`lines 813-817`): If `text` non-empty, `e.clipboardData?.setData('text/plain', text)`, `navigator.clipboard?.writeText(text)`, `e.preventDefault()`.

### `paste` Listener (`web/src/wideboi-app.ts:820-827`)
- Guard (`line 821`): `if (!this.connected || !this.client || fromFormControl(e)) return;`
- Data (`line 822`): `value = e.clipboardData?.getData('text/plain') || ''`
- Action (`lines 823-826`): `sendPasteInput(this.client, this.focusedPaneId, value)`. If true, `revealCursor()`, `e.preventDefault()`.

## 2. Selection, Coordinates, and Pointer Events (`wideboi-pane.ts`, `wideboi-app.ts`)

- `WideboiPane.prototype.cellAt(clientX, clientY)` (`web/src/wideboi-pane.ts:315-325`): Clamps cell coordinates strictly to pane bounds: `rect = this.canvas.getBoundingClientRect()`.
- Pointer lifecycle:
  - `pointerdown` (`web/src/wideboi-app.ts:942-959`): Pointer capture via `pane.setPointerCapture(e.pointerId)`. Clears prior selection. `e.preventDefault()`.
  - `pointermove` (`web/src/wideboi-app.ts:961-993`): Sets `dragged = true`. If button 1 and not tracking: `press.pane.setSelection(press.start, end)`, sets `this.selectedPane = press.pane`.
  - `pointerup` (`web/src/wideboi-app.ts:1002-1032`): Releases pointer capture. If `dragged`, extracts `text` via `selectionText(...)` and calls `navigator.clipboard?.writeText(text)`. If not dragged, single click clears selection or opens link. `e.preventDefault()`.
- `WideboiPane.prototype.selectedText()` (`web/src/wideboi-pane.ts:343-346`): calls `selectionText(this.pane, this.currentSelection.start, this.currentSelection.end)` (`web/src/pane-state.ts:105-138`).

## 3. Input Encoding & WebSocket Transport (`input.ts`, `client.ts`, protobuf)

- `sendKeyboardInput` (`web/src/input.ts:19-35`): Drops `metaKey: true` (`line 21`). Sends `MsgInput` with `key: KeyData`.
- `sendPasteInput` (`web/src/input.ts:43-47`):
  ```ts
  sender.send({
    case: 'input',
    value: {
      paneId: paneID,
      data: new TextEncoder().encode(text),
      paste: true,
    }
  });
  ```
- Protobuf schema (`internal/protocol/wirepb/wideboi.proto:231-236`):
  `MsgInput` carries `int32 pane_id = 1`, `KeyData key = 2`, `bytes data = 3`, `bool paste = 4`.
  Server wraps `data` in bracketed paste markers if `paste == true` and child terminal has bracketed paste enabled (`internal/server/handlers.go`).

## 4. Test Suite Findings (`web/tests/links-and-clipboard.spec.ts`)

- Pre-existing tests only tested copy by manually dispatching a synthetic `new Event('copy')` (`lines 180-182`).
- Keystrokes (`Meta+c`, `Meta+v`, `Control+v`, `Control+Shift+v`) and right-click paste were never tested in Playwright.
- Test suite had no coverage for paste events or keyboard shortcuts triggering paste.

## 5. Form Controls & `fromFormControl` (`web/src/wideboi-app.ts:721-723`)

- `fromFormControl(e)` checks `e.composedPath().some(node => node instanceof HTMLInputElement || node instanceof HTMLTextAreaElement || node instanceof HTMLSelectElement || node instanceof HTMLButtonElement)`.
- Terminal canvas `<canvas tabindex="0">` is not an editable element.
