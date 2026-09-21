# Plan 19 — notes

`make check` was ~4 minutes and we lean on it constantly. It is now **91
seconds**, and the suites are less flaky than they were rather than more.

## Numbers

| target | before | after |
| --- | --- | --- |
| smoke | 158.4s | **44.4s** |
| attach-check | 53.5s | **8.1s** |
| verify-exit | 17.2s | 17.1s |
| race | 17.5s | 17.5s |
| test | ~30s cold | 16.5s |
| golden | 4.6s | 4.6s |
| **`make check`** | **252s** | **90.8s** |

Untouched targets are unchanged, which is the point — only the waiting
changed, never an assertion.

## It was never Python

Les's first guess was the Python harness, and mine would have been too. It
was **fixed sleeps**: 25 sessions × 1.2s startup, 70 keystroke groups × 0.8s
settle, 24 teardowns × 2.0s. About 116s of smoke's 158s was spent asleep. A
Go rewrite would have carried every one of those across unchanged and
delivered nothing.

Three changes, in descending payoff:

1. **Wait for the screen to stop changing**, with the old fixed values as
   *timeouts* rather than durations. Measured need: startup 0.154s against
   1.2s, a keystroke 0.10–0.28s against 0.8s.
2. **Stop 24 cases paying for a teardown one case asserts.** `quit_and_reap`
   costs 2s because an interactive `/bin/sh` ignores SIGTERM, so teardown
   always burns its grace. `verify-exit` and `case_quit_restores_and_reaps`
   assert that; nothing else needed to.
3. **Same treatment for attach-check**, where `PROMPT_WAIT` was 8 seconds.

## Two findings worth more than the speed

**wideboi emits ~1.1 KB/s while completely idle** — filed as #52. Naive idle
detection fails outright because the pty is *never* quiet: ~62 cursor
hide/show pairs a second, which is the 16 ms render tick. `main.go` renders
and flushes unconditionally and the renderer wraps every frame in a
hide/show, so a frame where nothing changed still costs 12 bytes. Over SSH
that is 1.1 KB/s for a static screen.

`_FRAME_NOISE` in `ptylib.py` exists only to work around this and **should be
deleted when #52 is fixed.** Noted in both places.

**`PROMPT_WAIT = 8.0` was waiting for something that stopped happening.** Its
comment justified 8 seconds as covering a themed zsh's slow first prompt —
but `ptylib` pins `SHELL=/bin/sh`, so that has not been the risk since the
pin landed. A comment that explains a value outliving its reason is how
values like this survive.

## The fast number was the wrong number

`quiet=0.10` gave **78.1s** and passed 25/25 on the first run. It then failed
one or two cases on each of the next three — three *different* cases, never
the same twice:

```
control mode is visible and escapable    / leaving control mode did not restore the cursor
ctrl repeat moves two columns            / focus did not reach pane 1 after C-h C-h: got pane 4
help overlay opens and any key dismisses / could not get back to typing after dismissing help
```

Multi-step operations have gaps wider than 100 ms between bursts. `quiet=0.25`
is stable over four consecutive runs and costs ~14s against `0.10`.

**The first run passing 25/25 is exactly how a flaky suite presents**, and one
green run would have shipped it. The only reason this was caught is running it
repeatedly on purpose.

## The leak guard

Phase 2's whole risk is trading 47 seconds for leaked processes, and nothing
would have noticed. The runner now fails if any process *it spawned* is still
alive at the end.

Scoped to this run's pids rather than grepping `ps` for `wideboi`: the global
version false-positived immediately on a stray from one of my own probe
scripts. **A check that cries wolf gets ignored, which is worse than not
having one.**

It was also proved to fail — spawning a session and deliberately not closing
it makes `strays()` report it. A leak check nobody has watched fail is not a
leak check.

## Deliberately not done

- **Parallelism and pytest.** Projected savings were achievable without
  either, and doing both at once means not knowing which broke something.
  Two real obstacles remain recorded in #51: `attachcheck` binds a fixed
  socket path so its cases must stay serial, and leak detection scans the
  whole process table so concurrent sessions can see each other's panes. The
  `pytest-xdist` dependency is easier to judge against a measured 91s than a
  guess.
- **Fixing #52.** It would make `_FRAME_NOISE` unnecessary, but it is a
  product behaviour change and deserves judging on its own.
- **Touching `verify-exit` / `ptycheck.py`.** Their waits are the thing under
  test.

## Where to pick up

- **#52** is the one with value beyond the test suite.
- The remaining 91s is now `smoke` 44s, `race` 17.5s, `verify-exit` 17.1s,
  `test` 16.5s. Nothing is dominated by sleeps any more, so the next lever
  genuinely is parallelism — `make -j` across the independent targets is
  nearly free and bounded by `smoke`.
- `quiet=0.25` is the tuning knob, in `ptylib.settle_output`. Judge any change
  to it by repeat runs, not one green.
