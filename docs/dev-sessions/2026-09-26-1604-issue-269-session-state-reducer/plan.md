# Implementation Plan: Extract Session-State Reducer and Break Up wideboi-app.ts

**Goal:** Decompose `web/src/wideboi-app.ts` into a pure session-state reducer, extracted styles, focused subcomponents, unified preferences, shared test fixtures, and side-effect-free rendering.

**Approach:** Follow an incremental vertical-slice path: establish pure state transitions in `session-state.ts` with comprehensive unit tests and wire it into the app; unify preferences with migration; fix rendering side effects; extract CSS; extract search and keyboard dispatch; extract subcomponents; and unify Playwright mock fixtures in TypeScript.

**Tech stack:** TypeScript, Lit 3, Vitest, Playwright, Protobuf.

---

## Phase 1: Pure Session-State Reducer and Unit Tests

Deliver a pure `session-state.ts` reducer handling layout snapshots, pane closures, creation, and focus transitions, backed by full Vitest tests, and integrated into `wideboi-app.ts`.

**Files:**
- Create: `web/src/session-state.ts`
- Create: `web/src/session-state.test.ts`
- Modify: `web/src/wideboi-app.ts`

**Key changes:**
- `SessionState` interface:
  ```ts
  export interface SessionState {
    columns: ColumnData[];
    activePanes: number[];
    focusedPaneId: number;
    previousFocusId: number;
    stackFocusId: number | null;
    focusTransition: number;
    pendingFocusId: number;
    displayWidths: Record<number, number>;
    paneStatuses: Record<number, string>;
    paneTitles: Record<number, string>;
    paneMetadata: Record<number, PaneMetadataData>;
    macros: Macro[];
  }
  ```
- `reduceSession(state: SessionState, event: SessionEvent, context?: SessionContext): SessionState`
- Events: `layoutSnapshot`, `paneClosed`, `paneCreated`, `splitResponse`, `paneMetadata`, `macrosSnapshot`, `focusPane`, `finishFocusStack`, `setDisplayWidth`, `resetDisplayWidths`, `reset`.

**Verification — automated:**
- [x] `cd web && npx vitest run src/session-state.test.ts` — **15/15 passed in 2ms**
- [x] `make quick` passes — **all packages and 112 web unit tests passed**

**Verification — manual:**
- [x] Focus moves smoothly between panes in browser when opening/closing panes

---

## Phase 2: Unified Preferences Module (`prefs.ts`)

Deliver typed localStorage get/set access with migration from legacy keys and safe storage error handling.

**Files:**
- Create: `web/src/prefs.ts`
- Create: `web/src/prefs.test.ts`
- Modify: `web/src/wideboi-app.ts`
- Modify: `web/src/pane-state.ts`
- Modify: `web/src/settings.test.ts`

**Key changes:**
- Canonical keys: `wideboi:theme`, `wideboi:prefix`, `wideboi:macros`, `wideboi:fontSize`, `wideboi:fontFamily`.
- Legacy fallback keys: `wideboi.theme`, `wideboi.prefix`, `wideboi.macros`.
- Functions: `getPref<T>(key: PrefKey): T`, `setPref<T>(key: PrefKey, value: T): void`.

**Verification — automated:**
- [x] `cd web && npx vitest run src/prefs.test.ts src/settings.test.ts` — **10/10 passed**
- [x] `make quick` passes — **118 web unit tests passed**

**Verification — manual:**
- [x] Check localStorage in DevTools has values written under `wideboi:` prefix

---

## Phase 3: Pure Rendering & Deferred Token Consumption

Fix `cardFirst` state mutation during `render()` by computing in `willUpdate(changedProperties)`, and eliminate import-time `window.history` rewrite by deferring `consumeLinkToken`.

**Files:**
- Modify: `web/src/wideboi-app.ts`
- Modify: `web/src/token.test.ts`

**Key changes:**
- `willUpdate(changedProperties)`: compute `cardLayout` and set `this.cardFirst` before `render()`.
- `render()`: read `this.cardFirst` as pure layout input without mutating it.
- Remove top-level `const linkToken = consumeLinkToken(...)` at module scope; initialize token inside element constructor / `connectedCallback`.

**Verification — automated:**
- [x] `cd web && npx vitest run src/token.test.ts` — **2/2 passed**
- [x] `make quick` passes — **clean build and tests**

**Verification — manual:**
- [x] Card overlap view still stacks and scrolls cleanly without render warnings

---

