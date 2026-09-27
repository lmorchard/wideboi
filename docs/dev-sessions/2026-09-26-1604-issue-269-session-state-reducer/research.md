# Research: Issue 269 - web: extract a session-state reducer and break up wideboi-app.ts

*Research conducted via documentarian subagent across `issue-269-session-state-reducer` worktree.*

### 1. WebSocket Messages in `connectClient`, State Updates, Focus and Stack Reconciliation

In `web/src/wideboi-app.ts` (lines 1401–1550), incoming messages trigger `client.onMessage = (message) => { ... }` switching on `message.msg.case`:

- **`layoutSnapshot`** (lines 1405–1448):
  - **State updated:**
    - `focusedPaneId` via `reconcileFocus(this.columns, snapshot.columns, this.focusedPaneId)` (line 1409).
    - `displayWidths` merged with snapshot columns (preserving custom widths unless `followPTY` is true; non-existent pane IDs pruned) (lines 1410–1417).
    - `columns` set to `snapshot.columns` (line 1418).
    - `activePanes` mapped from `snapshot.columns.map(c => c.paneId)` (line 1419).
    - `paneStatuses` and `paneTitles` assigned from snapshot (lines 1431–1432).
    - `paneMetadata` pruned of inactive pane IDs (lines 1434–1442).
  - **Focus & Stack Reconciliation:**
    - `reconcileFocus` (`web/src/focus.ts:3–14`) keeps the focused pane if still present in next columns; otherwise focuses the pane at the same column index (or previous adjacent pane if at the tail); returns `0` if empty.
    - If `focusedPaneId !== previousFocus`, `stackFocusId = this.focusedPaneId` and `focusTransition++` (lines 1420–1422). If `stackFocusId` is not null but no longer in `activePanes`, it resets to `this.focusedPaneId` (lines 1423–1425).
    - If `pendingFocusId` is now in `activePanes`, calls `this.focusPane(this.pendingFocusId)` and clears `pendingFocusId` (lines 1426–1429).
    - Clears `previousFocusId` if not in `activePanes` (line 1430).
    - Post-update (`this.updateComplete`): triggers `animateReorder`, `revealFocus`, and `sendResizeIfChanged` (lines 1443–1447).
- **`paneCreated`** (lines 1450–1452): sets `this.pendingFocusId = message.msg.value.paneId`.
- **`splitResponse`** (lines 1453–1457): sets `this.pendingFocusId = message.msg.value.paneId` if present.
- **`paneUpdate`** (lines 1458–1462): calls `this.panes.update(...)`, `this.requestUpdate()`, and `this.revealPendingCursor(paneId)`.
- **`panePatch`** (lines 1463–1469): calls `this.panes.patch(...)`; if patch fails, sends client resync message `{ case: 'paneResync', value: { paneId } }`; requests update and reveals pending cursor.
- **`paneClosed`** (lines 1470–1513):
  - **State updated:** removes pane from `paneZooms` (line 1472); closes in `panes` (line 1474); filters `columns` (line 1476, 1479) and updates `activePanes` (line 1480); updates `focusedPaneId` via `reconcileFocus` (lines 1477, 1481); resets `previousFocusId` / `pendingFocusId` if matching (lines 1487–1488); clears `searchState` if on this pane (lines 1489–1492); clears `pointer` / `selectedPane` (lines 1493–1494); deletes from `paneMetadata` (lines 1495–1499).
  - **Focus & Stack Reconciliation:**
    - If `stackFocusId === closedId` or focus changed, sets `stackFocusId = this.layoutMode === 'cards' && !this.mobile && focusChanged ? null : nextFocus` and increments `focusTransition` (lines 1483–1486).
    - Post-update: runs `animateReorder`; if focus changed and not mobile, focuses input and calls `finishFocusStack(nextFocus, transition)` for cards layout; waits for animations then calls `revealFocus` (lines 1500–1510).
