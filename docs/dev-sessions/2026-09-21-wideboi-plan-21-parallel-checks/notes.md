# Plan 21 — notes

`make check` was 83s and `make -j` failed outright. It is now **12.2s**,
stable over four runs. Two of the six phases were product/harness bug fixes
that had to land before any of the speed work was possible.

## Numbers

| target | before | after |
| --- | --- | --- |
| smoke | 48.05s | **7.29s** |
| verify-exit | 17.09s | **3.21s** |
| attach-check | 7.88s | 7.88s (now the longest single target) |
| race | 5.40s | 5.40s |
| test | 4.14s | 4.14s |
| **`make check`** | **83s** | **12.2s** |
| `make check` serial (`CHECK_JOBS=1`) | 83s | 27.7s |
| `make quick` | 4.53s | 0.44s cached |

Four consecutive `make check` runs: 12.20 / 12.23 / 12.31 / 12.23s. One with
`TERM` unset. `build` runs exactly once under `-j`.

## The blocker was never the test harness

`make -j8 check` failed reliably — in 44s against 83s serial, so the
parallelism worked and the isolation did not. Bisecting by combination:

| combination | result |
| --- | --- |
| smoke + verify-exit + race + test | pass ×2 |
| smoke + attach-check | **fail ×2** |

So not CPU contention, which is what I expected going in. Two independent
causes, both real bugs.

### wideboi hangs forever on SIGINT when backgrounded

The one worth the session. POSIX requires a shell to set SIGINT/SIGQUIT to
`SIG_IGN` for an asynchronous list, so anything started as `cmd &` — which is
every target under `make -j` — inherits them ignored.

`signal.Notify` installs a Go handler *over* `SIG_IGN`, so the guard catches
the signal and the session tears down correctly. Then `signal.Stop` +
`signal.Reset` faithfully restore the original disposition, `SIG_IGN`, and the
re-raise is discarded. The process lives forever with the terminal already
restored, immune to every signal it had armed — only SIGKILL/SIGABRT touch it.

Instrumented:

```
guard: signal received interrupt
guard: Stop() returned            (2.0s later -- CloseGrace)
guard: reset done, re-raising
guard: re-raise returned (STILL ALIVE)
```

**`wideboi &` then Ctrl-C hung forever, today, for users.** It was only ever
going to show up in a test that backgrounds things.

Three things this cost me, all worth remembering:

1. **The first test used SIGTERM and failed for the wrong reason** — the child
   died by the signal. Go's runtime respects an inherited `SIG_IGN` only for
   **SIGHUP and SIGINT** (`sigInstallGoHandler`) and installs its own handler
   over an ignored SIGTERM. SIGTERM cannot reproduce this at all.
2. **The first fix was racy** and `TestGuardArmRestoresAndReRaises` caught it.
   Exiting *after* a failed re-raise looks obvious, but `Kill` can return
   before the signal is delivered, so `os.Exit` sometimes won and the process
   exited 143 instead of dying by SIGTERM. Deciding up front — sample
   `signal.Ignored` at Arm time, before `Notify` — is race-free. **The
   existing test is what stopped me shipping that.**
3. **My manual verification reported a false "STILL ALIVE"**: it used
   `kill(pid, 0)`, which succeeds on a zombie, and the Python probe never
   reaped its child. The fix had been working for an hour. Use `waitpid`.

### And fixing it alone would have made things worse

Phase 1 turns the hang into `os.Exit(128+signo)` — but `ptycheck` asserts the
process died **by** the signal, so a backgrounded run then fails with
`exited normally with status 130, expected death by signal 2`. Measured, not
predicted.

So `spawn_in_pty` pins the child's dispositions to `SIG_DFL`, beside the
existing `SHELL`/`TERM`/`PS1` pins and for exactly the same stated reason: the
assertions depend on it. A test that asserts "died by SIGINT" cannot run where
SIGINT does nothing — pin the disposition, don't relax the assertion.

## The socket was the other half

`cmd/wideboi/main.go` auto-attaches to `$TMPDIR/wideboi-$UID/default.sock` if
anything answers, and attachcheck binds that identical path. Measured: with a
server up, a plain wideboi has **zero pane children** instead of two.

**This is a live foot-gun: with a `wideboi server` running in another
terminal, plain serial `make smoke` failed 9 cases.** Anyone using wideboi
while developing it hits it, and the failures point nowhere near the cause.

`WIDEBOI_SOCK` overrides the path. smoke and golden point at a path inside a
fresh temp dir that is **never created** — they only dial, so that makes them
immune to any server anywhere rather than merely non-colliding with
attachcheck's. attachcheck gets one private path per run, because its cases
assert *exclusive* ownership of the socket and a shared path was never going
to hold.

Deliberately an escape hatch, not a design — #27 is real session naming, #58
is configuration as a topic.

## Corrections I had to make mid-flight

Worth recording because each was a confident claim that measurement overturned.

- **"The stray failures are false positives."** Mostly they were *true*
  positives: the other five runs were correctly reporting the wedged process
  from the SIGINT run. The scan is separately buggy, but I proved that with a
  different test.
- **"The SIGINT hang needs a concurrent run."** It does not — a single
  backgrounded run wedges. Concurrency was incidental; backgrounding was the
  trigger.
- **"The fan-out is ~4 lines."** It was two bug fixes and a rewritten guard.
  I gave Les that estimate twice before the diagnosis was done.
