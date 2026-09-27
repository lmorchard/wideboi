# Spec: Web Client Mouse Wheel Tracking (Issue #303)

## Problem

When running an application like Neovim (`nvim`), `htop`, or `less` inside wideboi, the mouse scroll wheel does not scroll the buffer when accessed via the web client (or desktop app).

## Findings

In `web/src/wideboi-app.ts`, the `wheel` event listener unconditionally sends `MsgScroll`:
```ts
const delta = e.deltaY > 0 ? -3 : 3;
this.client.send({ case: 'scroll', value: { paneId: pane.paneId, delta } });
```
1. Unlike pointer events (`pointerdown`, `pointermove`, `pointerup`), `wheel` never checks whether the target pane has mouse tracking active (`this.panes.mouseTracking(pane.paneId)`).
2. When mouse tracking is active, the web client should forward the wheel event as a `mouse` message (`MouseKind.WHEEL`) with appropriate button (`4` for wheel up, `5` for wheel down) and local cell coordinates (`pane.cellAt(e.clientX, e.clientY)`).
3. Because `nvim` runs in the alternate screen buffer without lines in wideboi scrollback, `MsgScroll` is clamped to 0 by the server and has no effect.

## Invariants & Design

1. **Mouse tracking check:**
   - If `!e.metaKey && this.panes.mouseTracking(pane.paneId)`:
     - Compute cell coordinates `{ x, y } = pane.cellAt(e.clientX, e.clientY)`.
     - Button: `e.deltaY < 0 ? 4 : 5` (matching ultraviolet / ANSI `MouseWheelUp = 4`, `MouseWheelDown = 5`).
     - Modifier flags: `(e.shiftKey ? 1 : 0) | (e.altKey ? 2 : 0) | (e.ctrlKey ? 4 : 0)`.
     - Send `{ case: 'mouse', value: { paneId: pane.paneId, kind: MouseKind.WHEEL, x, y, button, mod } }`.
     - Prevent default.
2. **Fallback:**
   - If mouse tracking is false (or metaKey bypass is held):
     - Fall back to existing scroll behavior: if `pane.hasVerticalOverflow && !e.altKey`, return; otherwise `this.client.send({ case: 'scroll', value: { paneId: pane.paneId, delta } })`.
3. **Card layout consideration:**
   - When in card layout, only the focused pane receives tracking mouse events (matching `pointerdown` logic: `pane.paneId === this.focusedPaneId || !pane.cardMode`), or if the pane under the wheel has mouse tracking.
   - Wait, in TUI client (`internal/client/mouse.go`):
     `// The wheel goes to whatever child is under it, focused or not -- the same rule as scrolling a shell's history.`
     So any pane with mouse tracking can receive wheel events. But in card layout, background panes only show slivers. We should check if `pane.cardMode && pane.paneId !== this.focusedPaneId` — if focused or not in cardMode (or if tracking), forward wheel event.
