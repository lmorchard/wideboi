# Notes: Distinguish Unseen Completions from Idle Panes (Issue #375)

- 2026-10-03: Session started in worktree `/home/lmorchard/devel/wideboi-issue-375` on branch `feat/issue-375-unseen-completions`.
- Implemented client-local unseen completion tracking in `internal/client/`:
  - `unseenDone map[int]bool` records unfocused panes transitioning `Working -> Idle`.
  - `displayStatusLocked(id)` returns `StatusDone` (`✓`) while unseen.
  - Cleared automatically when the client focuses the pane (keyboard, mouse, smart jump, digit keys).
  - Integrated into `smartJumpTargetLocked` (`<prefix> a`) to iteratively cycle through completed and blocked tasks.
- Implemented server dashboard unseen completion tracking in `internal/server/`:
  - `s.unseenDone map[int]bool` records unfocused completions and surfaces them as `StatusDone` (`✔ done`) in the status dashboard.
- Implemented web client unseen completion tracking in `web/src/wideboi-app.ts`:
  - Surfaces `StatusDone` in card headers, mobile bar, and smart jump until focused.
- Added test coverage in `internal/client/client_test.go` and `internal/server/server_test.go`.
- All tests and `make quick` passed cleanly.
