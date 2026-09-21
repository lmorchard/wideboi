# Plan 20 — Faster Go tests, and a fast tier to run them in

**Goal:** Cut the Go suite from 16.2s to under 6s so the inner loop is usable,
and add a `make quick` tier for it — without weakening any assertion about
process teardown.

**Source:** issue #54, and Les: *"separate unit and integration tests, if only
for local work… 90s is still quite long for a local iteration dev loop."*

## Current state

`go test -count=1 ./...` is **16.2s at 23% CPU** — sleeping, not computing.
`make check` is 90.8s and has no fast/slow tier at all (`Makefile:28` is one
flat list of eight targets).

Measured (see `research.md` for the four experiments):

| package | cold | why |
| --- | --- | --- |
| `internal/server` | 15.4s | 8 tests × 2s `CloseGrace` at incidental teardown |
| `internal/server/ptyx` | 11.5s | 5 tests × 2s, from **test-local literals** |
| `internal/server/term` | 3.57s | one `time.Sleep(3100ms)` |
| `cmd/wideboi` | 2.28s | one test; 15 others are 0.00s |
| the other 8 packages | **0.36s** | |

**#54 framed this as one injectability problem. It is three separate causes.**
Setting `CloseGrace` to 100ms takes `internal/server` to 2.74s and leaves
`ptyx` at 11.5s unchanged — because `ptyx.Pane.Kill(grace)`
(`ptyx/reap.go:135`) already takes the grace as a parameter and its tests
simply pass their own `2 * time.Second`.

The grace is spent because `waitForExit(grace)` (`ptyx/reap.go:193`) returns
early on `<-p.done`, so `Kill` only costs ~`grace` for a child that *ignores*
SIGTERM. Every affected test spawns an interactive `/bin/sh`, which does.

## Desired end state

- `go test ./...` cold in **under 6s**; the inner loop after editing
  `internal/server` drops from 15.7s to ~3s.
- `make quick` = `fmt-check lint seam-check test`, ~5s, for the edit loop.
- `make check` unchanged in coverage, ~78s.
- Every test that currently asserts grace/escalation/reaping still asserts it,
  at full price. `make verify-exit` untouched.

## Design decisions

- **Decision: a `closeGrace` field on `Server`/`Pane`, exposed to tests via
  `export_test.go` rather than a public setter.**
  - **Why:** it is a config field (Les's call, over a mutable package `var`,
    which would also race between parallel tests in a package). `export_test.go`
    is compiled only into the test binary, so the knob costs **zero production
    API surface** — and nothing but a test would ever turn it.
  - **Zero means default**, resolved at the point of use in `pane.go:314`, not
    in the constructor. This is forced: white-box fixtures build `&Server{}`
    and `&Pane{}` literals directly (`status_test.go:66-79`,
    `transport_close_test.go:35,75`, `pane_wedge_test.go:100-108`) and would
    otherwise silently get a 0s grace.
  - **Rejected:** a public `SetCloseGrace` following the `Server.SetLayout`
    precedent (`server.go:47-53`). `SetLayout` is a real product feature; this
    is not, and shipping a knob only tests use is worse than hiding it.
  - **Consequence accepted:** `cmd/wideboi`'s test is a different package and
    cannot reach `export_test.go`, so its one 2.05s test stays. That is free —
    after the other fixes `ptyx` (~5.8s) is the critical path and `cmd/wideboi`
    (2.28s) runs in parallel underneath it.

- **Decision: `ptyx` gets no production change — only its test literals.**
  - **Why:** the grace is already a parameter there. Changing `p.Kill(2s)` to a
    short grace at the three spawn tests (`ptyx/pane_test.go:22,46,71`) takes
    the package 11.5s → 5.82s, measured, with the reap-contract tests untouched.
  - `TestKillReapsSIGTERMIgnoringEscapee` already passes 500ms
    (`reap_test.go:66`) — a short grace in this file is established practice.

