# Mobile Web Command Menu & Palette Affordance Spec

**Goal:** Provide mobile web users with direct touch access to summon the wideboi command palette/prompt and execute wideboi session management actions (new pane, close pane, search, layout toggle, etc.) without requiring physical keyboard chords.

**Source:** https://github.com/lmorchard/wideboi/issues/293

## Problem
On mobile screens (narrow viewport < 480px), the desktop toolbar is hidden. Wideboi session management actions (palette, prompt, pane splitting/closing, layout mode, search, etc.) are ordinarily driven by keyboard shortcuts starting with a prefix chord (e.g. `Ctrl+B Space`). On mobile devices, virtual keyboards cannot easily send chords, and touch users have no hardware keys. Consequently, mobile users cannot summon the command palette or manage panes and sessions easily.

## Solution

1. **Mobile Bar Command Button**:
   - Add a command menu button (`⌘`, class `.mobile-cmd-btn`, `aria-label="Command menu"`, `title="Command menu"`) to `<wideboi-mobile-bar>`.
   - Adjust `.mobile-bar button` min-width to 36px so that all 6 controls (`‹`, `<select>`, zoom controls, `›`, `⌘`, `⚙`) fit cleanly even on 320px narrow screens.
   - Clicking `⌘` dispatches `open-command-menu` event to `<wideboi-app>`.

2. **Dedicated Command Menu Component (`<wideboi-command-menu>`)**:
   - Create a clean, touch-friendly modal dialog component in `web/src/components/command-menu.ts`.
   - Dialog features:
     - Header: "Wideboi Commands" and a close button (`✕`, `aria-label="Close command menu"`).
     - Backdrop click and Escape key dismisses the dialog.
     - List / grid of action buttons with clear icons, action titles, and keyboard shortcut badge hints:
       - **Command Palette** (`wideboi palette --caller-pane=<id>`) - `Space`
       - **Command Prompt** (`wideboi prompt --caller-pane=<id>`) - `:`
       - **New Pane** (New column) - `n`
       - **Close Pane** (Kill pane) - `x`
       - **Search History** (Pane scrollback search) - `/`
       - **Toggle Cards / Scroll** (Layout toggle) - `c`
       - **Toggle Follow PTY** - `f`
       - **Settings** - `,`
       - **Help** - `?`
     - Tapping any command dismisses the menu and dispatches the action immediately.

3. **Application Handlers in `<wideboi-app>`**:
   - Centralize `openPalette()` and `openPrompt()` methods on `WideboiApp` so both the keyboard router (`KeyRouterAction`) and the mobile command menu share the exact same split request invocation.
   - Handle all command menu actions cleanly using existing verb dispatching (`dispatchVerb`) and methods (`startSearch`, `setLayoutMode`, `toggleSettings`, `openHelp`).

4. **Testing**:
   - Unit tests for `<wideboi-command-menu>` and updated `<wideboi-mobile-bar>`.
   - Playwright browser tests in `web/tests/mobile.spec.ts` verifying that:
     - The command menu button is visible in narrow view.
     - Clicking the command menu button opens the command dialog.
     - Clicking "Command Palette" dispatches `splitRequest` with `wideboi palette --caller-pane=...`.
     - Clicking "New Pane" dispatches the new column verb.
     - Clicking close button or backdrop dismisses the dialog.