- **`paneMetadata`** (lines 1514–1518): stores metadata record into `this.paneMetadata`.
- **`focusPane`** (lines 1519–1522): calls `this.focusPane(message.msg.value.paneId)`.
- **`historySnapshot`** (lines 1523–1526): calls `this.handleHistorySnapshot(message.msg.value)`.
- **`macrosSnapshot`** (lines 1527–1548): sets `this.macros` and persists them to `localStorage.setItem('wideboi.macros', ...)`.

---

### 2. CSS Styles Structure in `web/src/wideboi-app.ts`

- Declared statically using Lit's `css` tagged template literal on the component class: `static styles = css`...`` (`web/src/wideboi-app.ts:26–878`).
- Uses CSS custom properties defined in themes (`--wb-bg-app`, `--wb-fg-primary`, `--wb-border`, `--divider-width`, etc.) with fallback values.
- Sections within `static styles` include:
  - Host and shell container (`:host`, `.terminal-shell`: lines 27–47).
  - Title and search status bars (`.title`, `.status`, `.status.search-bar`, `.search-input`, `.search-btn`: lines 48–144).
  - Horizontal pane container and cards layout styles (`.pane-strip`, `.pane-strip.cards`, `.card-count`: lines 145–205).
  - Split dividers and drag indicators (`.pane-divider`: lines 206–241).
  - Toolbar, connection bar, settings, macro editor, modal overlays, help dialog, mobile toolbar/input controls (`.toolbar`, `.mobile-bar`, `.mobile-dock`, `.modal-backdrop`, `.settings-dialog`: lines 242–877).

---

### 3. Interaction with `localStorage` and URL Parameters

- **URL Parameters & Location:**
  - `consumeLinkToken(window.location, window.history)` is called at module scope (`web/src/wideboi-app.ts:19` and defined in `web/src/token.ts:3–22`). It extracts a `token` from `location.hash` (preferred) or `location.search`, deletes the `token` parameter from the URL, and invokes `history.replaceState(...)`.
  - `desktopSession` reads query parameter `session` (`window.location.search`): `new URLSearchParams(window.location.search).get('session')` (`web/src/wideboi-app.ts:20`).
  - Render stats check query parameter via `statsEnabled(window.location.search)` (`web/src/wideboi-app.ts:885`).
  - WS connection URL building uses `window.location.protocol`, `window.location.host`, and appends `?session=...` if `desktopSession` exists (`web/src/wideboi-app.ts:916–917, 2681`).
  - `connectClient` also extracts `token` from hash/search fallback on user-provided `this.wsUrl` before stripping it (`web/src/wideboi-app.ts:1376–1378`).
- **`localStorage` Keys Accessed:**
  - `'wideboi.theme'`:
    - Read in `currentTheme` getter (`web/src/wideboi-app.ts:936`).
    - Written in `setTheme()` (`web/src/wideboi-app.ts:959`).
  - `'wideboi.prefix'`:
    - Read in `prefix` getter (`web/src/wideboi-app.ts:974`).
    - Written in `handlePrefixChange()` (`web/src/wideboi-app.ts:2160`).
  - `'wideboi.macros'`:
    - Read in constructor (`web/src/wideboi-app.ts:1003`).
    - Written in `onMessage` (`macrosSnapshot`) (`web/src/wideboi-app.ts:1542`).
    - Written in `saveMacro()` (`web/src/wideboi-app.ts:2036, 2045`).
    - Written in `deleteMacro()` (`web/src/wideboi-app.ts:2071`).
    - Written in `handleMacroFileImport()` (`web/src/wideboi-app.ts:2517`).
  *(Note: `'wideboi:fontSize'` and `'wideboi:fontFamily'` are accessed by `web/src/pane-state.ts:9–20`, not directly in `wideboi-app.ts`.)*

---

### 4. `render()` Mutations of Component State and Usage of `cardFirst`

- **Mutations inside `render()` (`web/src/wideboi-app.ts:2220–2230`):**
  - Line 2228–2229 computes `layout = cards ? cardLayout(...) : undefined`.
  - Line 2230 directly mutates:
    ```ts
    if (layout) this.cardFirst = layout.first;
    ```