- **Decision: name the idle timeout and add one constructor variant.**
  `term/grid.go:271`'s bare `3*time.Second` becomes `DefaultIdleTimeout`, and
  `NewVT(cols, rows)` delegates to `NewVTWithIdleTimeout(cols, rows, idle)`.
  - **Why:** `NewVT` returns the `Grid` interface, so a setter would have to go
    on the interface and be implemented by the hand-rolled fakes
    (`statusGrid` at `status_test.go:23-60`, `newBlockingGrid()` in
    `pane_wedge_test.go`). A second constructor touches nothing else — only
    `osc_test.go:112` needs it.
  - **Rejected:** shortening the sleep alone. The 3s is real product behaviour;
    the test must wait out whatever the timeout is, so the timeout is what has
    to move.

- **Decision: `make quick` needs no package list.**
  - **Why:** once the Go suite is ~6s, the useful boundary is "Go tests" vs
    "race + pty suites", which is stable. An exclusion list
    (`go list ./... | grep -v …`) was measured at 0.36s but only earns its
    keep if `internal/server` stays slow, and it is one more thing to drift.

- **Decision: every shortened test must be proved to still fail for its own
  reason.**
  - **Why:** this is the specific way this change can go wrong — a test whose
    child would have been reaped during grace now gets SIGKILLed instead, stays
    green, and asserts something weaker. Note that at a 2s grace these tests are
    *already* in `Kill`'s `!rootExited` branch (interactive `/bin/sh` ignores
    SIGTERM), so shortening does not change which branch runs — but that is an
    argument for expecting it to work, not evidence that it did.

## Patterns to follow

- Zero-value-means-default with resolution at the use site, mirroring the
  reasoning in `Server.SetLayout`'s doc comment (`server.go:44-49`): don't widen
  constructors, let existing call sites stay put.
- `ptyx/reap_test.go:66`'s existing 500ms grace as precedent for a short grace
  in a teardown call.
- Plan 19's discipline: prove the new/changed test fails before trusting it
  green (`docs/dev-sessions/2026-09-21-wideboi-plan-19-faster-checks/plan.md`).

## What we're NOT doing

- **Touching `make verify-exit` / `scripts/ptycheck.py`.** They assert the
  end-to-end teardown contract for the real binary (`ptycheck.py:1-30`) and
  their waits are the thing under test.
- **Shortening the three reap-contract tests.**
  `TestKillReapsEscapedGrandchild`, `TestKillReapsSIGTERMIgnoringEscapee`, and
  `TestKillEscalatesEvenWhenRootExitsWithinGrace` keep their graces. The last
  one's subject *requires* a grace longer than the root's own exit.
- **The other nine hardcoded durations** inventoried in `research.md` §6 —
  `killWait`, `aliveWait`, the 33ms/1000ms server ticks, the 16ms draw ticks.
  None are what these tests pay; the measurement says so.
- **`make -j` across check's targets.** smoke, attach-check and verify-exit all
  scan the process table and bind fixed paths.
- **Fixing #52** (idle render trickle), **pytest/xdist**, or converting any
  Python suite to Go.
- **A shared `newTestServer` fixture** for the white-box literals, tempting as
  it is while editing those files.

## Open questions

- **Does `TestKillIsIdempotent` (`reap_test.go:113`, 2.03s) get a short grace?**
  Its subject is idempotence, not the grace duration. **Default: yes, shorten
  it**, and prove it still fails if the second `Kill` is made non-idempotent.
  Worth ~1.9s. If proving that turns out awkward, leave it — it is not on the
  critical path once ptyx is ~5.8s.
- **Exact short-grace value for tests.** **Default: 100ms**, the value the
  experiments used. If CI proves it tight on a loaded runner, raise to 250ms —
  the same tuning lesson as Plan 19's `quiet` window, and cheap either way since
  a child that dies promptly returns early regardless.
