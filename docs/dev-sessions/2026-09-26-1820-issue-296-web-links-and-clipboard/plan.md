# Implementation Plan: Web UI Link Clicking and Copy & Paste

## Phase 1: URL Detection Helper & Tests
- Implement `findUrlAt(pane: MsgPaneUpdate | undefined, point: CellPoint): { url: string; start: CellPoint; end: CellPoint } | undefined` in `web/src/pane-state.ts`.
- Reconstruct text of the line at `point.y`, mapping each character to its cell column.
- Use robust URL regex `https?://[^\s<>"'()\[\]{}|\\^`]+` and clean trailing punctuation (`.,:;!?)'"`).
- Test cases in `web/src/pane-rendering.test.ts`:
  - URL at start, middle, end of line.
  - Clicking on URL returns correct URL and spans.
  - Clicking outside URL returns undefined.
  - Trailing periods, parentheses, commas stripped properly.
  - URLs with wide characters earlier in the line correctly map column offsets.

## Phase 2: Fix Clipboard Overwrite and Selection API
- In `web/src/wideboi-pane.ts`:
  - Expose `getSelection()` and `selectedText()` from `WideboiPane`.
- In `web/src/wideboi-app.ts`:
  - In `pointerup`:
    - Only call `navigator.clipboard.writeText` when `this.pointer.dragged` is true.
    - If `!this.pointer.dragged`, do not write anything to the clipboard. Clear existing selection instead.

## Phase 3: Copy Event Listener and Clipboard Shortcuts
- In `web/src/wideboi-app.ts`:
  - Add `document.addEventListener('copy', ...)`:
    - If active selection exists, `e.clipboardData.setData('text/plain', selectedText)` and `e.preventDefault()`.
  - In keydown handler / router:
    - On `Ctrl+Shift+C`: copy selection and prevent default.
    - On `Ctrl+C`: if text is selected, copy selection, clear selection, and prevent default. If no text is selected, pass through as SIGINT.
    - On `Ctrl+V` (without Meta): do not preventDefault / do not send control key to child so browser `paste` event fires.
    - On `Ctrl+Shift+V`: read text from clipboard and send via `sendTextInput`.

## Phase 4: Link Hover and Click Handling
- In `web/src/wideboi-app.ts`:
  - In `pointermove` (when not dragging, mouse tracking is false):
    - Call `findUrlAt`. If URL found under pointer, set canvas cursor to `pointer` and `title` to the URL. If none, reset.
  - In `pointerup` (when `!this.pointer.dragged` and not mouse tracking and not `focusOnClick`):
    - Check `findUrlAt(press.pane.pane, press.start)`.
    - If URL found, open via `window.open(url, '_blank', 'noopener,noreferrer')`.

## Phase 5: Verification & Full Suite
- Unit tests via `npm test` in `web/`.
- Full project checks via `make check`.
- Validate no stray processes or session disruptions.
