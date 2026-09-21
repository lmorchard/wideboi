# Plan 21 — Parallel checks Implementation Plan

**Goal:** `make check` from 83s to ~15s, by fixing two real wideboi/harness
bugs that block concurrency and then running the suites in parallel.

**Approach:** Correctness first — an unkillable-process bug in `Guard.Arm` and
an inherited-signal-disposition bug in `spawn_in_pty`, both of which only
surface when something is backgrounded. Then socket isolation, then the
parallelism itself.

**Tech stack:** Go, Python (stdlib only), `make`.

**Commit per phase:** `Phase N: <name>`.

**Baselines, measured:** `make check` 83s. Per target: smoke 48.05s,
verify-exit 17.09s, attach-check 7.88s, race 5.40s, test 4.14s, static <0.3s
each. Parallel smoke spike: 42.9s → 13.2s at width 8, **25/25 across eight
runs**.

---

## Phase 1: Guard must never leave an unkillable process

**The bug, diagnosed by instrumenting the guard.** With `SIGINT` inherited as
`SIG_IGN` — which POSIX *requires* a shell to do for a background job — wideboi
catches the signal, tears down correctly, and then **never exits**:

```
guard: signal received interrupt
guard: Stop() returned            (2.0s later -- CloseGrace)
guard: reset done, re-raising
guard: re-raise returned (STILL ALIVE)
```

`signal.Notify` installs a Go handler even over `SIG_IGN`, so the catch works.
`signal.Stop(ch)` + `signal.Reset(s)` then faithfully restore the *original*
disposition — `SIG_IGN` — so `syscall.Kill(os.Getpid(), sig)` is discarded and
returns. All four armed signals revert together, so the process is thereafter
immune to INT/TERM/HUP; only SIGKILL/SIGABRT touch it. Confirmed by sending
each externally: all three left it alive.

**This is user-facing: `wideboi &` hangs forever on those signals today.**

**Files:**
- Modify: `internal/hostterm/guard.go` — exit explicitly if the re-raise is
  discarded.
- Modify: `internal/hostterm/signal_test.go` — new test.

**Test first.** Model on `TestGuardArmRestoresAndReRaises`
(`signal_test.go:17`), which already re-execs the test binary as a child. The
new test differs in one way: the child is launched through `sh -c 'trap ""
TERM; exec ...'` so the ignored disposition is genuinely **inherited**, as a
shell's background job would deliver it.

```go
// POSIX requires a shell to set SIGINT/SIGQUIT to SIG_IGN for an
// asynchronous list, so anything started as `cmd &` inherits them
// ignored. signal.Notify installs a handler over that, so the guard
// still catches and tears down -- but signal.Reset restores SIG_IGN,
// the re-raise is discarded, and the process used to hang forever.
func TestGuardExitsWhenTheReRaiseIsIgnored(t *testing.T) {
	// child branch: identical to the existing test's, arming SIGTERM.
	//
	// parent: launch via sh so the disposition is inherited, not set
	// in-process -- signal.Ignore would exercise Go's own bookkeeping
	// rather than the real case.
	cmd := exec.Command("/bin/sh", "-c",
		`trap "" TERM; exec "$0" -test.run=TestGuardExitsWhenTheReRaiseIsIgnored`,
		os.Args[0])
	// ... env as in the existing test ...
	// Wait with a deadline: before the fix the child hangs, and a bare
	// cmd.Wait() would hang the whole package rather than fail.
	// Assert: shutdown ran (marker present), child exited (not signalled)
	// with code 128+SIGTERM = 143.
}
```

**Key change** in `Guard.Arm`'s goroutine:

```go
		signal.Stop(ch)
		signal.Reset(s)
		if sig, ok := s.(syscall.Signal); ok {
			_ = syscall.Kill(os.Getpid(), sig)
			// A delivered, default-disposition signal does not return
			// here. Reaching this line means the signal's original
			// disposition was SIG_IGN -- inherited, and faithfully
			// restored by Reset -- so the re-raise was discarded.
			// Exit with the conventional status rather than hanging
			// forever with the terminal already restored.
			os.Exit(128 + int(sig))
		}
```

