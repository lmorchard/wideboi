# Dev Session Notes: Web UI Link Clicking and Copy & Paste

- Date: 2026-09-26
- Branch: `issue-296-web-links-and-clipboard`
- Worktree: `.worktrees/issue-296-web-links-and-clipboard`
- Issue: #296

## Root Causes Identified
- Pointerup on already-focused pane was writing single-cell text to system clipboard unconditionally whenever `focusOnClick` was false and `!dragged`, destroying clipboard contents when clicking to focus/paste.
- `<canvas>` has no native DOM text selection, and without a `copy` event listener, `Cmd+C` / Edit->Copy failed to copy active selection.
- `Ctrl+V` on Linux/Windows was captured and prevented by `sendKeyboardInput`, preventing the native `paste` event.
- No URL detection or link clicking existed anywhere in the web UI.

## Changes Implemented
1. `findUrlAt(pane, point)` in `web/src/pane-state.ts`:
   - Scans row text, handling wide-character continuation cells.
   - Detects URLs wrapped across lines (when consecutive lines reach column boundary without spaces).
   - Robust URL regex matching with trailing punctuation cleaning and balanced parentheses preservation.
2. Link hover & click in `web/src/wideboi-app.ts` and `web/src/wideboi-pane.ts`:
   - Canvas cursor switches to `pointer` and `title` tooltip displays the URL on hover over detected links.
   - Pointerup without drag on a URL opens it in a new window/tab via `window.open(url, '_blank', 'noopener,noreferrer')`.
   - On mobile, tapping a link opens it.
3. Selection tracking and copy/paste fixes:
   - Added `getSelection()` and `selectedText()` to `WideboiPane`.
   - Pointerup now only copies to system clipboard if `this.pointer.dragged` is true. Plain clicks never write to clipboard, preserving clipboard contents.
   - Added `document.addEventListener('copy', ...)` to copy active selection to `e.clipboardData` and `navigator.clipboard`.
   - Added support for `Ctrl+Shift+C`, `Ctrl+C` (when selection active), `Ctrl+V` (passes through to native paste event), and `Ctrl+Shift+V` (reads clipboard text).

## Verification
- Unit tests added to `web/src/pane-rendering.test.ts` for URL detection and selection tracking.
- Playwright browser integration tests added in `web/tests/links-and-clipboard.spec.ts` covering:
  - Single click in focused pane does not overwrite clipboard.
  - Hovering over URL shows pointer cursor and title tooltip.
  - Clicking URL opens it in a new window.
  - Drag-select copies text to clipboard and `copy` event copies active selection.
- Full `make check` suite passed (fmt-check, lint, seam-check, unit tests, web-test, web-accept, race, verify-exit, smoke, attach-check).
