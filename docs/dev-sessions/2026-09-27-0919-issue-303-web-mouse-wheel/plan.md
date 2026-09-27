# Implementation Plan: Web Client Mouse Wheel Tracking (Issue #303)

## Phase 1: Implementation
- Edit `web/src/wideboi-app.ts` in the `wheel` event handler:
  - Check if `!e.metaKey && this.panes.mouseTracking(pane.paneId)`.
  - In cards mode, if `pane.cardMode && pane.paneId !== this.focusedPaneId`, background cards shouldn't swallow wheel if they are slivers, or verify matching TUI client behavior.
  - Compute `{ x, y } = pane.cellAt(e.clientX, e.clientY)`.
  - Determine button: `e.deltaY < 0 ? 4 : 5` (4 = MouseWheelUp, 5 = MouseWheelDown).
  - Compute `mod = (e.shiftKey ? 1 : 0) | (e.altKey ? 2 : 0) | (e.ctrlKey ? 4 : 0)`.
  - Send `{ case: 'mouse', value: { paneId: pane.paneId, kind: MouseKind.WHEEL, x, y, button, mod } }`.
  - `e.preventDefault()`.

## Phase 2: Unit Testing
- Add tests in `web/src/` (or extend existing unit tests like `wideboi-app.test.ts` or component tests) verifying:
  - Wheel on a pane with `mouseTracking = true` sends `case: 'mouse'` with `MouseKind.WHEEL`, correct button (4 for up, 5 for down), and coordinates.
  - Wheel on a pane with `mouseTracking = false` sends `case: 'scroll'`.
  - Wheel with `metaKey = true` bypasses mouse tracking and falls back to `case: 'scroll'`.

## Phase 3: Verification
- Run `npm test` in `web/`.
- Run `make check` at root.
