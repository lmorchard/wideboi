# Notes: Detect Agent Permission Prompts and Blockers (Issue #376)

- 2026-10-03: Session started in worktree `/home/lmorchard/devel/wideboi-issue-376` on branch `feat/issue-376-detect-agent-blockers`.
- Researched problem: Claude Code, OpenCode, and Codex do not emit OSC 133 or OSC 9;4, causing blocked/waiting states to drop to `StatusIdle` after 3s.
- Implemented `heuristic.go` with pattern matching for:
  - Title Braille/arc spinners (`isWorkingTitle`) & "Action Required" (`isBlockerTitle`).
  - Screen tail scanner for Claude Code, OpenCode, Codex, and generic `[y/N]` prompts.
- Integrated into `vtGrid.Status()` with `outputGen` + title caching to eliminate CPU overhead on the 33ms server broadcast tick.
- Debounce window dynamically caps at `idle/2` so short test idle timeouts (e.g. 50ms) decay predictably without sleeping through fixed 500ms windows.
- All unit tests and `make quick` passed cleanly.