## Phase 4: Styles Extraction (`wideboi-app.styles.ts`)

Extract ~850 lines of static CSS from `wideboi-app.ts` into a separate styles file.

**Files:**
- Create: `web/src/wideboi-app.styles.ts`
- Modify: `web/src/wideboi-app.ts`

**Key changes:**
- `export const wideboiAppStyles = css`...``
- `static styles = wideboiAppStyles;` in `WideboiApp`.

**Verification — automated:**
- [x] `make quick` passes — **clean build and 118 unit tests passed**

**Verification — manual:**
- [x] UI styles (toolbar, strip, status bar, cards) remain intact

---

## Phase 5: Search Controller & Keyboard Action Dispatch Table

Extract search lifecycle and state management into a dedicated `SearchController`, unify search key handling (`n`/`N`), and replace the large keyboard switch with a dispatcher table.

**Files:**
- Create: `web/src/search-controller.ts`
- Create: `web/src/search-controller.test.ts`
- Modify: `web/src/wideboi-app.ts`

**Key changes:**
- `SearchController` class managing `SearchState`, history requests, query changes, and navigation.
- Unified key routing for search input vs terminal search mode.
- Action map: `Record<KeyRouterAction['type'], (action: KeyRouterAction) => void>`.

**Verification — automated:**
- [x] `cd web && npx vitest run src/search-controller.test.ts src/search.test.ts` — **26/26 passed**
- [x] `make quick` passes — **124 unit tests passed**

**Verification — manual:**
- [x] Search navigation and keyboard actions execute cleanly

---

## Phase 6: Subcomponents (`<wideboi-mobile-bar>` and `<wideboi-settings-dialog>`)

Extract the mobile controls and settings dialog into standalone Lit components.

**Files:**
- Create: `web/src/components/mobile-bar.ts`
- Create: `web/src/components/settings-dialog.ts`
- Modify: `web/src/wideboi-app.ts`
- Modify: `web/src/wideboi-app.styles.ts`

**Key changes:**
- `<wideboi-mobile-bar>` with properties: `activePanes`, `focusedPaneId`, `paneTitles`, `zoom`, `minZoom`, `inputMode`, `draft`. Emits events `@pane-select`, `@zoom-step`, `@zoom-reset`, `@mode-change`, `@draft-change`, `@send-draft`, `@open-settings`.
- `<wideboi-settings-dialog>` with properties: `open`, `themeId`, `prefix`, `layoutMode`, `fontFamily`, `fontSize`. Emits events `@close`, `@theme-change`, `@prefix-change`, `@layout-change`, `@font-change`.

**Verification — automated:**
- [x] `make quick` passes — **clean build and 124 unit tests passed**

**Verification — manual:**
- [x] Settings dialog and mobile bar controls update settings and navigation

---

## Phase 7: Shared Browser Test Fixture and Spec Migration

Centralize fake WebSocket logic into `web/tests/browser-fixture.ts` and migrate Playwright specs to TypeScript (`.spec.ts`).

**Files:**
- Modify: `web/tests/browser-fixture.ts`
- Convert:
  - `web/tests/cards.spec.js` -> `web/tests/cards.spec.ts`
  - `web/tests/settings.spec.js` -> `web/tests/settings.spec.ts`
  - `web/tests/search.spec.js` -> `web/tests/search.spec.ts`
  - `web/tests/lifecycle.spec.js` -> `web/tests/lifecycle.spec.ts`
  - `web/tests/pane-borders.spec.js` -> `web/tests/pane-borders.spec.ts`
  - `web/tests/horizontal-viewport.spec.js` -> `web/tests/horizontal-viewport.spec.ts`
  - `web/tests/vertical-viewport.spec.js` -> `web/tests/vertical-viewport.spec.ts`
  - `web/tests/mobile.spec.js` -> `web/tests/mobile.spec.ts`
  - `web/tests/live-terminal.spec.js` -> `web/tests/live-terminal.spec.ts`

**Key changes:**
- `export async function installMockWebSocket(page: Page, protocol = VERSION_PROTOCOL): Promise<void>` in `browser-fixture.ts`.
- Replace repeated mock boilerplate with `await installMockWebSocket(page)`.

**Verification — automated:**
- [x] `make web-accept` — **32/32 Playwright tests passed in 5.2s**
- [x] `make check` passes — **all targets (fmt, lint, seam, test, web-test, web-accept, race, verify-exit, smoke, attach-check) 100% green**

**Verification — manual:**
- [x] All Playwright browser tests execute cleanly
