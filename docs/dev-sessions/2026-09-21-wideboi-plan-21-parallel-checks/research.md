# Plan 21 — research

Measurements first; they decided the design. Codebase facts follow.

## Measured

### `make -j8 check` fails, but the parallelism works

44s against 83s serial, so the speedup is real and the isolation is not.

| combination | result |
| --- | --- |
| smoke + verify-exit | pass ×2 |
| smoke + verify-exit + race + test | pass ×2 |
| **smoke + attach-check** | **fail ×2** |
| smoke + attach-check, pre-built (no concurrent `go build`) | **fail ×2** |
| **smoke + attach-check, separate `TMPDIR`s** | **pass ×2** |

CPU contention is not the cause. Neither is the concurrent `go build`
(`build` is `.PHONY`, so each `make <target>` does rebuild — but pre-building
and running the scripts directly still fails).

### Root cause: auto-attach to a machine-global socket

`cmd/wideboi/main.go:255-258` — plain `wideboi` dials the default socket and
attaches if anything answers:

```go
sockPath := defaultSocketPath()
if conn, err := net.Dial("unix", sockPath); err == nil {
	conn.Close()
	return runAttach(sockPath)
}
```

`defaultSocketPath()` (`main.go:27-31`) is
`$TMPDIR/wideboi-$UID/default.sock`; attachcheck's `runtime_dir()`
(`scripts/attachcheck.py:60-67`) computes the identical path. Proved directly:

```
no server running:  pid=78606  descendants=[/bin/sh, /bin/sh]  -> own session
server bound:       pid=78614  descendants=[]                  -> AUTO-ATTACHED
```

**Live foot-gun, not just a parallelism problem:** with a `wideboi server`
running in another terminal, plain serial `make smoke` fails **9 cases**.

Because `os.TempDir()` honours `$TMPDIR`, separate `TMPDIR`s already fix it —
which is the proof the diagnosis is right, and a fallback if the env override
is rejected.

### Plain wideboi never *binds*, so smoke cases cannot collide with each other

`ListenSocket` is reached only from `runServer` (`main.go:133`, via
`main.go:49`). `run()` only dials. So the 25 smoke cases are mutually
isolated already; they collide only with an *external* server.

**This is the key simplification: intra-smoke parallelism needs no per-case
socket.**

### Parallel smoke works, and is stable

Throwaway `ThreadPoolExecutor` over `smoke.CASES`, with a lock around the
`SPAWNED` mutations:

| width | wall | runs |
| --- | --- | --- |
| serial | 42.9–48s | baseline |
| 4 | 14.7 / 14.8 / 14.8 / 14.7s | **25/25 ×4** |
| 8 | 13.3 / 13.4 / 13.3 / 13.4s | **25/25 ×4** |

Eight consecutive runs, no failures, timings within 0.1s. Width 8 buys only
1.4s over width 4.

### Why it is stable — and where the risk actually is

| | serial | width 8 |
| --- | --- | --- |
| wall | 42.9s | 13.2s |
| **case-seconds** | 42.9 | **89.3** |
| slowest case | 3.5s | 5.5s |

Contention roughly **doubles per-case latency**, and nothing failed. Plan 19's
settle-based waits are adaptive: under load a case waits longer and still gets
a correct screen, where a fixed sleep would have handed back a half-drawn one.
**Parallelism would have been flaky before #53; the two changes compose.**

The hazard that follows: those settle values are *ceilings*
(`Session.__init__(startup=1.2)`, `Session.type(settle=0.8)`), and
`settle_output` returning `False` does not fail — the case proceeds with
whatever it has (`smoke.py:98,100-102`). Contention eats the margin. Since
they are timeouts rather than sleeps, **raising them costs nothing when things
are fast.**

### Per-target serial baseline

| target | time |
| --- | --- |
| smoke | 48.05s |
| verify-exit | 17.09s |
| attach-check | 7.88s |
| race | 5.40s (was 17.5s before #56) |
| test | 4.14s |
| fmt-check / lint / seam-check | <0.3s each |

Serial total 83s. With smoke parallelised, **verify-exit becomes the floor.**

## Codebase facts

### smoke.py

- `CASES` — a list of `(name, fn)` at `smoke.py:719-745`; case contract is
  `def case_x(fail)` where `fail` is `problems.append`. No return value.
- Runner loop `smoke.py:769-799` — a plain `for`, single process.
- **`SPAWNED: list[int]`** (`smoke.py:754`) is the only cross-case mutable
  global: appended in `Session.__init__` (`:89`), extended in `Session.close`
  (`:148`), read by `strays()` (`:757-766`). Never reset.
- `strays()` runs **after** the loop, inline in `main()` (`:791-796`), and is
  not a `CASES` entry — so keeping it last is natural.
- `Session` (`:83-159`) is per-instance throughout: own pty, own pid, own
  `Drainer`. Nothing shared but the read-only `./bin/wideboi` path.
- Pre-existing, noted not fixed: the pass count is `len(CASES) - len(failures)`
  (`:798`), which is wrong under `--only`. attachcheck computes this correctly
  (`attachcheck.py:358`).

### attachcheck.py

- Same runner shape (`:325-333`, `:336-360`).
- `Server.__init__` (`:73-89`) removes a stale socket file, launches
  `./bin/wideboi server`, polls for the path.
- **Two cases own the socket path exclusively** and cannot run concurrently
  with another server: `case_second_server_refuses_to_steal_the_socket`
  (`:278-294`) asserts a second server is refused, and
  `case_attach_without_a_server_says_so` (`:297-307`) asserts attach fails
  when nothing is listening — it removes only the *file*, not a live listener.
  **attachcheck's own cases must stay serial.**

### Fixed paths

| path | who |
| --- | --- |
| `testdata/golden/startup.txt` (`golden.py:22`) | golden.py only, r/w |
| `$TMPDIR/wideboi-$UID/default.sock` | attachcheck.py + the binary |
| `./bin/wideboi` | all four, read-only |

`golden.py:61` also spawns plain `./bin/wideboi`, so it is subject to the same
auto-attach collision as smoke.

### Existing conventions

- Env: `WIDEBOI_LAYOUT`, `WIDEBOI_PREFIX`, read via `os.Getenv` with a
  fallback (`main.go:116,150,267,277`). No socket override exists.
- `defaultSocketPath()` has three callers: `main.go:49` (server), `:52`
  (attach), `:255` (auto-attach).
- argparse: smoke and attachcheck both take `--only`; ptycheck takes
  `--binary/--size/--signal/--timeout/--startup-delay/--no-escapee`. **No
  script reads an environment variable for its own configuration today.**
- Makefile: `smoke:` runs `smoke.py` then `golden.py`; `verify-exit:` runs
  `ptycheck.py` **six times in a serial shell loop** (`Makefile:109-115`).
