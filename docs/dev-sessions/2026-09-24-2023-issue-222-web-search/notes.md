# Notes: Browser Pane-History Search UI (#222)

## 2026-09-24: Session Summary
- Branch: `issue-222-web-search`
- Worktree: `.worktrees/issue-222-web-search`
- Completed implementation for issue #222:
  - Created pure search module `web/src/search.ts` with `findHistoryMatches`, `computeScrollTarget`, `navigateIndex`, `createSearchSession`, `applySnapshot`, `cancelSearch`, `liveSearch`, and `formatSearchStatus`.
  - Added unit test coverage in `web/src/search.test.ts` for Unicode runes (CJK Kanji, emoji, multibyte UTF-8), soft-wrapped physical lines, match count, scroll offset clamping, wrap-around navigation, and status formatting.
  - Added search routing (`{ type: 'search' }`) to `KeyRouter` (`web/src/key-router.ts`) triggered by Prefix + `/`. Unit tested in `web/src/key-router.test.ts`.
  - Integrated hybrid bottom status search overlay into `WideboiApp` (`web/src/wideboi-app.ts`):
    - Overlay contains search text input with auto-focus, match status indicator, prev/next navigation buttons, keep, restore, and live buttons.
    - Triggered via Prefix + `/`, `Ctrl+F` / `Cmd+F`, and toolbar "Search" button.
    - Captures input keystrokes locally; never leaks typing to the child terminal pane.
    - Listens for `historySnapshot` server messages, searches rows, selects latest match, and dispatches anchored `MsgScroll`.
    - Handles navigation (`n` / `N` / buttons), keep (`Enter` / Keep button), restore on cancel (`Escape` / Restore button), and live bottom view (`Ctrl+G` / Live button).
    - Cleans up search session on pane focus change or pane close.
  - Resolved Shadow DOM event retargeting bug: `e.composedPath()[0]` is required to detect `HTMLInputElement` when listening on `document` from outside shadow root.
  - Adjusted toolbar padding/gap to ensure 7 toolbar items fit without triggering toolbar line-wrap discrepancies between card and scroll layout modes on narrow viewports.
  - Added comprehensive Playwright integration tests in `web/tests/search.spec.js` covering:
    - Overlay activation via Prefix + `/`, toolbar, and `Ctrl+F`
    - Keystroke isolation from child process and `historyRequest` dispatch
    - Match count and scroll target computation
    - Navigation next/prev with button/key controls
    - No matches state and Escape restore
    - Unicode and soft-wrapped rows
    - Independent multi-client isolation (client A and client B search separate queries without mutual interference)
  - Updated documentation in `web/src/wideboi-app.ts` (help overlay table), `README.md`, and `docs/MANUAL.md`.
  - Verified full test suite (`make check`) passes completely: Go tests, Vitest, Playwright browser tests, smoke test, attach check, race check, and golden snapshots.
