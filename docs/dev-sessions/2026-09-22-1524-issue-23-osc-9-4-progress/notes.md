# Notes: Issue #23 — Support OSC 9;4 Progress

- Session started: 2026-09-22 15:24
- Worktree: `.worktrees/issue-23-osc-9-4-progress`
- Branch: `issue-23-osc-9-4-progress`

## Summary of Work
1. Created `docs/TERMINAL-METADATA.md` explaining CSI vs. OSC channels, supported protocols (titles OSC 0/1/2, shell integration OSC 133, progress OSC 9;4), latching architecture, and agent terminal capability detection.
2. Refactored `sawOSC133` to `sawAuthoritativeStatus` in `internal/server/term/grid.go`, unifying the latch between OSC 133 and OSC 9;4.
3. Implemented OSC 9;4 progress handler registered on `x/vt.SafeEmulator`:
   - State `0`: `StatusDone`
   - State `1` & `3`: `StatusWorking`
   - State `2`: `StatusFailed`
   - State `4`: `StatusNeedsInput`
   - Unrecognized or non-progress OSC 9 sequences leave `sawAuthoritativeStatus` unlatched and return `false`.
4. Added full unit test suite in `internal/server/term/osc_test.go`:
   - `TestOSC9ProgressDrivesPaneStatus` (table-driven for all states)
   - `TestMalformedOSC9LeavesTheIdleFallbackArmed` (verifying latch behavior under malformed / non-progress OSC 9)
   - `TestOSC9And133Interleaving` (verifying last-writer-wins precedence between 133 and 9;4)
5. Verified via `make quick` and 4 consecutive runs of `make check` (all green).