**Verification — automated:**
- [x] The new test fails before the fix, and fails by *timeout-and-report*, not by hanging the package — **FAIL at 10.01s: `child never exited after SIGINT`.** First attempt used SIGTERM and failed for the wrong reason (`child died by signal terminated`): Go's runtime respects an inherited SIG_IGN only for SIGHUP and SIGINT (`sigInstallGoHandler`) and installs its own handler over an ignored SIGTERM, so SIGTERM cannot reproduce this at all.
- [x] `go test -count=1 ./internal/hostterm/` passes after — **all 5 tests pass, new one in 0.04s**
- [x] `TestGuardArmRestoresAndReRaises` still passes unchanged — the foreground path must still die *by* the signal, not exit 143 — **it caught a real race in my first fix.** Exiting *after* a failed re-raise is racy: `Kill` can return before the signal lands, so `os.Exit` sometimes won and the process exited 143 instead of dying by SIGTERM. Re-done by sampling `signal.Ignored` at Arm time, before `Notify`; 3 consecutive clean runs.
- [x] `make test` passes — **all packages ok; `make lint` and `make fmt-check` clean**

**Verification — manual:**
- [x] Reproduce by hand: `wideboi &` then `kill -INT %1` exits instead of hanging. — **backgrounded: `exited with status 130` after 2.1s (was: alive forever). Foreground: `died by signal 2`, contract preserved.** My first probe reported a false 'STILL ALIVE': it used `kill(pid,0)`, which succeeds on a zombie, and Python never reaped the child. Re-checked with `waitpid`.

---

## Phase 2: Pin the child's signal dispositions in spawn_in_pty

Phase 1 stops the hang, but converts it into a *different* failure for
`ptycheck`, which asserts the process **died by** the signal (`WIFSIGNALED`) —
an `os.Exit(143)` is a normal exit. A test that asserts "dies by SIGINT" must
not run under a disposition where SIGINT does nothing.

**Files:**
- Modify: `scripts/ptylib.py` — reset dispositions in the forked child.

**Key change**, in `spawn_in_pty`'s child branch beside the existing
`SHELL`/`TERM`/`PS1` pins (`ptylib.py:170-195`), which exist for exactly this
reason ("the assertions depend on it"):

```python
            # Pinned for the same reason SHELL/TERM/PS1 are: the
            # assertions depend on it. POSIX requires a shell to set
            # SIGINT and SIGQUIT to SIG_IGN for an asynchronous list, so
            # anything spawned from a `cmd &` -- including every target
            # under `make -j` -- inherits them ignored, and a test that
            # asserts "died by SIGINT" cannot run under a disposition
            # where SIGINT does nothing.
            for _sig in (signal.SIGINT, signal.SIGQUIT,
                         signal.SIGTERM, signal.SIGHUP):
                signal.signal(_sig, signal.SIG_DFL)
```

**Verification — automated:**
- [x] Backgrounded `python3 scripts/ptycheck.py --signal SIGINT` passes and reports `died by signal 2` — **passes twice.** Proved needed *after* Phase 1: with the guard fixed but the disposition unpinned it failed differently — `exited normally with status 130, expected death by signal 2` — which is exactly why both phases exist.
- [x] `make verify-exit` still passes serially (all six invocations) — **PASS, 18 OK lines (3 assertions x 6 runs)**
- [x] `make smoke` and `make attach-check` still pass — the pin applies to every spawned child, not just ptycheck's — **smoke 25/25 + golden, attach-check 7/7**

**Verification — manual:**
- [x] Confirm the reset happens *after* the fork and *before* `execvpe`, so it cannot affect the parent harness process. — **confirmed; it sits in the `if pid == 0:` branch beside the SHELL/TERM/PS1 pins, and the comment says why**

---

## Phase 3: WIDEBOI_SOCK, so suites stop joining each other's sessions

`cmd/wideboi/main.go:255-258` auto-attaches to
`$TMPDIR/wideboi-$UID/default.sock`; attachcheck binds the identical path. With
a server up, a plain wideboi has **zero pane children** — measured. **With a
`wideboi server` running in another terminal, serial `make smoke` fails 9
cases today.**

**Files:**
- Modify: `cmd/wideboi/main.go` — env override in `defaultSocketPath`.
- Modify: `scripts/smoke.py`, `scripts/golden.py` — a path that never exists.
- Modify: `scripts/attachcheck.py` — a per-run unique path.

**Key changes:**

```go
// defaultSocketPath is the session socket. WIDEBOI_SOCK overrides it
// outright: the path is machine-global per-uid, so two sessions cannot
// coexist and anything that dials it joins whoever got there first.
// A single escape hatch, not a design -- see #27 for real session
// naming and #58 for configuration generally.
func defaultSocketPath() string {
	if p := os.Getenv("WIDEBOI_SOCK"); p != "" {
		_ = os.MkdirAll(filepath.Dir(p), 0700)
		return p
	}
	// ... existing body ...
}
```

