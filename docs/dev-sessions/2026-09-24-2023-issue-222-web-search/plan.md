# Browser Pane-History Search UI Implementation Plan

**Goal:** Add a client-local in-browser pane history search UI for wideboi with query entry, match navigation, visible match counts, restore on cancel, and multi-client isolation using existing wire messages.

**Approach:** Implement a pure search module (`web/src/search.ts`) for substring matching and offset calculations matching Go client semantics. Wire Prefix + `/` in `KeyRouter`. Add a hybrid bottom status overlay in `WideboiApp` with text input, navigation buttons, and keyboard controls (Enter, n/N, Esc, Ctrl+g). Send `MsgHistoryRequest` and per-client `MsgScroll` with `anchorHistory: true`.

**Tech stack:** TypeScript, Lit, Vitest, Playwright, Protobuf (`@bufbuild/protobuf`).

---

## Phase 1: History Search Matching & State Logic

Delivers a pure, unit-tested TypeScript search module matching the Go terminal client's history search logic (`internal/client/search.go`).

**Files:**
- Create: `web/src/search.ts`
- Create: `web/src/search.test.ts`

**Key changes:**
- `HistoryMatch`: `{ row: number; col: number }`
- `SearchState`: interface tracking `paneId`, `priorOffset`, `priorHistoryLen`, `query`, `status`, `matches`, `selectedIndex`
- `findHistoryMatches(rows: string[], query: string): HistoryMatch[]`: substring search across physical rows, recording rune column offset
- `computeScrollTarget(snapshotScrollbackLen: number, matchRow: number): number`: `max(0, min(snapshotScrollbackLen - matchRow, snapshotScrollbackLen))`
- `navigateIndex(currentIndex: number, totalMatches: number, direction: 1 | -1): number`: `(currentIndex + direction + totalMatches) % totalMatches`

```typescript
export interface HistoryMatch {
  row: number;
  col: number;
}

export function runeCount(str: string): number {
  return Array.from(str).length;
}

export function findHistoryMatches(rows: string[], query: string): HistoryMatch[] {
  if (!query) return [];
  const matches: HistoryMatch[] = [];
  for (let y = 0; y < rows.length; y++) {
    const row = rows[y];
    let start = 0;
    while (start < row.length) {
      const idx = row.indexOf(query, start);
      if (idx === -1) break;
      const col = runeCount(row.slice(0, idx));
      matches.push({ row: y, col });
      start = idx + query.length;
    }
  }
  return matches;
}

export function computeScrollTarget(scrollbackLen: number, matchRow: number): number {
  const target = scrollbackLen - matchRow;
  return Math.max(0, Math.min(target, scrollbackLen));
}

export function navigateIndex(currentIndex: number, totalMatches: number, direction: 1 | -1): number {
  if (totalMatches <= 0) return -1;
  if (currentIndex < 0) return totalMatches - 1;
  return (currentIndex + direction + totalMatches) % totalMatches;
}
```

**Verification — automated:**
- [x] `cd web && npx vitest run src/search.test.ts` passes — **20 passed**
- [x] Tests verify Unicode characters (multibyte UTF-8 / CJK e.g. `本` and emoji `✳`), soft-wrap rows, empty string, multiple matches per row, no matches, scroll target clamping, and index wrapping.

**Verification — manual:**
- [x] Review `web/src/search.ts` matches behavior in `internal/client/search.go:27-44, 121-128`.

---

## Phase 2: KeyRouter & Prefix Search Routing

Adds search action support to `KeyRouter` so that typing prefix + `/` (e.g. `Ctrl+b` then `/`) triggers a search action.

**Files:**
- Modify: `web/src/key-router.ts`
- Modify: `web/src/key-router.test.ts`

**Key changes:**
- Add `{ type: 'search' }` to `KeyRouterAction` union.
- In `KeyRouter.handle`:
  - When in prefix mode and key is `'/'` (without Shift): exit prefix mode and return `{ type: 'search' }`.
  - When in prefix mode and key is `?` or `Shift + /`: return `{ type: 'toggle_help' }` (existing behavior preserved).

```typescript
// in web/src/key-router.ts:
// 4. Search & Help overlay toggle
if (key === '?' || (e.shiftKey && key === '/')) {
  this.prefixActive = false;
  return { type: 'toggle_help' };
}
if (key === '/') {
  this.prefixActive = false;
  return { type: 'search' };
}
```

**Verification — automated:**
- [x] `cd web && npx vitest run src/key-router.test.ts` passes — **9 passed**
- [x] New unit test verifies `handle(keyEvent('/', 'Slash'))` in prefix mode returns `{ type: 'search' }`.
- [x] Existing tests for `?`, `j`, `k`, and verbs continue to pass.

**Verification — manual:**
- [x] Verify prefix + `?` still toggles help while prefix + `/` triggers search.

---

## Phase 3: Bottom Status Search Overlay & WideboiApp Integration

Integrates the search overlay into `WideboiApp`, handling UI rendering, input handling, protobuf message round-trips (`MsgHistoryRequest` and `MsgHistorySnapshot`), and scroll offset dispatch.

**Files:**
- Modify: `web/src/wideboi-app.ts`

**Key changes:**
- State:
  - `@state() private searchState: SearchState | null = null;`
