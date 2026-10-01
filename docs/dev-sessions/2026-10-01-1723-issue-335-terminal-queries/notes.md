# Notes: Issue 335 Terminal Environment Queries

- Worktree: `.worktrees/issue-335-terminal-queries`
- Branch: `issue-335-terminal-queries`
- Status: Completed implementation and full verification.

## Decisions & Architecture
- Query Interception: `queryScanner` in `internal/server/queries.go` intercepts PTY output before the emulator, matches queries (`OSC 10;?`, `OSC 11;?`, `OSC 4;N;?`, `CSI 14t`, `CSI 16t`, `CSI 18t`, and `CSI ? 996 n`), and synthesizes immediate responses directly to the child PTY (`p.pty.WriteBounded`).
- Fast Path: When `qs.buf` is empty and no `ESC` (`0x1b`) byte is present, `process` returns the original chunk slice immediately with zero allocations.
- Query String Terminators: The exact terminating sequence used by the child application (`\x1b\` ST vs `\x07` BEL) is preserved in synthesized responses.
- Theme Configuration: `QueryTheme` is propagated from `Server` to all panes (spawned and adopted) and configured via `atomic.Pointer[QueryTheme]` on `Pane`.
- Testing: Comprehensive unit test suite in `queries_test.go` verifies all query forms, both terminators, dark/light themes, packet splitting, non-query passthrough, and an end-to-end real PTY test (`TestPaneRealPTYQueryInterception`) confirming child roundtrip in 0.02s without emulator leakage. All existing test suites pass.
