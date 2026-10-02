# Notes: Issue 331 (mobile web virtual keyboard input)

- **Worktree:** `.worktrees/issue-331-mobile-keyboard`
- **Branch:** `issue-331-mobile-keyboard`
- **Baseline:** `make quick` passed cleanly on origin/main (`c206ad6`).
- **Research:** Investigated `wideboi-app.ts` input capture, `fromFormControl`, `handleMobileDirectKey`, `handleMobileDirectInput`, and `input.ts`.
- **Brainstorm:** Decided on a padded baseline buffer (`\u00a0` x 8 with centered caret) + `beforeinput` and `input` events, encapsulated in `web/src/mobile-direct-input.ts`.
- **Execution:**
  - Phase 1: Implemented `MobileDirectInputController` with baseline setup, caret positioning, `beforeinput`, `keydown`, `input`, `composition*`, and `blur`/`focus` handling. Added Vitest unit test suite covering all mobile browser event sequences.
  - Phase 2: Integrated `MobileDirectInputController` into `wideboi-app.ts` on `.mobile-direct-input`.
- **Verification:** Unit tests, Playwright mobile tests, and full test suite (`make quick`, `make check`) passed cleanly.
