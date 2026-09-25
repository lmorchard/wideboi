# Research: Browser Pane-History Search UI (#222)

## 1. Browser Client Architecture
- **Keyboard routing:** `setupKeyboard` (`web/src/wideboi-app.ts:679-753`) intercepts keys. Ignored on input/select/button elements (`wideboi-app.ts:689-690`) and IME (`wideboi-app.ts:691`). Passes to `KeyRouter.handle(e)` (`web/src/key-router.ts:82-165`).
- **Prefix mode:** Default `ctrl+b`. When prefix is active:
  - `j`/`k` sends `MsgScroll` with `delta: -10`/`10` (`wideboi-app.ts:703`).
  - `?` or `Shift+/` triggers `toggle_help` (`key-router.ts:117-120`).
  - `/` is currently unhandled in prefix mode (drops out as `ignore`).
- **Focus:** `focusedPaneId` in `wideboi-app.ts:309`. Reconciled via `reconcileFocus` (`web/src/focus.ts:5-20`). `focusPane(id)` updates state, animates cards, and calls `revealFocus()`.
- **Scrolling:**
  - Server-managed offset: `scrollOffset` and `scrollbackLen` in `MsgPaneUpdate`/`MsgPanePatch`. Bottom footer rendered by `PanePainter.draw()` (`web/src/pane-painter.ts:179-193`).
  - Inner DOM viewport: `wideboi-pane.ts` has `.viewport` with `overflow-y: auto`, `followBottom` tracking (`wideboi-pane.ts:87-93`).

## 2. Terminal Client Search Mechanics
- **Search flow (`internal/client/search.go`):**
  - `StartSearch()`: captures `pu.ScrollOffset` as `priorOffset`, `pu.ScrollbackLen` as `priorHistoryLen`, enters input mode.
  - `SearchCommit()`: sends `MsgHistoryRequest{PaneID: s.paneID}`, marks `waiting = true`, `pending = 0`.
  - `applyHistoryLocked()`: on `MsgHistorySnapshot`, matches lines via `findHistoryMatches(snapshot.Rows, s.query)`.
  - Row matching: `strings.Index`, column calculated via `utf8.RuneCountInString`.
  - Target offset: `target := snapshot.ScrollbackLen - match.row`, clamped to `[0, snapshot.ScrollbackLen]`.
  - Initial selection: latest match `len(matches) - 1`.
  - Navigation: `s.selected = (s.selected + s.pending + len(matches)) % len(matches)`.
  - Scroll update: `MsgScroll{PaneID: s.paneID, SetAbsolute: true, Offset: target, AnchorHistory: true, HistoryLen: snapshot.ScrollbackLen}`.
  - Cancel/Restore: sends `MsgScroll` back to `priorOffset` with `AnchorHistory: priorOffset > 0`. Esc restores; Ctrl+g or Enter keep or jump to live.

## 3. Server History & Scroll Protocol
- **History Request/Snapshot (`internal/server/server.go:523-526, 813-815`):**
  - `MsgHistoryRequest{PaneID}` returns `MsgHistorySnapshot{PaneID, ScrollbackLen, Rows}`.
  - Rows ordered: oldest scrollback (0) -> newest scrollback (`sbLen-1`) -> top screen (`sbLen`) -> bottom screen (`sbLen+screenRows-1`).
  - Trailing spaces trimmed per row (`internal/server/term/grid.go:707`).
- **Scroll Anchoring (`internal/server/server.go:741-752`):**
  - When `SetAbsolute` and `AnchorHistory` are true, server computes `newOffset += maxOffset - m.HistoryLen` and updates `paneSbLens[tp][paneID] = maxOffset`.

## 4. Test Infrastructure
- **Vitest (`web/src/*.test.ts`):** Fast unit tests. Uses `FakeWebSocket`, mocks `CanvasRenderingContext2D`, exercises `KeyRouter`, `PaneStore`, etc.
- **Playwright (`web/tests/*.spec.js`):**
  - Mock WebSocket fixtures: `cards.spec.js`, `lifecycle.spec.js` via `browser-fixture.ts` with `window.testSockets`.
  - Multi-context testing: Playwright `browser.newContext()` allows multiple independent client pages connected to same server or mock sockets.

## 5. Documentation
- `README.md:51-72`: Essential keys table (`/` search).
- `docs/MANUAL.md:179-195`: Search mode details (`/`, `Enter`, `n`, `N`, `Esc`, `Ctrl+g`).
- `docs/MANUAL.md:367-372`: Web client features section.
- `web/src/wideboi-app.ts:1052-1081`: Web help overlay table.
