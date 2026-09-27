# Web UI Link Clicking and Copy & Paste Fixes

**Goal:** Enable clicking on URLs in the web UI terminal to open them in a browser, and fix copy & paste so it is reliable, supports standard shortcuts, and never clobbers the clipboard on click.

## Background & Problem

Filed as issue #296:
1. **Link Clicking:**
   - The web UI terminal renders on an HTML5 `<canvas>`.
   - There is no URL detection in terminal text.
   - Pointer events are captured and prevented; clicking on URLs does nothing.
   - Hovering over URLs provides no visual affordance (no pointer cursor or preview).
2. **Copy & Paste:**
   - Single click in an already focused pane falls into the pointerup copy branch when `focusOnClick` is false and `!dragged`. It computes `selectionText` for 1 cell and calls `navigator.clipboard.writeText(text)`, overwriting the user's system clipboard with a single character.
   - `<canvas>` has no native DOM text selection, and wideboi has no `document.addEventListener('copy')` handler. Pressing `Cmd+C` (macOS), `Ctrl+Shift+C`, or clicking browser Edit -> Copy fails to copy selected text.
   - `sendKeyboardInput` intercepts `Ctrl+V` on Linux/Windows, converts it to a terminal control character, and `forwardKeyEvent` calls `e.preventDefault()`. This stops the browser from ever firing the native `paste` event.
   - `Ctrl+Shift+C` and `Ctrl+Shift+V` are not supported.

## Requirements

### 1. Link Detection & Clicking
- Pure function `findUrlAt(pane: MsgPaneUpdate | undefined, point: CellPoint): { url: string; start: CellPoint; end: CellPoint } | undefined`:
  - Scans row `point.y` in `pane.lines`.
  - Reconstructs text and cell index mapping.
  - Matches `https?://...` URLs with common trailing punctuation stripped (e.g. `.,:;!?)'"]`).
  - Returns URL and cell span if `point.x` falls within the URL.
- On hover (`pointermove` when not dragging and pane is not mouse-tracking):
  - If a URL is under the pointer, set canvas cursor to `pointer` and `title` to the URL.
  - Otherwise, reset cursor and `title`.
- On click (`pointerup` when `!this.pointer.dragged` and not mouse-tracking):
  - If a URL is at `press.start`, open it via `window.open(url, '_blank', 'noopener,noreferrer')`.
  - Plain click to focus an unfocused pane (`focusOnClick`) does NOT trigger link opening.

### 2. Copy & Paste
- In `pointerup`:
  - If `this.pointer.focusOnClick && !this.pointer.dragged`: clear selection and focus pane.
  - If `this.pointer.dragged`: compute `selectionText` and copy to clipboard via `navigator.clipboard.writeText(text)`.
  - If `!this.pointer.dragged`: clear selection. **NEVER** write single-cell text to clipboard on click.
- Add `document.addEventListener('copy', ...)`:
  - If event originates from a form control, let native behavior run.
  - If a pane has an active selection, populate `e.clipboardData?.setData('text/plain', text)` and `e.preventDefault()`. Also update `navigator.clipboard.writeText` if available.
- Keyboard shortcuts:
  - `Cmd+C` (macOS): triggers native `copy` event, which reads active selection.
  - `Ctrl+Shift+C` (Linux/Windows): copies active selection.
  - `Ctrl+C` (Linux/Windows): if there is an active text selection, copy it to clipboard and clear selection; if no selection, forward `^C` (SIGINT) to terminal.
  - `Ctrl+V` (Linux/Windows): do not call `preventDefault()` or forward as terminal control key; let browser fire `paste` event.
  - `Ctrl+Shift+V` (Linux/Windows): read from clipboard and send as text input, or dispatch paste.
