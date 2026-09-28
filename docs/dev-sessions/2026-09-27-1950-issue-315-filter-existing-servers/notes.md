# Notes: Issue 315

- Worktree: `.worktrees/issue-315-ptycheck-filter-strays`
- Branch: `issue-315-ptycheck-filter-strays`
- Tracking: `origin/main`

## Summary
- Reproduced issue #315: when testing a binary path matching an active wideboi host session (or detached session), `find_stray_wideboi` in `scripts/ptycheck.py` detected the host server process (e.g. PID 18424 with `ppid == 1`) and falsely failed with `FAIL: 1 stray wideboi process(es) left behind`.
- Added `pre_existing_wideboi(binary_path)` in `scripts/ptycheck.py` to capture matching processes alive before `spawn_in_pty`.
- Extended `find_stray_wideboi(binary_path, own_pid, ignore_pids)` to skip any processes in `ignore_pids`.
- Updated `docs/LESSONS.md` to note that `ptycheck.py` now snapshots pre-existing wideboi processes at startup and ignores them.
- Verified fix directly against the reproduction command (`python3 scripts/ptycheck.py --binary .../bin/wideboi --size 1x1 --signal SIGTERM`), which now passes with 0 strays.
- Verified with full `make check` (all 10 check targets passed including parallel `verify-exit`).