- **"`TMPDIR` isolation will fix ptycheck."** It did not — the ptycheck
  failures had nothing to do with sockets.

## ptycheck's stray scan

`find_stray_wideboi` matched any process whose argv[0] equalled the absolute
binary path, and every concurrent invocation uses the same path. With one
other such process alive it reports a stray it never spawned, deterministically.

Assertion 4 is deliberately *global* — "a wideboi outliving the one this
script reaped would mean a double-fork, a hung child of a panic" — so scoping
it to a list of known pids would have deleted the assertion rather than fixed
it. Scoped by **parentage** instead: ours is still our child or orphaned onto
init; another run's still has that run's live harness as its parent.

Proved it still fires: a genuinely orphaned wideboi is reported as
`pid=... ppid=1`. Staging that took two attempts — the first orphan died
instantly because the exiting parent was also holding the pty master.

## Why the parallelism is safe, and why it would not have been this morning

Contention roughly doubles per-case latency — 42.9 → 89.3 case-seconds at
`--jobs 8` — and **nothing failed**, because Plan 19's settle-based waits are
adaptive: under load a case waits longer and still gets a correct screen,
where a fixed sleep hands back a half-drawn one.

The corollary is the hazard: those settles are *ceilings*, and
`settle_output` returning `False` does not fail. Contention eats the margin.
They went 1.2s/0.8s → 4.0s/3.0s. Raising a timeout costs nothing when things
are fast; it was the whole margin under load.

Smoke needed **no per-case socket**: a plain wideboi only ever dials, never
binds (`ListenSocket` is reached only from `runServer`), so cases cannot reach
each other. That collapsed a chunk of expected work.

## The review found five things, and two of them were mine to have caught

Copilot's pass on #59 returned five comments and **all five were real**. Two
were gaps in work I had already called done.

1. **`WIDEBOI_SOCK` made an existing unlink destructive.**
   `NewSocketListener` removes a stale socket so a dead server does not block
   the next one. Harmless while the path was a fixed `default.sock` under a
   wideboi-owned directory; once a caller can name any path,
   `WIDEBOI_SOCK=~/notes.txt wideboi server` **deletes that file** before
   failing to listen. Proved with a test that writes a regular file and
   watches it disappear. Now refused unless the path is actually a socket.
   **I added the override and did not look at what consumed it.**
2. **ptycheck never got the socket isolation.** I gave it to smoke, golden and
   attachcheck and missed the fourth. `make verify-exit` failed outright with
   a default-path server running — the same foot-gun this plan exists to fix,
   still live in one of four suites.
3. **attachcheck leaked a temp dir per run.** I had fixed exactly this for
   smoke and golden during self-review and did not check the third site.
4. **`ps_rows()` returns `[]` on failure**, so a failed process-table query
   made assertion 4 pass silently. The old `ps` shell-out reported it. I
   weakened an assertion while scoping it.
5. **Raising only the default settle left twenty explicit overrides behind**
   (0.5–1.6s), which are precisely the tight ones under contention.

## And then the gate did its job

After fixing those, `make check` failed **1 run in 8** — two different
failures in different suites. The flake rate across all the runs of that
configuration was roughly 15%. Four green runs earlier had said it was fine.

**This is the second time today that pattern has appeared**, and it is the
single most valuable habit from #51: a fast green number is worth nothing
until it repeats. Both causes were real races that contention exposed, and
both were fixed rather than tuned around:

- **`ptycheck` slept a fixed 0.5s and then asserted two pane children
  existed.** Under `make -j` wideboi has not always got there. Now waits for
  the panes with the delay as a ceiling — the same fixed-sleep bug #51
  removed elsewhere, left behind because #51 scoped ptycheck out on the
  grounds that "its waits are the thing under test". Its *teardown* waits are.
  This one was not.
- **`Session()` returned before either pane shell had prompted.** Settling
  only says wideboi's own chrome stopped changing, and it draws that
  immediately. A case that typed into a pane with no shell behind it got no
  echo — surfacing as "lowercase input never reached the pane". Startup now
  waits for both prompts. wideboi emits exactly one `$` of its own (the
  DECRQM query `ESC[?2027$p`); every other one on a fresh screen is a prompt,
  because `PS1` is pinned.

**10/10 `make check`** after those, plus 4/4 with `TERM` unset — 14
consecutive green runs across configurations.

Note what this means about the earlier numbers: the parallelism was never
*quite* safe until the last two fixes, and four green runs had already told
me it was.

## Deliberately not done

- **#27 / #58.** `WIDEBOI_SOCK` is one escape hatch; session naming and
  configuration deserve their own sessions.
- **Parallelising attachcheck's cases.** Two of them assert exclusive
  ownership of the socket path — contradictory under concurrency by
  construction. It is now the longest single target at 7.88s, so this is where
  the next win is if anyone wants one.
- **pytest / xdist.** ~40 lines of `ThreadPoolExecutor` did it.

## Where to pick up

- **attach-check at 7.88s is now the critical path** for `make check`.
- **#43** should be updated, not closed: it asks whether any path to an
  unkillable `srv.Close()` survives. This was *not* that — `srv.Close()`
  completed in 2.0s — so its actual question is still open.
- `--jobs` and `CHECK_JOBS` are the knobs. Judge changes to either by repeat
  runs, not one green.
