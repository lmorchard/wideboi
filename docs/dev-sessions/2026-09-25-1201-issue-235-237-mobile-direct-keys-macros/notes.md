# Dev Session Notes: Issues 235 & 237

- **Worktree:** `.worktrees/issue-235-237-mobile-direct-keys-macros`
- **Branch:** `issue-235-237-mobile-direct-keys-macros`
- **Session Dir:** `docs/dev-sessions/2026-09-25-1201-issue-235-237-mobile-direct-keys-macros`

## Context & Direction
- Tackling both #235 (mobile direct keys & Ctrl shortcuts) and #237 (mobile macro panel).
- Research conducted on input routing (`input.ts`), mobile layout (`wideboi-app.ts`), and config storage.
- Alignments with Les:
  - Input model: Direct mode + expanded Ctrl keys palette.
  - Macro storage: WebSocket Protobuf messages for server loading and persistence verbs, with local overrides/caching.
- Spec completed and verified against readiness checklist.

## Implementation Details
1. **Mobile Direct Input & Expanded Ctrl Palette (#235):**
   - Added `mobileInputMode: 'draft' | 'direct'` in `web/src/wideboi-app.ts` with toggle controls in the mobile dock.
   - In Direct mode, an active input target streams keyboard events immediately to the terminal without buffering.
   - Expanded on-screen Ctrl buttons when active: `C`, `D`, `Z`, `R`, `L`, `A`, `E`, `W`, `K`, `U`.
   - Tapping `[R]` sends `Ctrl+R` directly to the terminal.
2. **Macro Protocol & Server Backend (#237):**
   - Added `MacroStep`, `Macro`, `MsgMacrosSnapshot`, `MsgSaveMacros` to `wideboi.proto`.
   - Bumped `protocol.Version` to 12 (`wideboi.v12`).
   - Implemented Go codecs and wire tests in `internal/protocol/`.
   - Added TOML config loading and atomic persistence in `internal/config/` (`SaveMacrosFile`, `UserMacrosPath`).
   - Added server macro state, snapshot on attach, `MsgSaveMacros` handling, and broadcasting in `internal/server/server.go`.
3. **Macro Engine & UI (#237):**
   - Implemented `web/src/macros.ts` for sequential step execution without implicit Enter.
   - Built slide-up macro panel and configure/edit dialog with "Save to Server".
   - Added comprehensive Vitest and Playwright test coverage in `macros.test.ts` and `mobile.spec.js`.
4. **Verification:**
   - `make quick`: passed.
   - `make web-accept`: 23 browser tests passed.
   - `make check`: 100% green across all gate checks (fmt, lint, seam, test, web-test, web-accept, race, verify-exit, smoke, golden, attach-check).