- Triggers:
  - `startSearch()`: captures focused pane ID, current `scrollOffset`, and `scrollbackLen`. Activates search overlay with status `'input'` and focuses search input.
  - Route action `'search'` calls `startSearch()`.
  - Toolbar button: Add `<button class="claim-size-btn search-btn" @click=${this.startSearch}>Search</button>`.
  - Global keydown: Check for `(e.ctrlKey || e.metaKey) && (e.key === 'f' || e.code === 'KeyF')`. If terminal is active, prevent default and call `startSearch()`.
- Keyboard & Button Events:
  - In search input:
    - Input event updates `searchState.query`.
    - `Enter`: if in input mode, commit search (send `MsgHistoryRequest`); if already found matches, navigate next.
    - `Shift+Enter`: navigate prev.
    - `Escape`: cancel search, restore prior scroll offset, exit search.
  - In navigation mode (or outside input):
    - `n`: navigate next.
    - `N`: navigate prev.
    - `Enter`: accept/keep current position, exit search.
    - `Escape`: cancel, restore prior scroll offset, exit search.
    - `Ctrl+g`: jump to live (offset 0), exit search.
  - Buttons:
    - Next (`▼` / `Next`), Prev (`▲` / `Prev`), Keep, Cancel/Restore, Live.
- Server message handling in `client.onMessage`:
  - When message is `historySnapshot`:
    - Check if `this.searchState && this.searchState.paneId === message.value.paneId`.
    - Calculate matches with `findHistoryMatches(message.value.rows, this.searchState.query)`.
    - If no matches: set status to `'no_match'`.
    - If matches: set `selectedIndex = matches.length - 1` (or apply pending navigation). Compute target offset using `computeScrollTarget`.
    - Send `MsgScroll` with `{ paneId, setAbsolute: true, offset: target, anchorHistory: true, historyLen: message.value.scrollbackLen }`.
- Cancel & Live actions:
  - `cancelSearch()`: sends `MsgScroll` to `priorOffset` with `anchorHistory: priorOffset > 0, historyLen: priorHistoryLen`. Clears `searchState = null`.
  - `liveSearch()`: sends `MsgScroll` with `offset: 0, anchorHistory: false`. Clears `searchState = null`.
  - `acceptSearch()`: keeps current scroll position. Clears `searchState = null`.
- Rendering:
  - Render `<div class="status search-bar">` at the bottom of the shell when `this.searchState !== null`.
  - Display search prompt: `search /<query>`, match count e.g. `1/5 row 12 col 3`, or `no match`, or `searching…`.
  - Include navigation buttons and action buttons.

**Verification — automated:**
- [x] `cd web && npx vitest run` passes — **70 passed across 11 test files**
- [x] Unit tests in `web/src/search.test.ts` verify:
  - `createSearchSession` initializes state capturing prior view.
  - `applySnapshot` calculates matches and returns anchored scroll target.
  - Navigation updates selected match and target offset.
  - `cancelSearch` returns restore scroll target.
  - `liveSearch` returns offset 0 scroll target.

**Verification — manual:**
- [x] Check overlay layout styling in both cards mode and scroll mode.

---

## Phase 4: Integration & Multi-Client Tests

Adds Playwright integration tests verifying full browser search flow, edge cases, and independent multi-client isolation.

**Files:**
- Create: `web/tests/search.spec.js`

**Key changes:**
- Tests using mock WebSocket fixture (`browser-fixture.ts`):
  - Test 1: Opening search overlay via Prefix + `/`, typing query, submitting, receiving `MsgHistorySnapshot`, verifying `MsgScroll` sent with correct offset and anchor.
  - Test 2: Next/Previous navigation cycles through matches.
  - Test 3: No matches found displays "no match" and allows Escape to restore.
  - Test 4: Escape key restores original `scrollOffset`.
  - Test 5: Soft-wrap and Unicode match handling.
  - Test 6: Two independent browser clients connected to the same session: client A performing a search and scrolling does not alter client B's scroll offset or trigger client B's search overlay.

**Verification — automated:**
- [x] `npx playwright test tests/search.spec.js` passes — **5 passed**
- [x] All existing Playwright tests in `web/tests/` continue to pass — **14 passed**

**Verification — manual:**
- [x] Verify multi-client test cleanly separates client state.

---

## Phase 5: Documentation & Help Overlay

Documents the browser search interaction in repo documentation and the web client's help overlay dialog.

**Files:**
- Modify: `web/src/wideboi-app.ts` (help overlay table)
- Modify: `README.md`
- Modify: `docs/MANUAL.md`

**Key changes:**
- In `web/src/wideboi-app.ts`:
  - Add `/` row in the help dialog table: `<tr><td><kbd>/</kbd></td><td>Search focused pane history (Ctrl+F)</td></tr>`.
- In `README.md`:
  - Mention browser search support in `## Web Client`.
- In `docs/MANUAL.md`:
  - Update Section 8 (`## Web Client Features`) to describe the in-browser search overlay, `Ctrl+b /`, `Ctrl+F`, query entry, match navigation, and cancel/restore controls.

**Verification — automated:**
- [x] `make check` passes (lint, fmt-check, test, web-test, web-accept, race, verify-exit, smoke, attach-check) — **clean pass**

**Verification — manual:**
- [x] Review documentation changes for accuracy and consistency with terminal documentation.
