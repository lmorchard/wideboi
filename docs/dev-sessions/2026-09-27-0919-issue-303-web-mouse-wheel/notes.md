# Dev Session Notes: Issue #303

## Context
Diagnosed why scroll wheel did not scroll buffer in nvim inside wideboi.
Root cause identified in web client (`web/src/wideboi-app.ts`): the `wheel` event listener unconditionally sends `MsgScroll` (history scrollback), completely ignoring whether the pane has active DEC mouse tracking (`mouseTracking = true`).
Because nvim runs in alternate screen buffer with 0 scrollback lines, `MsgScroll` does nothing.
Filed issue #303 to track this fix.

## Implementation Details
1. Added `sendWheelInput` helper in `web/src/input.ts`:
   - Translates DOM `WheelEvent` to `MouseKind.WHEEL` protobuf message with button 4 (`MouseWheelUp` for `deltaY < 0`) or button 5 (`MouseWheelDown` for `deltaY > 0`).
   - Encodes modifiers (`shift=1, alt=2, ctrl=4`).
2. Updated `wheel` listener in `web/src/wideboi-app.ts`:
   - If `!e.altKey && !e.metaKey && (pane.paneId === this.focusedPaneId || !pane.cardMode) && this.panes.mouseTracking(pane.paneId)`, forwards wheel event via `sendWheelInput`.
   - Preserves `Alt+wheel` / `Meta+wheel` bypasses to scroll wideboi history.
   - Preserves `hasVerticalOverflow` DOM scrolling behavior when the client window is shorter than the terminal grid.
3. Added unit tests in `web/src/input.test.ts`.
4. Added browser acceptance tests in `web/tests/mouse-wheel.spec.ts`.

## Verification
- `npm test` in `web/`: 19 files, 151 passed.
- `npx playwright test`: 46 passed.
- `make check`: 100% green across quick, unit, race, ptycheck, smoke, golden, attach-check, playwright.
