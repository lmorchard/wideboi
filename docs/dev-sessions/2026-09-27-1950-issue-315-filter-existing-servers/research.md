# Research: Issue 315

## Stray detection in ptycheck.py
- `scripts/ptycheck.py:72-121`: `find_stray_wideboi(binary_path, own_pid)` parses `ps_rows()` and looks for any process with `argv0 == binary_path` where `ppid == own_pid or ppid == 1 or ppid not in live`.
- Lines 113-120 wait 2.0s via `still_alive` for reparented sibling processes under concurrent `make verify-exit` runs to exit (Issue #102, commit `8efa548`).
- Lines 306-312 invoke `strays = find_stray_wideboi(argv[0], own_pid=pid)`. If non-empty, `run_check` logs `FAIL: ... stray wideboi process(es) left behind:` and fails the test.

## False positive with host wideboi sessions
- When running `make verify-exit` from inside wideboi (e.g., in a terminal pane or via an agent), the host session's background server process (`.../bin/wideboi server -L <session>`) is reparented to PID 1 because it daemonizes (`Setsid: true` in `cmd/wideboi/spawn.go:64`).
- If `verify-exit` tests the same binary path as the host session (e.g. `bin/wideboi`), `argv0 == binary_path` matches the host server process (PID 18424 in reproduction).
- Because `ppid == 1` and the host session stays running across the 2.0s settle window, `find_stray_wideboi` flags it as a leaked stray.

## Comparison with smoke.py
- `scripts/smoke.py:1243-1256`: maintains `SPAWNED: list[int]`, tracking every pid spawned by that test run, and `strays()` only checks if any pid in `SPAWNED` remains in `live`.
- In `ptycheck.py`, `tracked = descendants(pid)` already tracks all descendants spawned by `wideboi` before the signal is sent (lines 180, 279-292). `find_stray_wideboi` was added as assertion 4 to catch rogue double-forked copies of `binary_path` that reparented before `descendants(pid)` could walk them.

## Solutions evaluated
1. **Process start times (`lstart` / `etime` / `etimes`):**
   - macOS `ps` lacks `etimes`. `lstart` outputs multi-word date/time strings (`Sun Sep 27 19:55:41 2026`) that vary across platforms and locales. Fragile and complex.
2. **Snapshot pre-existing wideboi processes at harness startup:**
   - Query `ps_rows()` before `spawn_in_pty` in `run_check`.
   - Any PID running `binary_path` prior to `spawn_in_pty` was started before this check and cannot have been spawned or leaked by this run.
   - Pass `ignore_pids: set[int]` to `find_stray_wideboi`.
   - Fast (0.04s `ps` query), 100% portable across macOS and Linux, avoids locale issues, and directly solves the false positive.
