# Issue #148 Plan: Browser Keyboard, Actions, and Terminal Parity

## Phase 1: Key Router & Action Dispatcher (`web/src/key-router.ts`)

- Extract keyboard router logic out of `wideboi-app.ts` into a dedicated tested module `web/src/key-router.ts`.
- Configurable prefix parsing & matching:
  - Supports `ctrl+<a-z>` and `ctrl+space`.
  - Matches incoming `KeyboardEvent` against prefix.
- Control / prefix mode state machine:
  - Entering prefix mode on prefix key.
  - Double-prefix: if prefix key is pressed while in prefix mode, send literal key to child and exit prefix mode.
  - Exit on `Escape` or `Ctrl+C`.
  - Repeat mode: repeatable actions with `Ctrl` held stay in prefix mode.
  - Action dispatch:
    - Verbs: `FOCUS_LEFT` (`h`, `arrowleft`), `FOCUS_RIGHT` (`l`, `arrowright`), `NEW_COLUMN` (`n`), `CYCLE_WIDTH` (`w`), `KILL_PANE` (`x`), `SMART_JUMP` (`a`), `GROW_WIDTH` (`p`), `SHRINK_WIDTH` (`o`), `MOVE_LEFT` (`y`), `MOVE_RIGHT` (`u`), `FOCUS_LAST` (`tab`).
    - Scrolling: `j` (down), `k` (up).
    - Web actions:
      - `c`: `toggle_cards`
      - `1`-`9`, `0`: `focus_column` (1-9 = 1-based index, 0 = last column)
      - `?`: `toggle_help`
- Unit tests in `web/src/key-router.test.ts`.

## Phase 2: App Integration & Help Overlay (`web/src/wideboi-app.ts`)

- Integrate `KeyRouter` into `wideboi-app.ts`.
- Prefix configuration:
  - Persistent in `localStorage` under `wideboi.prefix` (fallback `'ctrl+b'`).
  - Expose in toolbar or UI so user can change it.
  - Update tips/help to reflect configured prefix.
- Implement missing actions in `wideboi-app.ts`:
  - `toggle_cards`: switches between `'cards'` and `'scroll'`.
  - `focus_column(n)`: switches focus to column at 1-based index, or last column for 0.
  - `toggle_help`: toggles a help overlay displaying shortcut reference.
- Add Help Modal / Overlay component or Lit template in `wideboi-app.ts`.

## Phase 3: Live Server Browser Acceptance Test (`web/tests/live-terminal.spec.js`)

- Start a real `bin/wideboi server` with a PTY session, temporary socket, and `--websocket 127.0.0.1:<port> --websocket-token <token>`.
- Playwright test:
  - Navigates to web app with URL and token.
  - Verifies connection and initial shell prompt.
  - Types commands into shell (e.g. `echo 'TEST_148_MARKER'`) and verifies canvas renders the output.
  - Starts interactive command or verifies input encoding, backspace, enter, and Ctrl+C.
  - Exercises prefix key chords (e.g. prefix + `c` for layout toggle, prefix + `?` for help).
  - Ensures server teardown after test finishes.

## Phase 4: Full Verification

- Run `make quick`.
- Run `make check`.
- Verify lint, formatting, seam-check, race checks, and exit checks.
