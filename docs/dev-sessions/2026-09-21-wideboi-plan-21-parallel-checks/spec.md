# Plan 21 — Parallel checks, and the shared socket that blocked them

**Goal:** Take `make check` from 83s to under 25s by running its targets and
smoke's 25 cases concurrently — and fix the machine-global socket that makes
both impossible today, which is a live bug for anyone running wideboi while
developing it.

**Source:** issue #57, and Les: *"let's keep going with parallelism"*.

## Current state

`make check` is 83s serial. `make -j8 check` **fails reliably** — but in 44s,
so the parallelism works and the isolation does not.

The cause is not contention (smoke + verify-exit + race + test pass together,
twice) and not the concurrent `go build`. It is `attach-check`, which passes
itself while breaking the others.

**`cmd/wideboi/main.go:255-258`: plain `wideboi` auto-attaches to
`$TMPDIR/wideboi-$UID/default.sock` if anything answers.** attachcheck's
`runtime_dir()` (`attachcheck.py:60-67`) computes the identical path, so while
its server is bound, every wideboi smoke spawns silently joins *attachcheck's*
session. Measured: with a server up, a plain wideboi has **zero descendants**
instead of its two pane shells.

**This is a live foot-gun independent of parallelism: with a `wideboi server`
running in another terminal, plain serial `make smoke` fails 9 cases.**

See `research.md` for the full measurement set.

## Desired end state

- `make check` **under 25s**, from 83s, with identical coverage.
- `make smoke` passes with a `wideboi server` running in another terminal.
- **`wideboi &` does not hang forever on SIGINT/SIGTERM/SIGHUP.**
- `make -j` works, and plain `make check` still works.
- Every currently-asserted behaviour still asserted. No case deleted or
  weakened.

## Two real bugs found while proving the parallelism, and fixed here

Scope grew on Les's call once these turned out to be product/harness
correctness rather than test plumbing.

**1. `Guard.Arm` can leave an unkillable process** (`hostterm/guard.go:46-68`).
POSIX requires a shell to set SIGINT/SIGQUIT to `SIG_IGN` for an asynchronous
list, so anything started as `cmd &` — including every target under `make -j`
— inherits them ignored. `signal.Notify` installs a handler over that, so the
guard catches the signal and tears down correctly; then `signal.Stop` +
`signal.Reset` faithfully restore `SIG_IGN`, the re-raise is discarded, and
the process lives forever with the terminal already restored. All four armed
signals revert together, so it is thereafter immune to INT/TERM/HUP — verified
by sending each externally; only SIGABRT/SIGKILL touch it.

Instrumented proof:

```
guard: signal received interrupt
guard: Stop() returned            (2.0s later -- CloseGrace)
guard: reset done, re-raising
guard: re-raise returned (STILL ALIVE)
```

This answers the open question in **#43** — "whether the wedge chain has any
surviving path to an unkillable `srv.Close()`" — with a *no*: `srv.Close()`
completes in 2.0s. The hang is entirely in the re-raise.

**2. `spawn_in_pty` lets the child inherit those dispositions**
(`ptylib.py:159-198`). A test that asserts "died by SIGINT" cannot run under a
disposition where SIGINT does nothing — which is why serial `make verify-exit`
passes (foreground) and a backgrounded one wedges.

Measured foreground vs background:

| | SIGINT | SIGQUIT |
| --- | --- | --- |
| foreground | handler | SIG_DFL |
| background (`&`) | **SIG_IGN** | **SIG_IGN** |

**3. `find_stray_wideboi` is not scoped to this run** (`ptycheck.py:98-128`).
It matches any process whose argv[0] equals the absolute binary path. With one
other such process alive it reports a stray it never spawned — proved
deterministically. Same shape as the guard fixed in `smoke.py` in Plan 19.

## Design decisions

- **Decision: a `WIDEBOI_SOCK` env override in `defaultSocketPath()`**
  (`main.go:27-31`), honoured by all three callers (`:49` server, `:52`
  attach, `:255` auto-attach).
  - **Why:** it fixes the user-facing foot-gun, not just the tests, and it
    follows the repo's existing `WIDEBOI_LAYOUT` / `WIDEBOI_PREFIX` convention
    of `os.Getenv` with a fallback. One function, three call sites.
  - **Rejected: per-suite `TMPDIR`.** It is *proved to work* (smoke +
    attachcheck pass twice with separate `TMPDIR`s) and needs no production
    change — but it is implicit, blunt (it redirects every temp file the child
    shells touch), and silently stops working if anyone forgets to propagate
    it. Kept as the documented fallback.
  - **Explicitly not** an attempt at #27. This is a single escape hatch, not a
    session-naming design, and should not preempt one.

