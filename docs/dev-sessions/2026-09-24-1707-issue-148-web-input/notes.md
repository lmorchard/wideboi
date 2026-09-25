# Session Notes: Issue #148 Browser Keyboard Parity

- **Worktree:** `.worktrees/issue-148-web-input`
- **Branch:** `issue-148-web-input`
- **Baseline:** `origin/main` commit `57c9030` passed `make quick` and `npm run test:browser`.

## Changes

1. **Configurable Client-Side Prefix & Router (`web/src/key-router.ts` & `web/src/key-router.test.ts`):**
   - Extracted keyboard routing out of `wideboi-app.ts` into a modular `KeyRouter`.
   - Supports configurable prefixes (`ctrl+b`, `ctrl+a`, `ctrl+space`), persisted in `localStorage` (`wideboi.prefix`).
   - Ignores bare modifier key events (`Control`, `Shift`, `Alt`, `Meta`) so that chords like `Control+b` do not prematurely cancel prefix mode.
   - Implemented double-prefix handling: pressing the prefix twice forwards the literal key to the child pane and exits prefix mode.
   - Repeat chords: holding `Ctrl` on repeatable verbs (`focus_left`, `focus_right`, `grow_width`, `shrink_width`, `move_left`, `move_right`) preserves prefix mode.
   - Esc or `Ctrl+C` cancels prefix mode.

2. **Web Action Parity in `web/src/wideboi-app.ts`:**
   - Layout toggle (`c`): toggles client presentation between cards and scroll strip.
   - Column jump (`1`–`9`, `0`): jumps focus to 1-based column position or last column (`0`).
   - Help overlay (`?`): opens an in-browser shortcut modal listing active prefix and key commands. Dismissible via Esc, `?`, or backdrop click.
   - Toolbar integration: added prefix selector and Help button; styled `.toolbar` with nowrap and ellipsis/hide on narrow screens so it does not wrap or push the terminal canvas.
   - Scoped out Detach and Quit from browser UI per design decision.

3. **Testing:**
   - Unit tests in `web/src/key-router.test.ts` (9 tests) verifying prefix matching, double prefix, repeat mode, column jumps, layout toggle, help, and modifier isolation.
   - Playwright mock lifecycle tests in `web/tests/lifecycle.spec.js` asserting prefix mode, double prefix sending literal key, column jump, layout toggle, help modal, and localStorage prefix selection.
   - Playwright live server acceptance test in `web/tests/live-terminal.spec.js` spawning a real `bin/wideboi server` with a PTY session over WebSockets, typing shell commands (`echo ...`), driving an interactive application (`cat`), sending `Ctrl+C`, and executing prefix actions against live panes.

## Verification

- `make quick`: passed (50 web unit tests and Go checks).
- `make check`: passed (including Playwright tests with Chromium against live backend, race detector, verify-exit, smoke, attachcheck).
