# Issue #148 Spec: Browser Keyboard, Actions, and Terminal Parity

**Goal:** Provide configurable client-side prefix handling and action parity (layout switch, column jumps, help overlay) in the web client, backed by an end-to-end Playwright acceptance test running against a live wideboi server.

**Source:** GitHub issue #148

## Current state

- Web keyboard input (`web/src/input.ts:18-40`) sends `MsgInput` with `KeyData` or raw bytes for paste/IME.
- Hardcoded `ctrl+b` in `web/src/wideboi-app.ts:483`.
- Actions implemented in prefix mode: focus left/right, move left/right, cycle/grow/shrink width, kill pane, smart jump, focus last, scroll history up/down.
- Missing actions:
  - Layout switch (`c`): toggles between `cards` and `scroll` modes.
  - Column focus (`1`-`9`, `0` for last column).
  - Help overlay (`?`): modal/overlay displaying available keybindings.
  - Double prefix: typing the prefix twice should send the literal prefix key to the child pane.
- Playwright tests (`web/tests/lifecycle.spec.js`) only mock the WebSocket, not verifying real terminal PTY interactions.

## Desired end state

1. **Client-side Configurable Prefix:**
   - Prefix key is a client-side setting (stored in `localStorage` under `wideboi.prefix`, default `"ctrl+b"`).
   - In toolbar or connect/settings, user can inspect/configure prefix.
   - Double-pressing the prefix sends the literal key to the active pane and exits prefix mode.
2. **Action Parity in Web Client:**
   - `c`: toggles client layout between cards and scroll strip (`this.handleLayoutSelect` equivalent).
   - `1`–`9`: focuses column at 1-based index; `0`: focuses the rightmost/last column.
   - `?`: toggles an in-app help overlay showing prefix key, available shortcuts, and mouse interactions.
   - `esc` or `ctrl+c`: exits prefix mode.
   - Detach and Quit are explicitly scoped OUT for web clients (web clients should not kill the session or detach).
3. **Live Acceptance Check:**
   - A Playwright test runs against a live `wideboi server` with a PTY session.
   - Connects to the WebSocket server with token.
   - Types commands in a live shell (`echo hello-world`), verifies the output text rendered.
   - Launches a full-screen interactive command (e.g. `vi` or `python3` or `cat`) or verifies interactive input handling.
   - Exercises column jump and layout toggle via prefix chords.

## Design decisions

- **Decision:** Prefix is client-side configuration (`localStorage`).
  - **Why:** Avoids server-side session-wide lock-in; each web user can use a prefix suitable for their OS/browser shortcuts (e.g. `ctrl+a`, `ctrl+space`, `ctrl+b`).
  - **Rejected:** Sending prefix over wire protocol from server (rejected per discussion: client property).
- **Decision:** Scope out Detach (`d`) and Quit (`q`) from web client.
  - **Why:** Web sessions are observing or working clients that shouldn't inadvertently kill the server or panes. Closing the tab disconnects cleanly.
- **Decision:** Double-prefix sends literal key.
  - **Why:** Allows users to send `ctrl+b` (or their configured prefix) to their child shell or editor by typing it twice, matching the CLI behavior.
- **Decision:** Live Playwright test starts a dedicated `wideboi server` on an ephemeral port.
  - **Why:** Ensures true end-to-end verification of keyboard encoding, PTY transmission, and canvas rendering without relying on mocks.

## Patterns to follow

- CLI keybindings and actions in `internal/keys/keys.go:153-203`.
- Router prefix and double-prefix matching logic in `cmd/wideboi/router.go:90-130`.
- Lit element overlays and modals in `web/src/wideboi-app.ts`.

## What we're NOT doing

- Server-side protocol schema changes for keybindings or prefix.
- Function key additions (F1-F12) - deferred per discussion.
- Server shutdown (`MsgShutdown`) or detach (`MsgDetach`) from the web UI.

## Open questions

None.
