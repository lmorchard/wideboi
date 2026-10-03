# Notes: Detect Interrupted or Hung Working Agents via Inactivity Timeout (Issue #383)

- 2026-10-03: Session started in worktree `/home/lmorchard/devel/wideboi-issue-383` on branch `feat/issue-383-working-inactivity-timeout`.
- Added `StatusInterrupted` (5) to `protocol.PaneStatus` and `PANE_STATUS_INTERRUPTED` (5) to wire protobuf schema.
- Added `DefaultWorkingInactivityTimeout` (30s) and `workingInactivityTimeout` on `vtGrid`.
- In `vtGrid.Status()`: if status evaluates to `StatusWorking` and no writes have arrived for > `workingInactivityTimeout`, status is demoted to `StatusInterrupted` (`?`).
- Self-healing: as soon as new PTY writes arrive via `Write()`, status immediately resets to `StatusWorking`.
- Updated dashboard presentation with `? interrupted` and `1?` summary badge, priority sorted at 4 (alongside failed).
- Updated client status bar badge `[? 2]` and smart jump ranking.
- Updated web client tabs, mobile bar, and smart jump.
- All unit tests and `make quick` passed cleanly.
