# Plan: Consolidate Title and Status into Toolbar Pane Selector Buttons

## Phase 1: Pane Selector Tabs & Removal of Standalone Title/Status
Remove `<div class="title">` and static `<div class="status">` from `.terminal-shell`, remove the `<select id="focus-pane">` from `.toolbar`, and introduce `.pane-tabs` containing interactive pane buttons.

### Files
- `web/src/wideboi-app.ts`

### Key Changes
- In styles:
  - Remove `.title` styles. Remove static `.status` styles (while keeping `.status.search-bar`).
  - Add styles for `.pane-tabs`:
    ```css
    .pane-tabs {
      display: flex;
      align-items: center;
      gap: 4px;
      overflow-x: auto;
      scrollbar-width: none;
      max-width: 60%;
      flex: 0 1 auto;
    }
    .pane-tabs::-webkit-scrollbar { display: none; }
    .pane-tab {
      display: inline-flex;
      align-items: center;
      gap: 4px;
      background: var(--wb-bg-btn, #3c3c3c);
      color: var(--wb-fg-primary, #ccc);
      border: 1px solid var(--wb-border-divider, #555);
      border-radius: 3px;
      padding: 2px 7px;
      font-size: 12px;
      cursor: pointer;
      white-space: nowrap;
      user-select: none;
    }
    .pane-tab[aria-selected="true"] {
      background: var(--wb-bg-tab-active, #1e1e1e);
      border-color: var(--wb-focus, #007fd4);
      color: #fff;
    }
    .pane-tab .tab-title {
      max-width: 140px;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
    .pane-tab .tab-status {
      font-weight: bold;
    }
    .pane-tab .tab-status.working { color: var(--wb-ansi-blue, #569cd6); }
    .pane-tab .tab-status.needs-input { color: var(--wb-ansi-yellow, #dcdcaa); }
    .pane-tab .tab-status.done { color: var(--wb-ansi-green, #4ec9b0); }
    .pane-tab .tab-status.failed { color: var(--wb-ansi-red, #f44747); }
    .pane-tab .tab-scroll {
      color: var(--wb-fg-muted, #888);
      font-size: 11px;
    }
    ```
  - In `render()`:
    - Remove `<div class="title">` and `<div class="status">` from `.terminal-shell`.
    - In `.toolbar`, replace `<label for="focus-pane">` and `<select id="focus-pane">` with `.pane-tabs`:
      ```html
      <div class="pane-tabs" role="tablist" aria-label="Terminal Panes">
        ${repeat(this.activePanes, id => id, id => {
          const isFocused = id === this.focusedPaneId;
          const status = this.paneStatuses[id] ?? PaneStatus.IDLE;
          const fp = this.panes.get(id);
          const scrollInfo = (isFocused && fp && fp.scrollOffset > 0)
            ? `+${fp.scrollOffset}${fp.unreadOutput ? ' ⤓' : ''}`
            : '';
          const glyph = status === PaneStatus.WORKING ? '»'
            : status === PaneStatus.NEEDS_INPUT ? '!'
            : status === PaneStatus.DONE ? '✓'
            : status === PaneStatus.FAILED ? '✗' : '';
          const statusClass = status === PaneStatus.WORKING ? 'working'
            : status === PaneStatus.NEEDS_INPUT ? 'needs-input'
            : status === PaneStatus.DONE ? 'done'
            : status === PaneStatus.FAILED ? 'failed' : '';
          const title = this.paneTitles[id] || 'Terminal';
          return html`
            <button
              class="pane-tab"
              role="tab"
              data-pane-id=${id}
              aria-selected=${isFocused ? 'true' : 'false'}
              aria-label=${`Pane ${id}: ${title}`}
              @click=${() => this.focusPane(id)}
            >
              <span class="tab-id">[${id}]</span>
              <span class="tab-title">${title}</span>
              ${glyph ? html`<span class="tab-status ${statusClass}">${glyph}</span>` : ''}
              ${scrollInfo ? html`<span class="tab-scroll">[${scrollInfo}]</span>` : ''}
            </button>
          `;
        })}
      </div>
      ```

### Verification
- [x] `npm --prefix web test` passes
- [x] `npm --prefix web run lint` passes

---

## Phase 2: Search Bar Overlay in Toolbar
Overlay the search bar on top of the `.pane-tabs` in the toolbar while search is active, preserving all search controls and DOM classes.

### Files
- `web/src/wideboi-app.ts`

### Key Changes
- In `render()`:
  - If `this.searchState` is present, render `<div class="status search-bar">` directly inside the `.toolbar` overlaying or replacing the `.pane-tabs` container.
  - Style `.toolbar .status.search-bar`:
    ```css
    .toolbar .status.search-bar {
      position: absolute;
      top: 0;
      bottom: 0;
      left: 0;
      right: 0;
      z-index: 10;
      display: flex;
      align-items: center;
      gap: 0.4rem;
      padding: 0 0.6rem;
      background: var(--wb-bg-toolbar, #252526);
      font: 12px monospace;
    }
    ```
  - Ensure `.toolbar` has `position: relative;` so the search bar overlays cleanly.

### Verification
- [x] `npm --prefix web test` passes
- [x] `npx playwright test tests/search.spec.js` passes

---

## Phase 3: Update Integration Tests & Add Regressions
Update tests that previously interacted with `<select id="focus-pane">` to click the new `.pane-tab` buttons, and verify the removal of `.title` and static `.status`.

### Files
- `web/tests/lifecycle.spec.js`
- `web/tests/cards.spec.js`

### Key Changes
- In `web/tests/lifecycle.spec.js`:
  - Replace `page.getByRole('combobox', { name: 'Focus Pane:' }).selectOption('4')` with clicking the tab for Pane 4: `page.locator('.pane-tab[data-pane-id="4"]').click()`.
- In `web/tests/cards.spec.js`:
  - Replace `page.getByRole('combobox', { name: 'Focus Pane:' }).selectOption(target)` with clicking the tab: `page.locator(\`.pane-tab[data-pane-id="\${target}"]\`).click()`.
- Add test assertions verifying:
  - `.terminal-shell > .title` is not in the DOM.
  - `.pane-tab` displays correct title, status glyph, and focus attribute.

### Verification
- [x] `npm --prefix web run test:browser` passes all 31 tests.

---

## Phase 4: Full Verification & Gate Checks
Run full project linting, type-checking, Go tests, web unit tests, and Playwright acceptance suite.

### Verification
- [x] `npm --prefix web run build` succeeds
- [x] `npm --prefix web run lint` succeeds
- [x] `make quick` succeeds
- [x] `make check` succeeds
