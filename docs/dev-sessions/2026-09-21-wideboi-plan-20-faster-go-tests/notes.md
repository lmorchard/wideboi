# Plan 20 — notes

The Go suite was 16.2s and 77% of it was sleeping. It is now **4.03s**, and
`make check` went 90.8s → 78.1s. There is a `make quick` at **4.53s** for the
edit loop.

## Numbers

| | before | after |
| --- | --- | --- |
| `internal/server` | 15.4s | **2.69s** |
| `internal/server/ptyx` | 11.5s | **3.88s** |
| `internal/server/term` | 3.57s | **0.63s** |
| `cmd/wideboi` | 2.28s | 2.28s (untouched, deliberately) |
| **`go test -count=1 ./...`** | **16.2s** | **4.03s** |
| **`make check`** | **90.8s** | **78.1s** |
| `make quick` | — | **4.53s** cold |

`make check` ran four times — 78.59 / 78.05 / 78.17 / 81.66s, the third with
`TERM` unset. The first three were taken at the end of Phase 4; **the 81.66s
run is the one that covers the tree actually shipped**, since it came after
the self-review fixes. The 78.1–81.7s spread is machine noise, and the number
to quote against the 90.8s baseline is the slowest, not the prettiest.

No assertion changed anywhere in this branch.

## The issue was wrong, and measuring is what caught it

#54 — which I filed an hour before starting — said this was one injectability
problem: a hardcoded `CloseGrace`. It was three problems, and the fix for the
second one was nothing at all.

Setting `CloseGrace` to 100ms and running the suite took `internal/server`
from 15.4s to 2.74s and left `ptyx` at **11.5s, unchanged**. `ptyx.Pane.Kill`
already takes the grace as a parameter (`reap.go:135`); its tests just passed
their own `2 * time.Second`. So ptyx needed **no production change**, only
four different literals.

Two experiments, ninety seconds, before a line of the spec was written. The
alternative was speccing an injectability mechanism for a package that already
had one.

## Where the time actually went

`Kill`'s `waitForExit(grace)` returns early on `<-p.done`, so `Kill` costs
~`grace` only for a child that *ignores* SIGTERM. Every affected test spawns an
interactive `/bin/sh`, which does. Hence the tight 2.03–2.07s cluster across
two packages.

## Zero-means-default is the subtle part

The grace resolves in `Pane.graceOrDefault()` at the point of use, not in a
constructor. That is forced, not stylistic: three white-box fixtures build
`&Pane{}` literals directly (`status_test.go:66-79`,
`transport_close_test.go:35,75`, `pane_wedge_test.go:100-108`), and a
constructor cannot reach them. Resolve in the constructor and those three
silently tear down with a **0s** grace and stop exercising the real path —
green, faster, and testing less.

`TestZeroCloseGraceResolvesToTheDefault` exists only to pin that. `term`'s
idle timeout resolves the same way in `Status()`, for the same reason.

## export_test.go, so the knob is not product API

`SetCloseGrace` lives in `internal/server/export_test.go`, compiled only into
the test binary. `go doc internal/server Server` lists `SetLayout` and not
`SetCloseGrace`, which is the check that it worked.

The cost: `cmd/wideboi` is a different package and cannot reach it, so its one
2.05s test stays slow. That is free — `ptyx` at 3.88s is the critical path and
`cmd/wideboi` runs in parallel underneath it. Fixing it would have required
the public setter this was avoiding.

## The mutation proof failed, and the failure is the interesting part

Phase 2's plan said: shorten four teardown literals, then break the kill path
and watch those tests go red. **They never went red.** Three mutations:

| mutation | result |
| --- | --- |
| `signalRoot` drops SIGKILL | all 7 pass |
| `signalDescendants` drops SIGKILL | 2 reap tests fail; the 4 shortened ones pass |
| **all** signalling neutered | 1 reap test fails; the 4 shortened ones pass |

`Kill` calls `p.Master.Close()` (`reap.go:149`) *before* the SIGKILL pass, and
closing the pty master reaps a plain `/bin/sh` by itself. So the four shortened
tests had **no reaping teeth at 2s either** — shortening them removed nothing,
which is what the checkbox wanted to establish, arrived at backwards.

Recorded as `[!]` in `plan.md` rather than reworded into a win.

### The part that outlives this session

Under the third mutation — *all* of `Kill`'s signalling removed —
**`TestKillReapsEscapedGrandchild` and `TestKillReapsSIGTERMIgnoringEscapee`
still pass**, stable over three runs. The pty master close is what reaps their
escapees, not the signalling those tests are named for.

Note also that removing *more* made *fewer* tests fail (mutation 3 vs 2), which
means the interaction is not understood, not merely incidental.

This is a pre-existing hole in the anti-leak coverage and has nothing to do
with this branch — but it is the same shape as the note in Plan 19's notes:
*"The escapee did die in the probe, via SIGHUP when the pty master closed. That
is incidental, not guaranteed."* Filed as **#55**. **Not fixed here**:
strengthening those tests is a change to what the suite guarantees, and it
deserves judging on its own.

Phase 3's mutation, by contrast, worked exactly as designed — latching
`sawOSC133` on an unrecognised payload makes the test fail with its own
message. Same technique, opposite outcome, which is why it is worth running
rather than assuming.

## Why `make quick` has no package list

An exclusion list (`go list ./... | grep -v …`) was measured at 0.36s for the 8
pure packages. It was dropped: once the Go tests stopped waiting out grace
periods the whole suite is 4s, so the useful boundary became "Go tests" vs
"race + pty suites" — stable, where a package list drifts. A new package joins
`quick` automatically, and a new slow test makes `quick` slower, which is
noticeable and self-correcting rather than silent.

**The two halves of #54 interacted**: doing the sleep fix first made the
tiering strictly simpler. Tiering first would have shipped the list.

## Deliberately not done

- **`cmd/wideboi`'s 2.05s test** — needs the public setter, is not on the
  critical path.
- **Strengthening the escapee tests** — see above; filed.
- **The other nine hardcoded durations** inventoried in `research.md` §6
  (`killWait`, `aliveWait`, the 33ms/1000ms server ticks, 16ms draw ticks).
  None is what these tests paid; the measurement says so.
- **`make -j`** — smoke, attach-check and verify-exit all scan the process
  table and bind fixed paths.
- **A shared `newTestServer` fixture** for the white-box literals.

## Where to pick up

- `make check` is now 78.1s, of which **smoke is 44s**. It is the only thing
  left worth attacking, and it is no longer sleep-bound — so the lever is
  parallelism, with the two obstacles recorded in #51.
- **#52** (idle render trickle, ~1.1 KB/s while idle) still has the most value
  beyond the test suite.
- **#55** (escapee reap tests have no teeth) is small and self-contained.
- `testGrace = 100ms` is the knob, declared in `server_test.go` and
  `ptyx/pane_test.go`. If CI proves it tight, raise it — a child that dies
  promptly returns early regardless, so the cost of a larger value is zero in
  the common case.
