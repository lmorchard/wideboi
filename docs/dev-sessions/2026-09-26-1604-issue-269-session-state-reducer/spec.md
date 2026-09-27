# Spec: Extract Session-State Reducer and Break Up wideboi-app.ts

**Goal:** Decompose `web/src/wideboi-app.ts` into a pure, vitest-tested session state reducer, extracted styles, focused subcomponents, unified preferences, shared test fixtures, and side-effect-free rendering.

**Source:** GitHub Issue #269

## Current State

`web/src/wideboi-app.ts` is ~2697 lines (`wideboi-app.ts:1-2697`), containing:
- ~850 lines of Lit CSS in `static styles` (`wideboi-app.ts:26-878`).
- Intricate layout snapshot and pane closure focus-and-stack reconciliation in `connectClient` (`wideboi-app.ts:1405-1510`), currently only tested via Playwright browser tests.
- Direct component state mutation inside `render()` (`this.cardFirst = layout.first`, `wideboi-app.ts:2230`).
- Top-level module-import side-effect calling `consumeLinkToken(window.location, window.history)` (`wideboi-app.ts:19`).
- Ad-hoc `localStorage` access across 11+ locations (`wideboi-app.ts:936, 959, 974, 1003, 1542, 2036, 2045, 2071, 2160, 2517`, `pane-state.ts:9, 10, 18-20`) with inconsistent key naming schemes (`wideboi.theme`, `wideboi.prefix`, `wideboi.macros` vs `wideboi:fontSize`, `wideboi:fontFamily`).
- Large inline subcomponent templates inside `wideboi-app.ts`: mobile toolbar/input controls (`wideboi-app.ts:2344-2457`), settings dialog (`wideboi-app.ts:2608-2670`), search bar (`wideboi-app.ts:2261-2313`).
- 100+ line keyboard event switch in `setupKeyboard` (`wideboi-app.ts:1612-1718`) and divergent search key handling (`wideboi-app.ts:1583-1609` vs `2277-2292`).
- Fake WebSocket mock implementation copy-pasted across 8+ Playwright `.spec.js` test files (`cards.spec.js`, `horizontal-viewport.spec.js`, `vertical-viewport.spec.js`, `pane-borders.spec.js`, `settings.spec.js`, `search.spec.js`, `lifecycle.spec.js`, etc.).

## Desired End State

1. **`web/src/session-state.ts` Pure Reducer:**
   - Pure state data structure `SessionState` representing: `columns`, `activePanes`, `focusedPaneId`, `previousFocusId`, `stackFocusId`, `focusTransition`, `pendingFocusId`, `displayWidths`, `paneStatuses`, `paneTitles`, `paneMetadata`, `macros`.
   - `initialSessionState(): SessionState`
   - Pure reducer `reduceSession(state: SessionState, event: SessionEvent, context?: SessionContext): SessionState` (or actions for `layoutSnapshot`, `paneClosed`, `paneCreated`, `splitResponse`, `paneMetadata`, `macrosSnapshot`, `focusPane`, `setDisplayWidth`, `resetDisplayWidths`, `finishFocusStack`).
   - Comprehensive Vitest unit tests in `web/src/session-state.test.ts` verifying all focus and stack reconciliation edges (card stacking, closed active pane, tail deletion, custom display width preservation/pruning, metadata pruning).

2. **Component & Styles Extraction:**
   - `web/src/wideboi-app.styles.ts`: static Lit CSS extracted out of `wideboi-app.ts`.
   - `web/src/components/mobile-bar.ts` (`<wideboi-mobile-bar>`): custom element for the mobile bar and mobile dock.
   - `web/src/components/settings-dialog.ts` (`<wideboi-settings-dialog>`): custom element for font, theme, prefix, and layout settings.
   - `web/src/search-controller.ts`: encapsulate search state, query input, commit/navigate/cancel/live actions, and unified search keyboard rules.
   - Keyboard action dispatch table mapping key-router action types to handler methods.

3. **Unified Preferences (`web/src/prefs.ts`):**
   - Typed get/set accessors with safe `try/catch` wrapping:
     - `theme` (default: `'wideboi'`)
     - `prefix` (default: `'ctrl+b'`)
     - `macros` (default: `[]`)
     - `fontSize` (default: `14`)
     - `fontFamily` (default: `'monospace'`)
   - Canonical key naming scheme (e.g. `wideboi:<key>`) with transparent fallback migration from legacy keys (`wideboi.theme`, `wideboi.prefix`, `wideboi.macros`).
   - Tested in `web/src/prefs.test.ts`.

4. **Test Fixtures & Playwright TypeScript Migration:**
   - `installMockWebSocket(page: Page)` helper in `web/tests/browser-fixture.ts`.
   - Migrate `.spec.js` files to `.spec.ts` using the shared helper.

5. **Clean Side Effects & Lifecycle:**
   - Compute `cardFirst` in `willUpdate(changedProperties)` instead of mutating inside `render()`.
   - Defer `consumeLinkToken` to component connection/initialization (`firstUpdated` or `connectedCallback`) rather than module import time.

## Design Decisions

- **Decision:** Keep `SessionState` pure without references to DOM elements, animation promises, or WebSocket clients.
  - **Why:** Pure state transitions can be tested exhaustively and deterministically in Vitest in milliseconds without DOM or browser overhead.
  - **Rejected:** Putting UI animations or WebSocket `.send()` inside the reducer. Side effects belong in the component layer responding to state changes.

- **Decision:** Use `wideboi:<name>` as the canonical localStorage key format, transparently migrating `wideboi.<name>` keys.
  - **Why:** `wideboi:fontSize` and `wideboi:fontFamily` already use colon separators. Supporting both formats on read and writing the canonical key ensures zero data loss for existing users.
  - **Rejected:** Breaking existing stored settings or abruptly dropping legacy key support.

- **Decision:** Keep subcomponents cleanly decoupled using Lit properties and custom DOM events (e.g. `@settings-change`, `@close`, `@focus-pane`, `@zoom-step`).
  - **Why:** Follows standard web component architecture; enables individual testing and keeps `wideboi-app.ts` as an orchestrator.
  - **Rejected:** Passing the entire `WideboiApp` instance down into child components.

- **Decision:** Migrate Playwright specs to TypeScript incrementally using `browser-fixture.ts`.
  - **Why:** Playwright has built-in TypeScript support; typechecking the specs prevents silent attribute/payload drift.

## Patterns to Follow

- Pure layout and focus calculations: `web/src/focus.ts:3-14` and `web/src/card-layout.ts:21-53`.
- Lit element property binding and event dispatch: `web/src/pane-element.ts`.
- Subprotocol and binary message decoding: `web/src/client.ts:31-70`.
- Vitest unit testing: `web/src/focus.test.ts` and `web/src/card-layout.test.ts`.

## What We're NOT Doing

- Not changing the wire protocol or protobuf schemas (`internal/protocol/wirepb/`).
- Not altering backend server behavior or Go codebase.
- Not altering visual design, themes, or CSS styling values (pure refactoring / extraction).
- Not altering the desktop manager or Electron/Wails code.

## Open Questions

None. All 5 steps are clear and approved.
