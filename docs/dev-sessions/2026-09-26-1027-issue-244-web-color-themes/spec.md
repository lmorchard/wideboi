# Web Client Color Themes Spec

**Goal:** Provide selectable, persisted color themes for the Wideboi web client covering terminal rendering and web chrome across both dark and light modes.

**Source:** https://github.com/lmorchard/wideboi/issues/244

## Current state
- The web client uses hardcoded dark colors throughout:
  - Terminal cell decoding defaults to `#1e1e1e` (bg) and `#d4d4d4` (fg) with fixed xterm-256 ANSI colors (`web/src/colors.ts:4-36`).
  - Terminal canvas painting optimizes out background fills assuming `#1e1e1e`, with cursor hardcoded to `#d4d4d4`/`#1e1e1e`, and selection to blue (`web/src/pane-painter.ts:153, 177, 187-194`).
  - Pane styling in `web/src/wideboi-pane.ts:10-84` has fixed `#1e1e1e` background and `#555` / `#007fd4` borders.
  - Web UI shell and toolbar in `web/src/wideboi-app.ts:23-740` use static dark hex values (`#1e1e1e`, `#252526`, `#3c3c3c`, `#ccc`, etc.).

## Desired end state
1. **Curated Themes in `web/src/themes.ts`**:
   - `dark` ("Default Dark", wideboi standard)
   - `one-dark` ("One Dark")
   - `nord` ("Nord")
   - `dracula` ("Dracula")
   - `solarized-dark` ("Solarized Dark")
   - `solarized-light` ("Solarized Light")
   - `github-light` ("GitHub Light")
   - `monokai` ("Monokai")
   Each theme defines:
   - Metadata: `id`, `name`, `isDark: boolean`.
   - Terminal palette: `background`, `foreground`, `cursor`, `cursorText`, `selection`, `scrollFooterBg`, `scrollFooterFg`, and `ansi: string[16]`.
   - UI chrome colors: CSS variable mapping for `--wb-bg-app`, `--wb-bg-toolbar`, `--wb-bg-pane`, `--wb-bg-input`, `--wb-bg-button`, `--wb-fg-primary`, `--wb-fg-muted`, `--wb-border`, `--wb-focus`, etc.
2. **Dynamic Terminal Painting**:
   - `decodeColor(color, isBg, theme?: Theme)` uses the active theme's `background`, `foreground`, and ANSI 16 palette (retaining 16–255 cube/grayscale algorithmically).
   - `PanePainter` accepts `theme: Theme`, invalidates on theme update, and checks `bg !== theme.terminal.background`. Cursor, selection, and scroll footer use theme colors.
   - `WideboiPane` exposes a `theme: Theme` property and passes it to its `PanePainter`.
3. **Reactive UI Chrome & Theme Selection**:
   - `WideboiApp` maintains active theme state, loaded from `localStorage.getItem('wideboi.theme')` (defaulting to `'dark'`).
   - Changing the theme writes to `localStorage` and updates CSS variables on `:host` (or applies a theme class/attribute) so all Lit components and canvas elements reflect the choice immediately without reloading.
   - Theme `<select id="theme-select">` added to toolbar (and mobile toolbar/dock).
4. **Verification**:
   - Unit tests for theme definitions, color decoding with custom theme palettes, and fallback handling.
   - Browser / component tests ensuring theme changes apply CSS variables, repaint canvases, and persist to `localStorage`.

## Design decisions
- **Decision:** Keep the 216-color cube (16-231) and 24-grayscale ramp (232-255) standard while customizing the 16 ANSI colors (0-15) per theme.
  - **Why:** Terminal applications rely on the first 16 ANSI colors for semantic meaning (black, red, green, yellow, blue, magenta, cyan, white + brights), while colors 16-255 are standard RGB mappings.
  - **Rejected:** Remapping all 256 colors per theme, which is unnecessary and deviates from standard terminal emulator behaviors.
- **Decision:** Drive web chrome with CSS custom properties on `wideboi-app` / `wideboi-pane`.
  - **Why:** Allows shadow DOM elements to inherit theme variables cleanly and makes theme definitions data-driven rather than writing duplicate CSS classes.
  - **Rejected:** Injecting separate `<style>` tags or writing massive duplicated stylesheet blocks.
- **Decision:** Persist theme in `localStorage['wideboi.theme']` as a client-side setting.
  - **Why:** Matches the established pattern in `wideboi-app.ts` for `wideboi.prefix` and `wideboi.macros`.

## Patterns to follow
- Settings storage pattern: `wideboi-app.ts:759, 1861`.
- Terminal decoding: `colors.ts:22-36`.
- Canvas rendering lifecycle: `pane-painter.ts:128-212`.
- Lit element styling with CSS properties: `wideboi-pane.ts:10-84`.

## What we're NOT doing
- User-authored custom JSON theme import/export (focus on polished built-ins).
- Sending theme preferences to the server or syncing across devices.
- Modifying TUI client themes (`internal/client/theme.go`) — this is scoped to the web client.

## Open questions
- None.