- **Decision: smoke and golden point `WIDEBOI_SOCK` at a path that will never
  exist.** They only ever dial, never bind.
  - **Why:** it makes them immune to *any* server anywhere, including the
    developer's, rather than merely non-colliding with attachcheck's.

- **Decision: smoke's 25 cases run in a `ThreadPoolExecutor`, `--jobs`
  defaulting to `min(8, os.cpu_count() or 1)`.**
  - **Why threads, not processes:** cases are I/O-bound on pty reads, which
    release the GIL. Measured 42.9s → 13.2s at width 8, **25/25 across eight
    consecutive runs** at widths 4 and 8, timings within 0.1s.
  - **Why no per-case socket:** plain wideboi never *binds* — `ListenSocket`
    is reached only from `runServer` (`main.go:133`). The cases are already
    mutually isolated.
  - **Width 8 buys only 1.4s over width 4**, so the default is a ceiling, not
    a target, and CI's 4-core runners land near width 4 by themselves.

- **Decision: raise the settle *ceilings* before parallelising.**
  - **Why:** contention roughly doubles per-case latency (case-seconds go
    42.9 → 89.3 at width 8). `Session.__init__(startup=1.2)` and
    `Session.type(settle=0.8)` are timeouts, and `settle_output` returning
    `False` does not fail — the case just proceeds with a half-drawn screen
    (`smoke.py:98,100-102`). **Raising a timeout costs nothing when things are
    fast** and is the whole margin under load.
  - This is why parallelism is safe *now* and would not have been before #53:
    settle-based waits absorb contention where fixed sleeps would not.

- **Decision: `SPAWNED` gets a lock; `strays()` still runs after every case.**
  - `SPAWNED` (`smoke.py:754`) is the only cross-case mutable global, mutated
    in `Session.__init__:89` and `Session.close:148`, read by `strays():766`.
  - `strays()` is already inline in `main()` after the loop (`:791-796`), not
    a `CASES` entry, so it stays last for free.

- **Decision: results are collected and printed in `CASES` order**, not as
  they complete.
  - **Why:** a stable, diffable transcript. At ~13s there is no value in
    streaming.

- **Decision: attachcheck stays internally serial.**
  - **Why:** `case_second_server_refuses_to_steal_the_socket` (`:278-294`) and
    `case_attach_without_a_server_says_so` (`:297-307`) each assert something
    about *exclusive* ownership of the socket path. They are contradictory
    under concurrency by construction. At 7.9s it is not the bottleneck.

- **Decision: fan out `verify-exit`'s six `ptycheck` runs** (`Makefile:109-115`).
  - **Why:** once smoke is ~13s, verify-exit's 17.1s serial loop *is* the
    floor, so leaving it serial caps the whole change at ~20s.
  - **Beyond the literal scope Les approved**, so it is its own phase and can
    be dropped without touching the others.

## Patterns to follow

- `os.Getenv` with a fallback, as `WIDEBOI_LAYOUT`/`WIDEBOI_PREFIX` do
  (`main.go:116,150`).
- The env overlay already plumbed through `spawn_in_pty`
  (`ptylib.py:196-197`), which `case_custom_prefix_from_env`
  (`smoke.py:372-384`) already uses.
- Plan 19's gate: **judge by repeat runs, not one green.** That rule caught
  `quiet=0.10`, and it is the reason this spec trusts the parallel numbers at
  all.

## What we're NOT doing

- **Fixing #27.** `WIDEBOI_SOCK` is an escape hatch; session naming is a
  product design and deserves its own session.
- **Parallelising attachcheck's cases** — see above; contradictory by
  construction.
- **Parallelising the Go test suite further.** `go test` already runs packages
  concurrently and the whole suite is 4.1s.
- **Fixing smoke's `--only` pass-count bug** (`smoke.py:798` uses
  `len(CASES)`), noted in `research.md`. Adjacent, pre-existing, and only
  touched if the parallel runner rewrite makes it unavoidable — in which case
  say so rather than sneaking it in.
- **Switching to pytest/xdist.** A ~40-line thread pool does this; a
  dependency does not earn its keep against that.
- **Touching what any case asserts.**

## Open questions

- **Does parallel smoke hold on CI's 4-core Linux runners?** Cannot be tested
  locally. **Default: ship it and let CI say so** — `--jobs` defaults from
  `cpu_count()`, so runners self-limit, and the fallback is pinning `--jobs 2`
  in the Makefile. CI must be green twice before merge, not once.
- ~~**Do the six `ptycheck` runs tolerate concurrency?**~~ **Answered.** They
  did not, for two reasons now understood and fixed in phases 1/2/4: the
  signal-disposition hang and the unscoped stray scan. Neither was the socket
  — separate `TMPDIR`s made no difference. With the disposition pin in place,
  **6/6 pass across three consecutive iterations (18 runs).**
