# Spec: Detect Agent Permission Prompts and Blockers (Issue #376)

**Goal:** Detect when an interactive AI coding agent (Claude Code, OpenCode, Codex, etc.) or CLI prompt is blocked waiting for human input/permission and promote the pane's status to `StatusNeedsInput` (`!`), while also keeping status in `StatusWorking` when title spinners or progress indicators are active.

**Source:** https://github.com/lmorchard/wideboi/issues/376

---

## Current State

- In `internal/server/term/grid.go`, `vtGrid.Status()` reports `protocol.PaneStatus`.
- If an authoritative OSC sequence (OSC 133 or OSC 9;4) has not been received, `Status()` relies strictly on `lastWriteTime`:
  - Output written within `idleTimeout` (default 3s) &rarr; `StatusWorking`.
  - No writes for > `idleTimeout` &rarr; `StatusIdle`.
- Coding agents rarely emit OSC 133 or OSC 9;4. When they display permission or confirmation prompts, they produce silence on stdout. Wideboi marks them as `StatusIdle` after 3 seconds.
- When an agent is running a long turn or background task with silent stdout but active title spinners (e.g. `⠋ Thinking...`), Wideboi also falls back to `StatusIdle` after 3s.

---

## Desired End State

1. **Title Spinner / Working Detection:**
   - If the terminal title contains active spinner runes (Braille `\u2800-\u28FF` or arc quadrant spinners `\u25D0-\u25D3`), `Status()` evaluates to `StatusWorking` even if stdout has been silent for > 3s.
   - If the terminal title contains `"Action Required"`, `Status()` evaluates to `StatusNeedsInput`.

2. **Screen-Tail Blocker Detection (`StatusNeedsInput`):**
   - When stdout writes pause and no authoritative status is active, the bottom $N$ (e.g. 12) non-empty lines of the visible screen are inspected for known blocker and interactive prompt patterns:
     - OpenCode: `"△ Permission required"`, `"esc dismiss"` with `"enter confirm"` or `"enter submit"`.
     - Claude Code: `"Do you want to proceed?"`, `"waiting for permission"`, `"do you want to run"`, `"do you want to allow"`, `"esc to cancel"` with `"enter to confirm"`.
     - Codex: `"Do you trust the contents of this directory?"`, `"Trust and continue"`.
     - Generic CLI confirmation: `"[y/N]"`, `"[Y/n]"`, `"(y/n)"`, `"[y/n]"` at or near the prompt line.
   - If a blocker pattern matches, `Status()` returns `protocol.StatusNeedsInput`.

3. **Screen-Tail Working Detection:**
   - If the screen tail displays active working indicators (e.g. `"esc to interrupt"`, `"ctrl+c to interrupt"`, progress bars `(■|⬝){4,}`), `Status()` returns `protocol.StatusWorking`.

4. **Zero Performance Overhead on 33ms Ticker:**
   - Evaluated heuristically only when `sawAuthoritativeStatus` is false and writes have paused.
   - Results are cached by `outputGen` (and title generation). If no writes have occurred since the last scan, `Status()` returns the cached status immediately in $O(1)$ without re-scanning or running regexes.

5. **Fallbacks:**
   - If no blocker or working pattern matches after write silence exceeds `idleTimeout`, status falls back to `StatusIdle`.
   - If authoritative status (OSC 133 or OSC 9;4) is received, it takes precedence as before.

---

## Design Decisions

- **Decision:** Implement scanner in `internal/server/term/heuristic.go` called from `vtGrid.Status()`.
  - **Why:** Keeps terminal parsing and state calculation encapsulated inside the `term` package where `vtGrid`, `DumpText`, and title storage reside.
  - **Alternative rejected:** Putting the scanner in `internal/server/pane.go`. That would duplicate screen locking and require exporting internal grid fields.
- **Decision:** Cache heuristic status by `outputGen` + `title`.
  - **Why:** Protects CPU cycles on the 33ms server broadcast tick. Scanning occurs at most once per write burst or title update.
- **Decision:** Scan bottom 12 visible screen rows without scrollback.
  - **Why:** Prompts and permission dialogs always appear at the bottom of the active viewport. Reading only the tail lines avoids scanning old history.

---

## What We're NOT Doing

- We are not downloading remote TOML manifests at runtime; patterns are compiled Go regexes/substring checks in Wideboi's binary.
- We are not wrapping or monkey-patching agent CLIs; detection is entirely passive via terminal state inspection.
- We are not altering the wire protocol; `protocol.StatusNeedsInput` and `protocol.StatusWorking` already exist in the protocol.

---

## Verification Plan

- Unit tests in `internal/server/term/heuristic_test.go`:
  - Test Claude Code permission prompt &rarr; `StatusNeedsInput`.
  - Test OpenCode permission required &rarr; `StatusNeedsInput`.
  - Test generic `[y/N]` prompt &rarr; `StatusNeedsInput`.
  - Test Braille title spinner `⠋ Thinking...` &rarr; `StatusWorking` (even after idle timeout).
  - Test `"esc to interrupt"` in screen tail &rarr; `StatusWorking`.
  - Test that output resume immediately resets to `StatusWorking` and then `StatusIdle` on clean exit.
  - Test caching: verify screen is not re-parsed when `outputGen` is unchanged.
- Integration tests in `internal/server/status_test.go` and `cmd/wideboi/status_test.go`.
