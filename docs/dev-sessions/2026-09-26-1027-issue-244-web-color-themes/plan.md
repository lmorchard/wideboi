# Web Client Color Themes Implementation Plan

**Goal:** Implement selectable and persisted dark/light color themes for the Wideboi web client across terminal rendering and UI chrome.

**Approach:** Define a typed theme catalog with ANSI palettes and UI color tokens in `themes.ts`. Parameterize `colors.ts` and `PanePainter` with the active theme, drive Lit element styles via CSS custom properties, and add a persisted selector in `wideboi-app`.

**Tech stack:** TypeScript, Lit 3, HTML5 Canvas 2D, Vitest, Playwright.

---

## Phase 1: Theme Definitions & Color Decoding

Create the theme catalog and update color decoding to use theme palettes.

**Files:**
- Create: `web/src/themes.ts`
- Modify: `web/src/colors.ts` — parameterize `decodeColor` with optional `Theme`
- Create: `web/src/themes.test.ts` — test theme retrieval, defaults, and color decoding

**Key changes:**
- `Theme` interface and `DEFAULT_THEMES` registry:
  - 8 curated themes: `dark`, `one-dark`, `nord`, `dracula`, `solarized-dark`, `solarized-light`, `github-light`, `monokai`.
  - Properties: `id`, `name`, `isDark`, `terminal` (bg, fg, cursor, cursorText, selection, scrollFooterBg, scrollFooterFg, ansi[16]), `ui` (CSS variables for app, toolbar, inputs, borders, focus).
- `decodeColor(color: ColorData | undefined, isBg: boolean, theme?: Theme): string`:
  - Resolves default fg/bg and ANSI 0-15 from `theme.terminal`.

**Verification — automated:**
- [x] `cd web && npm test src/themes.test.ts` passes — **84 passed across 13 test files**
- [x] `cd web && npm test` passes — **84 passed across 13 test files**

---

## Phase 2: Terminal Pane Rendering with Themes

Connect the theme to `PanePainter` and `WideboiPane` so canvas rendering responds to theme changes.

**Files:**
- Modify: `web/src/pane-painter.ts` — add `setTheme(theme: Theme)`, draw background if `bg !== theme.terminal.background`, use theme cursor/selection/scroll colors
- Modify: `web/src/wideboi-pane.ts` — add `@property({ attribute: false }) theme: Theme`, forward to painter, use CSS variables for borders and card labels
- Modify: `web/src/pane-rendering.test.ts` — test theme propagation to painter

**Key changes:**
- `PanePainter.setTheme(theme: Theme)` invalidates the frame and updates canvas drawing colors.
- `wideboi-pane` styles use CSS variables: `--wb-border-divider`, `--wb-focus`, `--wb-card-label-bg`, `--wb-card-label-fg`, etc.

**Verification — automated:**
- [x] `cd web && npm test src/pane-rendering.test.ts` passes — **85 passed across 13 test files**
- [x] `cd web && npm test` passes — **85 passed across 13 test files**

---

## Phase 3: Web UI Chrome Variables, Theme Selector, and Persistence

Style `wideboi-app` with CSS custom properties, add the toolbar selector, and persist choice in `localStorage`.

**Files:**
- Modify: `web/src/wideboi-app.ts` — apply theme CSS variables to `:host`, add `theme` select to toolbar, pass `theme` to `wideboi-pane`, persist to `localStorage`
- Create / Modify: `web/src/wideboi-app.test.ts` or add tests in `web/src/client.test.ts` / `web/src/lifecycle.test.ts`

**Key changes:**
- Toolbar selector:
  ```html
  <label for="theme-select">Theme:</label>
  <select id="theme-select" aria-label="Color theme" @change=${this.handleThemeSelect}>
    ${listThemes().map(t => html`<option value=${t.id} .selected=${t.id === this.themeId}>${t.name}</option>`)}
  </select>
  ```
- CSS variables applied to `:host.style` on theme change.
- Persistence via `localStorage.getItem('wideboi.theme')` and `localStorage.setItem('wideboi.theme', id)`.

**Verification — automated:**
- [x] `cd web && npm run lint` passes (`tsc --noEmit`) — **clean**
- [x] `cd web && npm test` passes — **85 passed**
- [x] `make quick` passes — **all targets passed**

---

## Phase 4: Verification and Browser Testing

Full suite check and end-to-end acceptance.

**Verification — automated:**
- [x] `make quick` passes — **all targets passed**
- [x] `make web-test` passes — **85 passed**
- [x] `make web-accept` passes (Playwright browser acceptance) — **30 passed**
- [x] `make check` passes — **all check-targets passed (fmt-check, lint, seam-check, test, web-test, web-accept, race, verify-exit, smoke, attach-check)**
