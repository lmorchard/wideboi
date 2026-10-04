# Spec: Detect Interrupted or Hung Working Agents via Inactivity Timeout (Issue #383)

**Goal:** Detect when a working pane has stopped producing PTY output for an extended period (default 30s) without completing, flag it as `StatusInterrupted` (`?`), and automatically resume active status as soon as new output arrives.

**Source:** https://github.com/lmorchard/wideboi/issues/383

---

## Current State

- When an authoritative status sequence (`OSC 133;C` or `OSC 9;4;3`) sets `StatusWorking`, `sawAuthoritativeStatus` is latched permanently.
- If the agent is interrupted via Ctrl-C or crashes without emitting an exit sequence, the pane remains in `▲ working` indefinitely.
- If a terminal title retains a Braille spinner (`isWorkingTitle`), it also stays `StatusWorking` indefinitely.

---

## Desired End State

1. **Working Inactivity Tracking:**
   - In `internal/server/term`, `vtGrid` monitors elapsed time since the last PTY write (`lastWriteTime`).
   - If a pane is in `StatusWorking` and no writes have occurred for longer than `workingInactivityTimeout` (default 30s):
     - `Status()` returns `protocol.StatusInterrupted`.
2. **Auto-Resumption:**
   - As soon as new bytes are written to the PTY via `Write()`:
     - `lastWriteTime` updates to `now`.
     - Status immediately clears `StatusInterrupted` and returns to `StatusWorking`.
3. **Visual Representation:**
   - Protocol status: `protocol.StatusInterrupted` (5)
   - Glyph: `?`
   - Formatted string: `"interrupted"`
   - Dashboard Wide/Compact: `? interrupted`
   - Dashboard Drawer: `?`
   - Status bar: `[? 2]`
   - Summary banner: `1 interrupted` (or `1?` in drawer mode)
4. **Attention Priority & Smart Jump:**
   - In dashboard sorting (`statusPriority`), `StatusInterrupted` ranks at 4 (alongside `StatusFailed`), floating to the top of the list.
   - In `smartJumpTargetLocked`, `StatusInterrupted` ranks above idle so `<prefix> a` can jump directly to stalled agents.
5. **Configurability:**
   - `workingInactivityTimeout` can be configured per grid (e.g. for testing with short 50–100ms timeouts).

---

## What We're NOT Doing

- We are not killing or terminating the child process on inactivity; we only update the presentation status.
- We are not applying inactivity timeouts to idle shells or panes waiting for input.

---

## Verification Plan

- Unit tests in `internal/server/term/heuristic_test.go`:
  - `TestWorkingInactivityTimeout`: Set 100ms inactivity timeout, assert pane becomes `StatusInterrupted` after 100ms of write silence.
  - `TestWorkingInactivityAutoResume`: New write after inactivity immediately resets status to `StatusWorking`.
  - `TestAuthoritativeWorkingInactivity`: Authoritative OSC 9;4;3 working pane also becomes `StatusInterrupted` on timeout.
- Unit tests in `internal/server/dashboard_test.go`:
  - Verify dashboard displays `? interrupted` and `1?` banner.
  - Verify priority sorting places `StatusInterrupted` near the top.
- Unit tests in `internal/client/client_test.go`:
  - Verify status bar badge formatting `[? 2]`.
  - Verify smart jump targets interrupted panes.
- Run `make quick` and ensure clean passes.