- **How `cardFirst` is used in layout and rendering:**
  - Initialized to `0` at class declaration (`web/src/wideboi-app.ts:1016`).
  - Passed into `cardLayout(displayColumns, this.focusedPaneId, viewportCols, this.cardFirst, stackFocusId)` as `previousFirst` (`web/src/wideboi-app.ts:2228–2229`).
  - Inside `cardLayout` (`web/src/card-layout.ts:21–53`), `cardFirst` (as `previousFirst`) anchors the index of the first visible card in the card deck:
    - If `focusedIndex < first`, `first` adjusts left to `focusedIndex` (`card-layout.ts:28`).
    - If `focusedIndex > last`, `first` adjusts right (`card-layout.ts:31`).
    - Computes `layout.first` and offsets `left = (index - first) * CARD_OVERLAP_COLS` (`card-layout.ts:47`).
  - Placements generated by `cardLayout` determine each pane's inline CSS `left`, `z-index`, and `visibility` (`web/src/wideboi-app.ts:2236–2239`).
  - Overflow counts `layout.hiddenLeft` and `layout.hiddenRight` render indicator badges `+<count>` (`web/src/wideboi-app.ts:2256–2257`).

---

### 5. Structure of Unit Tests vs Browser/E2E Tests & Fake WebSocket / Mock Server Connections

- **Unit Tests (`web/src/*.test.ts` via Vitest):**
  - Use in-memory global stubs via `vi.stubGlobal('WebSocket', FakeWebSocket)` or spy objects.
  - Examples:
    - `web/src/lifecycle.test.ts:8–18`: Defines a class `FakeSocket` with `onopen`, `onclose`, `onmessage`, `protocol = 'wideboi.v14'`, and stubs `globalThis.WebSocket`. Tests dispatch binary messages directly via `old.onmessage?.({ data: closed.slice().buffer })`.
    - `web/src/client.test.ts:11–16, 29–32, 38–47, 61–70`: Injects mock `FakeWebSocket` instances to verify subprotocol negotiation (`wideboi.v14`), reconnection filtering, and protobuf message decode timing.
    - `web/src/macros.test.ts:29, 68` and `web/src/input.test.ts:10`: Pass `{ send: vi.fn() } as unknown as WideboiClient` directly to test client function calls without instantiating sockets.
- **Browser/E2E Tests (`web/tests/*.spec.js` via Playwright):**
  - **Injected Client-side Mock Socket (`page.addInitScript`):**
    Used across `cards.spec.js:4–19`, `horizontal-viewport.spec.js:10–25`, `vertical-viewport.spec.js:10–25`, `pane-borders.spec.js:4–19`, `settings.spec.js:4–19`, `search.spec.js:4–26`, and `lifecycle.spec.js:4–20`.
    - `page.addInitScript()` runs before page scripts load, replacing `window.WebSocket` with a fake class.
    - Instances record themselves into `window.testSockets = []`.
    - Methods implemented:
      - `send(data)` stores Uint8Array data into `this.sent`.
      - `open()` sets `readyState = 1` and triggers `this.onopen?.()`.
      - `close()` sets `readyState = 3` and triggers `this.onclose?.()`.
      - `message(bytes)` invokes `this.onmessage?.({ data: bytes.buffer })`.
    - Tests trigger messages from the simulated server via helper `serverBytes` imported from `/tests/browser-fixture.ts:6–8` (which encodes Protobuf `ServerMessage` definitions into binary `Uint8Array`).
    - Outgoing messages captured in `sent` are evaluated using helper `clientMessages` (`/tests/browser-fixture.ts:10–12`).
  - **Real Server Subprocess (`web/tests/live-terminal.spec.js:26–52`):**
    - Spawns the compiled Go server binary in `test.beforeAll`.
    - Verifies HTTP readiness via `https.get`, then navigates Playwright directly to `https://127.0.0.1:${port}/#token=${token}` to run live end-to-end interactions over actual WebSockets.
