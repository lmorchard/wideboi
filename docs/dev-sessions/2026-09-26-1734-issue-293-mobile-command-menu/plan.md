# Mobile Web Command Menu & Palette Affordance Implementation Plan

## Phase 1: Mobile Bar Affordance
- [ ] Add `handleCommandMenu` and command button `<button class="mobile-cmd-btn" aria-label="Command menu" title="Command menu" @click=${this.handleCommandMenu}>⌘</button>` to `web/src/components/mobile-bar.ts`.
- [ ] Adjust styling for `.mobile-bar button` in `web/src/wideboi-app.styles.ts` so `min-width: 36px` allows all controls to fit without overflowing on 320px screens.
- [ ] Verify `mobile-bar` unit tests and appearance.

## Phase 2: Command Menu Component (`<wideboi-command-menu>`)
- [ ] Create `web/src/components/command-menu.ts` exporting `WideboiCommandMenu` custom element.
- [ ] Implement responsive dialog layout, keyboard dismissal (Esc), backdrop dismissal, and accessible markup (`role="dialog"`, `aria-modal="true"`, `aria-labelledby="command-menu-title"`).
- [ ] Render action items: Command Palette, Command Prompt, New Pane, Close Pane, Search, Toggle Layout, Toggle Follow PTY, Settings, Help.
- [ ] Add styling in `web/src/wideboi-app.styles.ts` for `.command-menu-overlay`, `.command-menu-dialog`, `.command-menu-list`, `.command-menu-item`, `.command-menu-badge`.

## Phase 3: Wire Command Menu into `<wideboi-app>`
- [ ] Extract `openPalette()` and `openPrompt()` helper methods in `WideboiApp`.
- [ ] Add `showCommandMenu: boolean` state to `WideboiApp`.
- [ ] Wire `@open-command-menu` on `<wideboi-mobile-bar>`.
- [ ] Render `<wideboi-command-menu>` when `showCommandMenu` is true, handling `@command` and `@close` events.
- [ ] Dispatch relevant verbs and actions (`openPalette`, `openPrompt`, `VerbType.NEW_COLUMN`, `VerbType.KILL_PANE`, `startSearch`, etc.).

## Phase 4: Verification & Tests
- [ ] Add unit test suite `web/src/components/command-menu.test.ts` verifying rendering, events, and dismissal.
- [ ] Add browser test in `web/tests/mobile.spec.ts` testing opening the command menu and triggering Command Palette and actions.
- [ ] Run `make web-test` and `make web-accept`.
- [ ] Run `make check`.
