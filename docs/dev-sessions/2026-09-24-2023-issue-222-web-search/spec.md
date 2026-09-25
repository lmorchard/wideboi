# Browser Pane-History Search UI Spec

**Goal:** Enable users of the wideboi web client to search the focused pane's scrollback history and visible screen using an in-browser status overlay UI without leaking search keystrokes to the child process or adding server-side search state.

**Source:** https://github.com/lmorchard/wideboi/issues/222

## Current state
- The Go terminal client supports client-side search over read-only history snapshots via `internal/client/search.go` and `cmd/wideboi/router.go`.
- The protobuf wire protocol supports `MsgHistoryRequest` (from client) and `MsgHistorySnapshot` (from server carrying `pane_id`, `scrollback_len`, and array of physical `rows` strings) (`internal/protocol/wirepb/wideboi.proto:225-230`).
- The web client handles keyboard input via `KeyRouter` (`web/src/key-router.ts:82-165`) and `setupKeyboard` (`web/src/wideboi-app.ts:679-753`). In `KeyRouter`, prefix mode supports verbs, digits, help (`?`), and layout (`c`), but does not handle `/`.
- The web client controls per-client scrolling by sending `MsgScroll` (`web/src/wideboi-app.ts:703`), and renders server-managed scroll offset and unread status in the status bar and `PanePainter` canvas footer (`web/src/pane-painter.ts:179-193`).
- Currently, the web client has no search UI, does not request or handle `MsgHistorySnapshot`, and cannot search scrollback text.

## Desired end state
- An interactive bottom status-bar overlay UI appears when search mode is activated for the focused pane.
- Activation triggers:
  1. Key router: Prefix (`Ctrl+b` by default) followed by `/` (matching the terminal client binding).
  2. Keyboard shortcut: `Ctrl+F` (or `Cmd+F` on macOS) when the wideboi terminal viewport is focused.
  3. Toolbar button: A "Search" button in the top toolbar alongside "Fit to Window" and "Help (?)".
- Bottom status overlay appearance and controls:
  - Sits at the bottom of the terminal shell (overlaying or replacing the normal status line while active).
  - Contains:
    - Search text input field pre-filled with current/previous query and auto-focused.
    - Status/match count indicator: e.g. `1/5 (row 42, col 10)`, `no match`, or `searching…`.
    - Navigation buttons: "Prev" (`▲` or `Prev`) and "Next" (`▼` or `Next`).
    - Action buttons: "Keep" (accept current view), "Restore" (cancel and return to pre-search view), and "Live" (jump to live bottom offset 0).
- Interaction & Keyboard Shortcuts:
  - While typing in the search input:
    - Characters modify the query. Keystrokes are completely isolated from the terminal pane process.
    - `Enter`: commits search / triggers find. If already found, navigates to next match.
    - `Shift+Enter`: navigates to previous match.
    - `Escape`: cancels search, restores previous view, and closes search overlay.
  - While navigating (either via buttons or if focus moves outside the input within search overlay):
    - `n`: navigates to next match.
    - `N`: navigates to previous match.
    - `Enter`: accepts/keeps current view and closes search overlay.
    - `Escape`: cancels/restores pre-search scroll offset and closes search overlay.
    - `Ctrl+g`: jumps to live terminal view (offset 0) and closes search overlay.
- Search Mechanics:
  - On commit/navigation, browser sends `MsgHistoryRequest{PaneID: focusedPaneId}` via `WideboiClient`.
  - On receipt of `MsgHistorySnapshot`:
    - Rows are searched for substring matches using standard JavaScript string matching (supporting full Unicode matching).
    - Physical soft-wrap rows are treated as separate rows (consistent with server emulator and terminal client).
    - Column is calculated as character/rune offset within the row.
    - Matches are indexed. Initial find selects the newest/latest match (bottom-most, matching terminal client behavior `len(matches) - 1`).
    - Navigation moves forward or backward through matches with modulo wrapping.
    - For the selected match, computes target scroll offset: `target = max(0, min(snapshot.scrollbackLen - match.row, snapshot.scrollbackLen))`.
    - Sends `MsgScroll` with `paneId: focusedPaneId`, `setAbsolute: true`, `offset: target`, `anchorHistory: true`, `historyLen: snapshot.scrollbackLen`.
  - On Cancel: sends `MsgScroll` with `setAbsolute: true`, `offset: priorOffset`, `anchorHistory: priorOffset > 0`, `historyLen: priorHistoryLen`.
  - On Live: sends `MsgScroll` with `setAbsolute: true`, `offset: 0`, `anchorHistory: false`.
- Multi-client isolation:
  - Search state (query, match index, active search overlay) is strictly local to each browser client instance.
  - Two independent browser sessions attached to the same wideboi server can search different queries or scroll to different locations without cross-client interference.
- Documentation:
  - Update `README.md`, `docs/MANUAL.md`, and the web help overlay dialog (`web/src/wideboi-app.ts`) to document the browser search UI, prefix + `/`, and `Ctrl+F`.

## Design decisions
- **Decision:** Hybrid bottom status overlay (with input, match indicators, and mouse buttons).
  - **Why:** Maintains the streamlined, familiar TUI status bar feel while providing full mouse usability for web users.
  - **Rejected:** Floating top-right widget (breaks visual consistency with terminal status layout).
- **Decision:** Triggerable via Prefix + `/`, toolbar button, and `Ctrl+F` / `Cmd+F`.
  - **Why:** Prefix + `/` preserves muscle memory from the terminal client; `Ctrl+F` aligns with standard web browser expectation; toolbar button provides discoverability for mouse users.
  - **Rejected:** Prefix + `/` only (poor web discoverability).
- **Decision:** Per-client query state using `MsgHistoryRequest` and `MsgScroll`.
  - **Why:** The server is stateless with respect to search. The server already provides atomic history snapshots and per-client anchored scroll offsets. Reusing these primitives keeps the backend unchanged and prevents client contention.
  - **Rejected:** Server-side search API (would add complexity, statefulness, and session tracking to the server).

## Patterns to follow
- Terminal search state and match logic: `internal/client/search.go:17-45, 110-137`.
- Web keyboard routing and prefix mode: `web/src/key-router.ts:82-165` and `web/src/wideboi-app.ts:679-753`.
- Scroll message sending and anchor parameters: `web/src/wideboi-app.ts:703` and `internal/client/search.go:134-137`.
- Toolbar and overlay styling: `web/src/wideboi-app.ts:76-120, 1052-1081`.
- Testing patterns:
  - Vitest unit tests: `web/src/key-router.test.ts`, `web/src/client.test.ts`, `web/src/pane-rendering.test.ts`.
  - Playwright integration tests: `web/tests/cards.spec.js`, `web/tests/lifecycle.spec.js`.

## What we're NOT doing
- We are not adding regex search or case-sensitivity toggles (matches terminal client substring search).
- We are not highlighting all matches simultaneously across the canvas (canvas painter renders server text grid; search jumps view to matching line, matching terminal client).
- We are not modifying the Go wire protocol or server search logic (the existing `MsgHistoryRequest`, `MsgHistorySnapshot`, and `MsgScroll` messages are sufficient).
- We are not modifying terminal client key bindings or behavior.

## Open questions
- None.
