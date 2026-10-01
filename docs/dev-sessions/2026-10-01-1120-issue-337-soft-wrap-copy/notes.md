# Notes: Issue 337 Soft-Wrap Selection & Copying

- Worktree: `.worktrees/issue-337-soft-wrap-copy`
- Branch: `issue-337-soft-wrap-copy`
- Status: Completed implementation and full verification.

## Decisions & Architecture
- Protocol (v20): Added `bool wrapped = 2` to `LineData` and `bool wrapped = 3` to `PaneRow`, and `WrappedLines []bool` to `MsgPaneUpdate`.
- Server Extraction: Computed `wrappedLines` in `UpdateMessageForOffset` directly from the rendered `uv.ScreenBuffer`: a row `y` soft-wraps into `y+1` when cell `cols-1` has non-blank content and row `y+1` has content. Avoided all per-character callbacks and locks on `term.Grid`.
- Patching: `BuildPanePatch` and `ApplyPanePatch` propagate `wrapped` status and include `WrappedLines` in shift candidate comparisons.
- TUI Client: `internal/client/mouse.go` checks `pu.WrappedLines[localY]` and omits the newline when row `localY` is soft-wrapped.
- Web Client: `web/src/pane-state.ts` respects `line.wrapped` in `selectionText`, concatenating soft-wrapped rows without `\n`.
- Testing: Verified unit tests in Go (`mouse_test.go`), Vitest (`pane-rendering.test.ts`), Playwright browser tests (`links-and-clipboard.spec.ts`), pty suites (`smoke.py`, `attachcheck.py`), and `seam-check`.
