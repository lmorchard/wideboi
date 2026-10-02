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

## Retrospective

### Recap
Implemented `MobileDirectInputController` in `web/src/mobile-direct-input.ts` to harden mobile virtual keyboard handling in direct mode. Eliminated the GBoard/Android backspace bug via an 8-character non-breaking space baseline (`\u00a0`) with centered caret, and handled `Process` keys and IME composition without dropped or duplicated keystrokes. Integrated into `.mobile-direct-input` in `wideboi-app.ts`.

### Scope Drift & Hardening
- **IME duplicate suppression:** Tracked `justComposed` to suppress follow-up `insertFromComposition` and `input` events after `compositionend`, preventing duplicate committed characters.
- **Modifier preservation:** Preserved Shift, Alt, and repeat flags on hardware Ctrl chords, and allowed printable Alt chords (e.g. `Alt+b`) to reach terminal key routing.
- **On-screen Ctrl with soft keyboard:** Applied the on-screen mobile Ctrl modifier consistently to text arriving through Android `Process` key inputs.
- **Placeholder restoration:** Emptied input on blur so the placeholder ("Direct terminal keys…") remains visible when idle, initializing the padded baseline on focus.

### Surprises
- In Vitest without a DOM environment (Node runner), `document` is not defined globally. Abstracting the controller to accept a lightweight `DirectInputElement` interface allowed 13 fast, deterministic unit tests for complex mobile browser event matrices without needing browser launches.

### Memory Candidates
- On mobile virtual keyboards (notably GBoard on Android), Backspace produces no DOM events when an input field is empty (`value === ""`). Maintaining a non-breaking space baseline buffer (`\u00a0` x 8) with centered caret ensures Backspace always has characters to delete and can be intercepted via `beforeinput` (`deleteContentBackward`) or length reduction.

