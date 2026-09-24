# Notes: Keep the sent frame as the patch baseline when a pane changes mid-send

## Session Context

- Issue: #206
- Worktree: `.worktrees/issue-206-mid-send-patch-baseline`
- Branch: `issue-206-mid-send-patch-baseline`

## Changes Summary

1. `internal/server/server.go`:
   - Removed lines 1341-1349 where `r.pane.Generation() != r.underlyingGen` caused `s.paneGens`, `s.paneFrames`, `s.paneUnderlyingGens`, `s.paneOutputGens`, and `s.paneSbLens` to be deleted.
   - Retained `r.frame` and `r.wireGen` as the client's baseline for all successful sends (`r.accepted == true`).
   - When new child output moves the generation during render or transmission, the next broadcast tick compares the newer generation against the recorded baseline and constructs a `MsgPanePatch` rather than falling back to a full `MsgPaneUpdate` snapshot.

2. `internal/server/pane_traffic_test.go`:
   - Added `TestPaneUpdatePatchBaselineRetainedOnMidSendWrite`:
     - Sets up a VT emulator with a hook that executes a `Write` during `DrawAt`.
     - Initial broadcast delivers a snapshot.
     - Second broadcast triggers a write mid-draw, advancing the emulator generation.
     - Third broadcast verifies that `MsgPanePatch` is sent instead of `MsgPaneUpdate`.
     - Applies the patch using `protocol.ApplyPanePatch` and asserts that the reconstructed client mirror exactly matches `pane.UpdateMessage()` full render at the new generation.

3. `scripts/traffic.py`:
   - Updated comment in `scroll_paced` scenario to explain that prior to #206 mid-send output dropped the baseline and forced fulls, whereas now shift patches are maintained.

## Verification Findings

- `TestPaneUpdatePatchBaselineRetainedOnMidSendWrite` initially failed under the old code with:
  `expected MsgPanePatch after mid-send write, got protocol.MsgPaneUpdate`
  After the fix in `server.go`, it passed cleanly.
- `TestGenerationChangingDuringRenderIsRetried` passed cleanly (0.00s).
- Full `make check` passed with 0 failures:
  - `fmt-check`, `lint`, `seam-check`, `test`, `web-test`, `web-accept` (Playwright) passed.
  - `race` detector passed on all packages.
  - `verify-exit` (6 pty exit cases) passed.
  - `smoke` (37 tests) passed.
  - `attachcheck` (25 tests) passed.
- Traffic suite (`python3 scripts/traffic.py --seconds 2`) ran all 5 scenarios (`typing`, `scroll`, `scroll-paced`, `tui`, `large`) successfully.
