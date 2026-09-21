# Plan 19 — Make the pty checks wait for the app, not the clock

**Goal:** Cut `make check` from ~4 minutes to well under one, by replacing
fixed sleeps with waits on observed state — which also removes the timing
fragility that broke CI twice this week.

**Source:** issue #51, and Les: *"it seems like `make check` is very slow …
could these be run in parallel and/or converted to go?"*

## Current state

Measured on an M-series Mac, warm caches:

| target | time | share |
| --- | --- | --- |
| **smoke** | **158.4s** | **63%** |
| attach-check | 53.5s | 21% |
| race | 17.5s | 7% |
| verify-exit | 17.2s | 7% |
| golden | 4.6s | 2% |
| fmt-check / lint / seam-check | <0.3s each | ~0% |

**Total ≈ 252s.** It is not Python. `smoke.py` has 25 sessions and 70
`type()` calls, and every one of them sleeps a fixed duration:

| fixed cost | value | measured need | count | waste |
| --- | --- | --- | --- | --- |
| `Session(startup=)` | 1.2s | **0.154s** | 25 | ~26s |
| `type(settle=)` | 0.8s | **0.10–0.28s** | 70 | ~43s |
| `quit_and_reap` | 2.04s | ~0s for cleanup | 24 | ~47s |

That is ~116s of the 158s spent asleep. A Go rewrite would carry every one of
those sleeps across unchanged.

### Why naive idle detection does not work, and what it exposed

The obvious fix — wait until the pty goes quiet — fails outright: **wideboi
never goes quiet.** While completely idle it emits **1,134 bytes/second**,
almost entirely `ESC[?25l` / `ESC[?25h` pairs at ~62/s, which is exactly the
16 ms render tick. `cmd/wideboi/main.go` calls `Draw` + `Render` + `Flush`
unconditionally every frame, and the renderer wraps each frame in a
hide/show pair even when no cell changed.

That is a product defect in its own right — 1.1 KB/s forever, over SSH, for a
screen that is not changing — and it gets its own issue rather than being
fixed here.

### The teardown cost is not what it looks like

`quit_and_reap` takes 2.04s, and **none of it is the panes**: `still_alive`
returns in 0.000s. It is wideboi itself. An interactive `/bin/sh` ignores
SIGTERM, so pane teardown always burns the full grace period before
escalating to SIGKILL. That is correct behaviour, and `verify-exit` exists to
assert it — but **24 of the 25 smoke cases pay for it and only one asserts on
it** (`case_quit_restores_and_reaps`, the only reader of `s.leaked`).

## Desired end state

- `make check` well under a minute, dominated by `race` rather than by sleeps.
- Waits are bounded by a timeout and satisfied by observation, so a slow CI
  runner takes longer rather than failing.
- The one case that asserts teardown still asserts it, unchanged.
- No new dependency; `make check` still runs on a bare `python3`.

## Design decisions

- **Decision: wait until output stops changing, ignoring cursor show/hide.**
  A new `ptylib` helper polls the drained buffer and returns once the
  *meaningful* byte count has been stable for a short quiet window.
  - **Why ignore `ESC[?25[hl]`:** it is the every-frame trickle above, so
    including it means nothing is ever quiet. Filtering it from the *change
    metric* does not remove it from `output()`, so `cursor_visible` and every
    existing assertion still see it.
  - **Rejected:** fixing the renderer first. It would make this unnecessary,
    but it is a behaviour change to the product in service of test speed, and
    it deserves to be judged on its own.

- **Decision: existing `settle=` and `startup=` values become timeouts, not
  durations.** A call that passes `settle=1.3` now waits *up to* 1.3s and
  returns as soon as the screen is stable.
  - **Why:** every existing override was chosen as "long enough for this to
    finish", which is exactly the right ceiling. Reusing them preserves the
    per-case judgement already encoded and keeps the diff small.
  - **Risk accepted:** a case that needs elapsed time with *no* output change
    would now return early. None is known — the overrides all wait for shell
    output — and the suite will say so immediately if one exists.

- **Decision: a fast teardown for cleanup, keeping the slow one where it is
  asserted.** `Session.close()` SIGKILLs wideboi and explicitly reaps pane
  children; `quit_and_reap()` is untouched.
  - **Why:** `verify-exit` is the dedicated teardown suite and `make check`
    runs it separately. Paying 2s in 24 smoke cases to re-verify what one case
    and a whole other target already assert is the definition of waste.
  - **Constraint:** `close()` must leave no strays, or it trades 47 seconds
    for a process leak. It reaps children explicitly rather than trusting
    SIGKILL'd wideboi to have done it.

- **Decision: `attachcheck.py` gets the same treatment**, via the same shared
  helper. It is 21% of the total and has the same shape.

- **Decision: no parallelism in this change, and no pytest.**
  - **Why:** the sleep removal is projected to take smoke from 158s to ~40s on
    its own. Parallelism is a second, riskier change — `attachcheck` binds a
    fixed socket path, and leak detection scans the whole process table, so
    concurrent sessions can see each other's panes. Doing both at once means
    not knowing which one broke something.
  - Revisit `pytest-xdist` once this has landed and the remaining time is
    known; the dependency is easier to justify against a measured 40s than
    against a guess.

## Patterns to follow

- The existing pinning discipline in `ptylib.spawn_in_pty` — `SHELL`, `TERM`,
  `PS1` are all pinned because assertions depend on them. A wait helper
  belongs beside them, in the shared module, not duplicated per script.
- `still_alive` is already a poll-until-condition with a deadline; the new
  helper is the same shape for output.
- `scripts/smoke.py`'s `Session` is the only place `type` and `startup` are
  defined; `attachcheck.py` has its own `Client` with the same shape.

## What we're NOT doing

- **Fixing the idle render trickle.** Filed separately; it is a product
  change, and this change is deliberately test-only.
- **Parallel execution**, `make -j`, or pytest — see above.
- **Touching `verify-exit` or `ptycheck.py`.** They assert the teardown
  contract and their waits are the thing under test.
- **Rewriting any case's assertions.** Only the waiting changes.
- **Converting anything to Go.** The sleeps are the cost; the language is not.

## Open questions

- **Does any case depend on elapsed time rather than observed output?**
  Default: assume none, and let the suite prove it. If one surfaces, give the
  helper an explicit `min_wait` and use it only there, rather than reverting.
- **What quiet window?** Default: 100 ms, which cleared every case measured
  (0.10–0.28s to settle) with margin over a 16 ms tick. Tunable in one place
  if CI proves it tight.