smoke and golden pass `WIDEBOI_SOCK` pointing inside a fresh `mkdtemp()` at a
name never created — they only ever dial, so this makes them immune to *any*
server, including the developer's. attachcheck computes one unique path per
run and uses it for both its server and its `socket_path()`.

**Verification — automated:**
- [x] With a `wideboi server` running, `make smoke` passes — **it fails 9 cases today; this is the discriminating check** — **25/25 + golden, with a default-path server up**
- [x] `make attach-check` passes, including `case_second_server_refuses_to_steal_the_socket` and `case_attach_without_a_server_says_so`, which assert exclusive ownership of the path — **7/7, and 7/7 again with an unrelated default-path server running**
- [x] `make check` passes serially — **81.0s, green**
- [x] `go build ./...`, `make lint`, `make fmt-check` pass — **all clean**

**Verification — manual:**
- [x] `WIDEBOI_SOCK=/tmp/x.sock wideboi server` + `WIDEBOI_SOCK=/tmp/x.sock wideboi attach` works by hand, and a plain `wideboi` in a third terminal does *not* join it. — **attach on the custom socket: 0 descendants (attached). A wideboi pointed elsewhere: 2 descendants (own session).**

---

## Phase 4: Scope ptycheck's stray scan to this run

`find_stray_wideboi` (`ptycheck.py:98-128`) walks the whole process table for
any process whose argv[0] equals the absolute binary path
(`argv = [os.path.abspath(binary)]`, `:136`) and excludes only its own pid. Six
concurrent runs all use the same absolute path.

**Proved deterministically:** with one other absolute-path wideboi alive,
ptycheck reports `FAIL: 1 stray wideboi process(es) left behind: pid=2152`
— a process it never spawned.

This is the same shape as the guard fixed in `smoke.py` during Plan 19, where
the global version false-positived and the fix was to track the pids *this run*
spawned.

**Files:**
- Modify: `scripts/ptycheck.py` — scope the scan.

**Key change:** `find_stray_wideboi` takes the set of pids this run spawned
(the wideboi pid plus its `tracked` descendants) and reports only those still
alive, dropping the `ps`-wide argv0 match. The failure message keeps naming the
command so it stays diagnosable.

**Verification — automated:**
- [x] The overlap case now passes: another absolute-path wideboi alive while ptycheck runs — **currently fails deterministically** — **now PASS**
- [x] The guard still *fires* when it should: leave a real stray from this run and confirm it is reported (prove the check can fail, per Plan 19) — **`FAIL: 1 stray wideboi process(es) left behind: pid=8879 ppid=1`.** Took two attempts to stage: the first orphan died instantly because its parent held the pty master, so the master has to be held by a separate process while the spawning parent exits.
- [x] `make verify-exit` passes serially — **PASS, 18 OK lines**

**Verification — manual:**
- [!] Re-read: the scan no longer consults `ps` for anything except rendering the command of a pid it already knows about. — **not how it ended up, and the plan's phrasing was wrong.** Assertion 4 is deliberately *global* (docstring: "a wideboi outliving the one this script reaped would mean a double-fork, a hung child of a panic"), so it has no list of known pids to work from — scoping it to pids we already know would delete the assertion rather than fix it. It still walks the whole table; the scoping is by **parentage**: `ppid == own_pid`, `ppid == 1`, or a dead parent. A concurrent run's wideboi has its own live harness as parent and is ignored. Teeth proved above.

---

## Phase 5: Run smoke's cases in a thread pool

**Files:**
- Modify: `scripts/smoke.py` — `--jobs`, a pool, a lock, ordered output,
  raised ceilings.

**Key changes:**

`--jobs` defaults to `min(8, os.cpu_count() or 1)`; `--jobs 1` keeps the
current serial path. `SPAWNED` (`:754`) is guarded by a `threading.Lock` around
its two mutation sites (`Session.__init__:89`, `Session.close:148`). Results
are collected and printed in `CASES` order, so the transcript stays diffable.
`strays()` is unchanged and still runs after every case (`:791-796`).

**Raise the settle ceilings**, because contention roughly doubles per-case
latency (case-seconds 42.9 → 89.3 at width 8) and `settle_output` returning
`False` does not fail — the case proceeds with a half-drawn screen:
`Session.__init__(startup=1.2 → 4.0)` and `Session.type(settle=0.8 → 3.0)`.
**These are timeouts, not sleeps: raising them costs nothing when things are
fast** and is the whole margin under load.

