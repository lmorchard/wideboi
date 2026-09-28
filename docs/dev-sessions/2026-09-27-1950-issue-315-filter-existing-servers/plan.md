# Filter pre-existing server processes in ptycheck.py Implementation Plan

**Goal:** Prevent false-positive stray process failures in `scripts/ptycheck.py` when running tests inside an active wideboi session.

**Approach:** Snapshot processes running `binary_path` prior to `spawn_in_pty` and pass them as `ignore_pids` to `find_stray_wideboi`, excluding pre-existing servers from stray detection. Update `docs/LESSONS.md`.

**Tech stack:** Python 3 (standard library only)

---

## Phase 1: Snapshot and filter pre-existing wideboi processes in ptycheck.py

Update `scripts/ptycheck.py` to capture pre-existing processes matching `binary_path` before spawning the child process in `run_check`, and pass them to `find_stray_wideboi` so they are ignored.

**Files:**
- Modify: `scripts/ptycheck.py`

**Key changes:**
- Add `pre_existing_wideboi(binary_path: str) -> set[int]` to scan `ps_rows()` for matching `argv0 == binary_path`.
- Update `find_stray_wideboi(binary_path: str, own_pid: int, ignore_pids: set[int] | None = None) -> list[str]` to skip any `pid in ignore`.
- Update `run_check` to capture `pre_existing = pre_existing_wideboi(argv[0])` before `spawn_in_pty` and pass `ignore_pids=pre_existing` to `find_stray_wideboi`.

```python
def pre_existing_wideboi(binary_path: str) -> set[int]:
    """Returns the set of PIDs matching binary_path that already exist before
    the harness spawns its child.
    """
    rows = ps_rows()
    existing = set()
    for pid, _, command in rows:
        argv0 = command.split(" ", 1)[0] if command else ""
        if argv0 == binary_path:
            existing.add(pid)
    return existing
```

**Verification — automated:**
- [x] Reproduce previous failure: run `python3 scripts/ptycheck.py --binary /Users/lmorchard/devel/mine/wideboi/bin/wideboi --size 1x1 --signal SIGTERM` before change and verify it failed on PID 18424 (`FAIL: 1 stray wideboi process(es) left behind: pid=18424 ppid=1 command=/Users/lmorchard/devel/mine/wideboi/bin/wideboi server -L wideboi`).
- [x] Run `python3 scripts/ptycheck.py --binary /Users/lmorchard/devel/mine/wideboi/bin/wideboi --size 1x1 --signal SIGTERM` after change and verify it succeeds with 0 strays reported — **verified passed**.
- [x] Run `make verify-exit` and verify all 7 matrix cases pass cleanly — **verified 7/7 passed**.
- [x] Run `make quick` and verify all fast checks pass — **verified passed**.

---

## Phase 2: Update documentation

Update `docs/LESSONS.md` to describe how `ptycheck.py` filters pre-existing server processes to prevent false positives when running inside active wideboi sessions.

**Files:**
- Modify: `docs/LESSONS.md`

**Verification — automated:**
- [x] `git diff docs/LESSONS.md` accurately reflects the behavior — **verified**.

---

## Phase 3: Full suite verification

Run the complete test suite to ensure no regressions across any checks.

**Verification — automated:**
- [x] `make check` passes cleanly (or individual suites `test`, `web-test`, `verify-exit`, `smoke`, `attach-check`) — **all 10 check targets passed: fmt-check, lint, seam-check, test, web-test (155 passed), web-accept (50 passed), race, verify-exit (7/7 passed), smoke (41/41 passed), attach-check (29/29 passed)**.
