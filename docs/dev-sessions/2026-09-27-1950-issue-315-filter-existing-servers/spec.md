# Filter pre-existing server processes in ptycheck.py Spec

**Goal:** Eliminate false-positive stray process failures in `scripts/ptycheck.py` when running tests inside an active wideboi session.

**Source:** GitHub Issue #315

## Current state
- In `scripts/ptycheck.py:72-121`, `find_stray_wideboi(binary_path, own_pid)` scans `ps_rows()` for processes matching `binary_path` where `ppid == own_pid or ppid == 1 or ppid not in live`.
- When running `verify-exit` in an environment where a wideboi server is already running (e.g. within an active wideboi session, or with a detached server from the same checkout), the pre-existing server process has `ppid == 1` and matches `binary_path`.
- `find_stray_wideboi` reports this pre-existing server as a leaked stray process, failing `verify-exit` even though `ptycheck.py` properly cleaned up its own child processes.
- Documented in `docs/LESSONS.md:212-217` as a known false positive to manually work around.

## Desired end state
- `scripts/ptycheck.py` records any existing processes matching `binary_path` before spawning the child process under test.
- `find_stray_wideboi` ignores those pre-existing PIDs when checking for strays.
- Pre-existing wideboi servers (such as the host session or detached servers) do not trigger false positive failures in `verify-exit`.
- Any actual stray process leaked during the test run is still detected and reported.
- `docs/LESSONS.md` is updated to reflect that `ptycheck.py` now filters pre-existing server processes.

## Design decisions
- **Decision:** Snapshot PIDs matching `binary_path` before `spawn_in_pty` and pass them as `ignore_pids` to `find_stray_wideboi`.
  - **Why:** Simple, robust, and portable across macOS and Linux. Processes existing before `spawn_in_pty` cannot have been leaked by the test run.
  - **Rejected:** Parsing `ps` start time / `lstart` / `etimes`. macOS `ps` lacks `etimes`, and parsing locale-dependent date/time strings from `lstart` is fragile across environments.
- **Decision:** Collect all processes matching `binary_path` at startup rather than only `ppid == 1`.
  - **Why:** Any process running `binary_path` that existed before `spawn_in_pty` was not created by this run. If a pre-existing client exits during the test and its server reparents to PID 1, that server was also pre-existing and should not be flagged.
  - **Rejected:** Only snapshotting `ppid == 1` processes at startup.

## Patterns to follow
- `scripts/ptycheck.py:72-121`: `find_stray_wideboi` logic and `still_alive` check.
- `scripts/ptylib.py:238-260`: `ps_rows()` helper.

## What we're NOT doing
- We are NOT modifying `smoke.py` (which already tracks its own `SPAWNED` list).
- We are NOT removing the stray check or `still_alive` settle wait for newly orphaned sibling processes under concurrent execution (#102).
- We are NOT killing or terminating pre-existing processes; they are merely ignored when evaluating whether *this* run leaked any processes.

## Open questions
None.
