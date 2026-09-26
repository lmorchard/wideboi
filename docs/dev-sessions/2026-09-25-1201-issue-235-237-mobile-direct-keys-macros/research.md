# Codebase Research: Mobile Direct Keys & Macros

## 1. Web Terminal Input Flow

Web terminal input converges on two helper functions in `web/src/input.ts`:
- `sendKeyboardInput(client, paneId, event)` (`web/src/input.ts:18`): Constructs a Protobuf `KeyData` message from a `KeyboardEvent` and sends `{ case: 'input', value: { paneId, key: KeyData } }`.
- `sendTextInput(client, paneId, text)` (`web/src/input.ts:36`): Encodes string as UTF-8 bytes and sends `{ case: 'input', value: { paneId, data: Uint8Array } }`.
- Wire transmission via `WideboiClient.send` (`web/src/client.ts:85`): Uses protobuf `ClientMessageSchema` over WebSocket.

### Desktop Input Path
- `WideboiApp.setupKeyboard` (`web/src/wideboi-app.ts:977`): Document-level `keydown` listener.
- `fromFormControl` filter (`web/src/wideboi-app.ts:974-976, 986`): Bypasses terminal input if target is an `input`, `textarea`, `select`, or `button`.
- Evaluates `this.keyRouter.handle(e)` (`web/src/key-router.ts:85`). For `'forward'` or `'send_literal_key'`, dispatches `sendKeyboardInput`.
- `paste` (`web/src/wideboi-app.ts:1090`) and `compositionend` (`web/src/wideboi-app.ts:1099`) dispatch `sendTextInput`.

### Mobile Dock & Draft Path
- Media query: `NARROW_VIEW = '(max-width: 480px)'` (`web/src/wideboi-app.ts:18, 404, 464`).
- Mobile bar at top: prev/next buttons and pane `<select>` (`web/src/wideboi-app.ts:1413-1423`).
- Mobile dock at bottom (`web/src/wideboi-app.ts:1514-1539`):
  - `.mobile-compose`: `<textarea>` (`@input`, `@keydown=${this.handleMobileDraftKey}`) + `<button>Send</button>` (`web/src/wideboi-app.ts:1515-1520`).
  - Typing in textarea updates `this.mobileDraft = (e.target as HTMLTextAreaElement).value` (`web/src/wideboi-app.ts:1518`).
  - Clicking Send calls `sendMobileDraft()` (`web/src/wideboi-app.ts:1274-1283`): sends UTF-8 bytes via `sendTextInput`, resets `mobileDraft = ''`, and clears textarea value.
  - `handleMobileDraftKey` (`web/src/wideboi-app.ts:1259-1272`):
    - If `mobileCtrl && e.key.length === 1`: calls `sendMobileKey(e.key, e.code)`, `e.preventDefault()`.
    - If `e.ctrlKey || e.altKey || e.key === 'Escape' || e.key === 'Tab' || (mobileDraft === '' && e.key.startsWith('Arrow'))`: calls `sendKeyboardInput`, `e.preventDefault()`.
  - `.mobile-keys`: Buttons for Esc, Tab, Ctrl (toggles `mobileCtrl`), conditional buttons C, D, Z when `mobileCtrl` is active, Arrow keys (←, ↓, ↑, →), ⌫ (Backspace), ↵ (Enter).
  - Clicking a key button calls `sendMobileKey(key, code)` (`web/src/wideboi-app.ts:1248-1257`), which synthesizes `KeyboardEvent('keydown', { key, code, ctrlKey: this.mobileCtrl })`, sends it via `sendKeyboardInput`, reveals the cursor, and clears `this.mobileCtrl = false`.

## 2. Key Encoding & Modifiers (`web/src/input.ts`)

- `namedCodes` (`web/src/input.ts:2-9`):
  - Arrows (Up: `0x110001`, Down: `0x110002`, Right: `0x110003`, Left: `0x110004`).
  - Insert (`0x110007`), Delete (`0x110008`), PageUp (`0x11000A`), PageDown (`0x11000B`), Home (`0x11000C`), End (`0x11000D`).
  - Backspace (`127`), Tab (`9`), Enter (`13`), Escape (`27`).
- Key codes:
  - Letters: `/^Key[A-Z]$/` -> lowercase ASCII code point (e.g. `'KeyR'` -> `114`, `'KeyC'` -> `99`).
  - Digits: `/^Digit[0-9]$/` -> digit ASCII code point.
  - Others: `namedCodes[key] ?? key.codePointAt(0)`.
- Modifiers bitmask:
  - Shift = `1`, Alt = `2`, Ctrl = `4`.
  - Filter: `metaKey`, `isComposing`, `Process`, `Dead`, `Unidentified` are rejected (`web/src/input.ts:19-20`).
- `KeyData`: `{ text, mod, code, shiftedCode: 0, baseCode: code, isRepeat: repeat }`.

## 3. Configuration & State Management

- `localStorage` is used for client-side configuration (`'wideboi.prefix'` in `web/src/wideboi-app.ts:444, 1311`).
- Wrapped in try/catch to handle privacy mode or quota errors.
- Other settings (`layoutMode`, `followPTY`, `displayWidths`, `searchState`) are currently in-memory on `WideboiApp`.

## 4. Test Infrastructure

- `web/src/input.test.ts`: Unit tests testing `sendKeyboardInput` and `sendTextInput` mock encoding.
- `web/tests/mobile.spec.js`: Playwright browser tests using intercepted WebSocket:
  - Sets viewport `{ width: 390, height: 700 }`, `isMobile: true`, `hasTouch: true`.
  - Verifies draft vs send separation, terminal key clicks, touch pan without terminal mouse events, and visual viewport keyboard handling.
- `web/tests/live-terminal.spec.js`: E2E test against live `wideboi server` testing PTY interaction.
