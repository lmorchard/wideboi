# Research: Browser Keyboard and Terminal Parity (#148)

## 1. Current Browser Input Architecture

- **`web/src/input.ts:18-34` (`sendKeyboardInput`)**:
  - Intercepts keydown events.
  - Rejects composing/dead/meta/unidentified keys.
  - Maps named navigation keys (`ArrowUp`, `ArrowDown`, `ArrowRight`, `ArrowLeft`, `Insert`, `Delete`, `PageUp`, `PageDown`, `Home`, `End`, `Backspace`, `Tab`, `Enter`, `Escape`) to ultraviolet extended codes or ASCII codes.
  - Resolves base letter (`event.code`) to lowercase ASCII code point, extracts modifier bits (`shift: 1`, `alt: 2`, `ctrl: 4`).
  - Sends `{ case: 'input', value: { paneId, key: { text, mod, code, shiftedCode: 0, baseCode, isRepeat } } }`.
  - Returns `true` if handled, causing `wideboi-app.ts` to call `e.preventDefault()`.

- **`web/src/input.ts:36-40` (`sendTextInput`)**:
  - Used for `paste` (`web/src/wideboi-app.ts:557-562`) and `compositionend` (`web/src/wideboi-app.ts:564-567`).
  - Encodes UTF-8 bytes and sends `{ case: 'input', value: { paneId, data } }`.

- **`web/src/wideboi-app.ts:476-555` (`setupKeyboard`)**:
  - Hardcodes prefix check: `if (e.ctrlKey && e.key === 'b') { this.inPrefixMode = true; e.preventDefault(); return; }`.
  - In prefix mode:
    - `Escape` or `Ctrl+C` cancels prefix mode.
    - Matches keys: `h`/`left` (focus left), `l`/`right` (focus right), `n` (new column), `w` (cycle width), `x` (kill pane), `a` (smart jump), `p` (grow width), `o` (shrink width), `y` (move left), `u` (move right), `tab` (focus last), `j` (scroll down), `k` (scroll up).
    - Unhandled keys exit prefix mode.
  - Missing in web prefix mode:
    - Layout switch (`c`): toggles between card layout and scroll strip.
    - Column jump (`0`-`9`): 1-9 focus column at 1-based index, 0 focuses last column.
    - Help (`?`): overlay/dialog showing shortcut bindings.
    - Double prefix: typing the prefix twice in CLI sends the literal prefix key to the child. Currently in web, prefix followed by `ctrl+b` is not explicitly sending `ctrl+b` as a key.

## 2. Server Wire Messages and Protocol

- `wideboi.proto`:
  - `VerbType`: `TOGGLE_CARDS` (7), `FOCUS_LEFT`, `FOCUS_RIGHT`, `NEW_COLUMN`, `CYCLE_WIDTH`, `KILL_PANE`, `SMART_JUMP`, `GROW_WIDTH`, `SHRINK_WIDTH`, `MOVE_LEFT`, `MOVE_RIGHT`, `FOCUS_LAST`.
  - Layout switching (`VerbType.TOGGLE_CARDS` or client-side `layoutMode`) in the browser is client-side view state (per PR #210 and issue #125).
  - Web client does NOT detach or quit (per design decision).

## 3. Browser Acceptance Check Setup

- Currently `web/tests/lifecycle.spec.js` and `web/tests/cards.spec.js` mock WebSocket in the page.
- For live server testing:
  - Can spawn `wideboi server` with a temporary socket and port (`--websocket 127.0.0.1:<port> --websocket-token <token>`).
  - Alternatively, a Playwright test can launch a real `wideboi server` process before running, open the browser to the web app, connect using the URL + token, and type into the live shell and full-screen terminal app (e.g. `cat`, `python3`, or interactive commands), verifying real terminal output on the canvas/DOM.