```python
def run_case(item):
    name, fn = item
    problems = []
    try:
        fn(problems.append)
    except Exception as exc:  # a crashed case is a failed case
        problems.append(f"raised {exc!r}")
    return name, problems
```

**Verification — automated:**
- [x] `python3 scripts/smoke.py` passes 25/25 and is **under 20s** (48.05s baseline) — **7.29s**, better than the 13.2s spike
- [x] **Run four consecutive times, all 25/25** — the Plan 19 gate; `quiet=0.10` passed 25/25 once and then failed three runs running — **25/25 x4 at 7.31 / 7.27 / 7.27 / 7.27s**
- [x] `python3 scripts/smoke.py --jobs 1` still passes 25/25 — the serial path is not abandoned — **25/25 in 43.50s**
- [x] `python3 scripts/smoke.py --only "focus"` runs and reports a correct count — **3 passed.** The old tally printed `len(CASES) - len(failures)`, i.e. 25, under any filter. Rewriting the tally for the pool made this unavoidable rather than a drive-by, per the spec.
- [x] Output is identical in case order between `--jobs 1` and `--jobs 8` (diff the OK/FAIL lines) — **identical**
- [x] `make check` passes — **41.85s, from 83s, still fully serial at the make level**

**Verification — manual:**
- [x] Confirm no case's assertions changed — only the runner and the ceilings. — **confirmed; the diff to `smoke.py` is imports, `NEVER_SOCK`, the lock, two ceiling defaults and `main`**

---

## Phase 6: Fan out verify-exit, parallelise check, record the numbers

**Files:**
- Modify: `Makefile` — fan out `verify-exit`, make `check` parallel.
- Modify: `docs/dev-sessions/.../notes.md`.

**Key changes.** `verify-exit`'s six invocations (`Makefile:109-115`) run
concurrently and the target fails if any does. **Measured with Phase 2 in
place: 6/6 pass, three consecutive iterations (18 runs).**

```make
verify-exit: build
	@pids=""; \
	for spec in "80x24 SIGTERM" "4x2 SIGTERM" "1x1 SIGTERM" "0x0 SIGTERM" \
	            "80x24 SIGINT" "80x24 SIGHUP"; do \
		set -- $$spec; \
		python3 scripts/ptycheck.py --size $$1 --signal $$2 & pids="$$pids $$!"; \
	done; \
	rc=0; for p in $$pids; do wait $$p || rc=1; done; exit $$rc
```

`check` delegates to a parallel sub-make so everyone gets the speedup without
having to remember `-j`:

```make
CHECK_JOBS ?= 8
check:
	@$(MAKE) -j$(CHECK_JOBS) check-targets
check-targets: fmt-check lint seam-check test race verify-exit smoke attach-check
```

**Verification — automated:**
- [x] `make check` passes and is **under 25s** (83s baseline) — **12.2s**
- [x] **`make check` four consecutive times, all green** — the gate for this whole plan — **12.20 / 12.23 / 12.31 / 12.23s**
- [x] `env -u TERM make check` passes (CI runners set no `TERM`) — **PASS, 51 OK lines**
- [x] `make CHECK_JOBS=1 check` passes — the serial path still works — **27.72s**
- [x] `make quick` still passes — **0.44s cached**
- [x] `build` runs exactly once under the parallel sub-make (check the output for a single `go build` line) — **exactly 1**

**Verification — manual:**
- [ ] CI green **twice** on Linux before merge, not once — 4-core runners are the untested configuration. *(pending: checked at PR time)*

---

## Spec coverage check

| Spec requirement | Phase |
| --- | --- |
| `make check` under 25s | 5, 6 |
| `WIDEBOI_SOCK` override, all three callers | 3 |
| smoke/golden immune to any server | 3 |
| `make smoke` passes with a server running | 3 |
| Parallel smoke, `--jobs`, threads | 5 |
| `SPAWNED` lock, ordered output, strays last | 5 |
| Raised settle ceilings | 5 |
| attachcheck stays internally serial | 3 (unchanged elsewhere) |
| verify-exit fan-out | 6 |
| Repeat runs as the gate | 5, 6 |
| **Shutdown deadlock fixed here** (Les's call) | 1, 2 |
| ptycheck stray scan scoped | 4 |
