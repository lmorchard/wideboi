# Mobile Web Command Menu & Palette Affordance Research

**Source:** https://github.com/lmorchard/wideboi/issues/293

## Codebase Findings

### 1. Mobile Interface Architecture
- On viewport width <= 480px (`@media (max-width: 480px)`):
  - `.toolbar` is hidden (`display: none`).
  - `<wideboi-mobile-bar>` (`web/src/components/mobile-bar.ts`) is displayed at the top (`.mobile-bar`).
  - Terminal pane strip runs full width (`.pane-strip.mobile`).
  - `<div class="mobile-dock">` is displayed at the bottom (`.mobile-input-bar` with Draft/Direct toggle, input, Send, and Macros button).
  - Tapping Macros reveals `.mobile-macros-sheet` with terminal keys (`.mobile-keys`: Esc, Tab, Ctrl, arrows, ⌫, ↵) and custom user macros.

### 2. Wideboi KeyRouter and Actions
- Desktop users use keyboard shortcuts prefixed by `prefix` (default `Ctrl+B` or configured prefix) to manage sessions:
  - `prefix + Space`: Palette (`wideboi palette --caller-pane=<id>`)
  - `prefix + :`: Prompt (`wideboi prompt --caller-pane=<id>`)
  - `prefix + n`: New Column / Split (`VerbType.NEW_COLUMN`)
  - `prefix + x`: Kill Pane (`VerbType.KILL_PANE`)
  - `prefix + /`: Search (`startSearch()`)
  - `prefix + w`: Cycle Width (`VerbType.CYCLE_WIDTH`)
  - `prefix + c`: Toggle Cards Layout (`setLayoutMode`)
  - `prefix + f`: Toggle Follow PTY (`toggle_follow_pty`)
  - `prefix + ,`: Settings (`toggleSettings()`)
  - `prefix + ?`: Help (`toggleHelp()`)
- Mobile touch users have no physical keyboards and virtual mobile keyboards do not support chording/prefix key sequences reliably.
- Currently, `<wideboi-mobile-bar>` only provides:
  - `‹` Previous pane
  - Pane select `<select>`
  - Zoom controls (`-`, `%`, `+`)
  - `›` Next pane
  - `⚙` Settings button

### 3. Palette & Prompt Invocation
- In `web/src/wideboi-app.ts:702-715`:
  ```typescript
  prompt: (_action, e) => {
    this.client?.send({
      case: 'splitRequest',
      value: { command: `wideboi prompt --caller-pane=${this.focusedPaneId}`, afterPaneId: this.focusedPaneId, keep: false, cwd: '' },
    });
    e.preventDefault();
  },
  palette: (_action, e) => {
    this.client?.send({
      case: 'splitRequest',
      value: { command: `wideboi palette --caller-pane=${this.focusedPaneId}`, afterPaneId: this.focusedPaneId, keep: false, cwd: '' },
    });
    e.preventDefault();
  },
  ```
- This splitRequest asks the server to launch the palette/prompt TUI pane attached to the session after the caller pane.

### 4. Component Structure & Styling Conventions
- Components live in `web/src/components/`:
  - `mobile-bar.ts`: Custom element `<wideboi-mobile-bar>`
  - `settings-dialog.ts`: Custom element `<wideboi-settings>`
- Lit elements import `wideboiAppStyles` from `../wideboi-app.styles` and use CSS custom properties:
  - `--wb-bg-toolbar`, `--wb-border`, `--wb-border-divider`, `--wb-bg-btn`, `--wb-focus`, `--wb-fg-primary`.
- Accessibility conventions: `role="dialog"`, `aria-modal="true"`, `aria-labelledby`, `aria-label` on buttons.

### 5. Existing Tests
- `web/tests/mobile.spec.ts`: Playwright tests running at mobile viewport (`width: 390, height: 700`, `hasTouch: true`, `isMobile: true`).
- `web/src/components/mobile-bar.ts` has event-driven interface emitting CustomEvents (`bubbles: true, composed: true`).
