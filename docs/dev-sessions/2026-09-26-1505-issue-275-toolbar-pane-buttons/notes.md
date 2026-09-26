# Notes: Consolidate Title and Status into Toolbar Pane Selector Buttons

## What Was Done
- Removed standalone `<div class="title">` and static `<div class="status">` from `.terminal-shell`, reclaiming ~34px of vertical terminal space.
- Exploded the desktop `<select id="focus-pane">` into a row of interactive `.pane-tab` buttons in `.toolbar`.
- Each tab displays:
  - Focus indicator (`aria-selected="true/false"`, dot `●`/`○`, active border/background styling)
  - Pane ID `[id]`
  - Pane title with ellipsis truncation
  - Status glyph matching TUI: `»` (working, blue), `!` (needs input, yellow), `✓` (done, green), `✗` (failed, red)
  - Scroll & unread output indicator: `[+N ⤓]` when scrolled back
- Styled `.pane-tabs` to support horizontal scrolling (`overflow-x: auto; scrollbar-width: none;`) for handling sessions with many panes without wrapping or breaking the toolbar.
- Decoupled the search bar overlay:
  - Inside `.pane-tabs-wrapper`, `<div class="status search-bar">` overlays the tab strip when search is active, restoring the tabs when dismissed (`Esc`/accept).
  - Maintained `.status.search-bar` and sub-element class names for full backward-compatibility with search tests.
  - Added responsive rules for mobile search.
- Updated Playwright tests in `web/tests/`:
  - Replaced obsolete `getByRole('combobox', { name: 'Focus Pane:' })` interactions with `.pane-tab[data-pane-id="..."]`.
  - Added new test in `lifecycle.spec.js` asserting tabs display title, status glyphs, and focus state, and that `.title` and static `.status` are absent from `.terminal-shell`.

## PR Review Follow-ups (Copilot)
- **Tablist keyboard navigation**: Added `handleTabKeydown` supporting standard roving tabindex tablist keyboard interactions (`ArrowRight`/`ArrowDown`, `ArrowLeft`/`ArrowUp`, `Home`, `End`).
- **Accessible name enhancement**: Included status (working, needs input, done, failed) and scroll info in each tab's `aria-label` so assistive tech announces the full state.
- **Verification**: Updated `web/tests/lifecycle.spec.js` to test tablist keyboard navigation and rich `aria-label` values.

## Verification
- `npm --prefix web run lint`: PASSED (`tsc --noEmit`).
- `npm --prefix web test`: PASSED (15 test files, 93 unit tests).
- `npm --prefix web run test:browser`: PASSED (32 browser acceptance tests).
- `make quick`: PASSED (all Go packages, vetting, seam check, web tests).
- Rebuilt `web/dist` and verified `bin/wideboi` end-to-end.
