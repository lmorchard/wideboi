# Research: Consolidate Title and Status into Toolbar Pane Selector Buttons

## Codebase Findings

### 1. Current Markup and Styling
- `web/src/wideboi-app.ts:2259–2260`:
  `<div class="title">${this.paneTitles[this.focusedPaneId] || (this.focusedPaneId ? `Pane ${this.focusedPaneId}` : '')}</div>`
  Height is 16.8px (line 49). Hidden on mobile `< 480px` via `.title { display: none; }` (line 575).
- `web/src/wideboi-app.ts:2315–2323`:
  `<div class="status">` renders formatted plain text when search is inactive:
  `focus: [${focusedPaneId} ★] [scroll +${scrollOffset}${unreadOutput ? ' ⤓' : ''}]  [1] IDLE  [2] WORKING ...`
  Height is 16.8px (line 49).
- `web/src/wideboi-app.ts:2327–2332`:
  `<label for="focus-pane">Focus Pane:</label>`
  `<select id="focus-pane" @change=${this.handlePaneSelect}>` with options `[${id}] ${title}`.
- `web/src/wideboi-app.ts:2261–2313`:
  When `this.searchState` is set, `<div class="status search-bar">` is rendered.
  In CSS (line 57-74), `.status.search-bar` has:
  `position: absolute; bottom: 0; left: 0; right: 0; height: 22px; z-index: 15;`

### 2. State & Data Available
- `this.activePanes: number[]`: open pane IDs.
- `this.focusedPaneId: number`: currently focused pane ID.
- `this.paneTitles: Record<number, string>`: map of pane IDs to titles.
- `this.paneStatuses: Record<number, PaneStatus>`: map of pane IDs to enum status.
  Enums (`wirepb.PaneStatus`): `0 = IDLE`, `1 = WORKING`, `2 = NEEDS_INPUT`, `3 = DONE`, `4 = FAILED`.
- `this.panes: Map<number, Pane>`: contains `scrollOffset` and `unreadOutput`.
- Focusing a pane: `this.focusPane(paneId)` or `this.handlePaneSelect(e)` which sets focus and reveals pane.

### 3. TUI Status and Badge Patterns (`internal/client/theme.go`)
- `StatusWorking` (1): `»` with working style (blue/cyan)
- `StatusNeedsInput` (2): `!` with needs-input style (yellow/orange)
- `StatusDone` (3): `✓` with done style (green)
- `StatusFailed` (4): `✗` with failed style (red)
- `StatusIdle` (0): ` ` (or subtle dot)

### 4. Tests Depending on Existing Structure
- `web/tests/cards.spec.js` (lines 80, 88, 105, 174):
  `page.getByRole('combobox', { name: 'Focus Pane:' }).selectOption(...)`
- `web/tests/lifecycle.spec.js` (lines 157, 196):
  `page.getByRole('combobox', { name: 'Focus Pane:' }).selectOption(...)`
- `web/tests/search.spec.js` (lines 60, 201, 253, 324, 327, 350, 373, 376):
  `page.locator('.status.search-bar')`
- No tests query `.title` or plain `.status`.
