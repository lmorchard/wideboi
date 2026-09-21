# Plan 20 — research

Codebase findings (documentarian subagent) plus four measurements taken
directly. The measurements matter most: they overturned part of the framing
in #54.

## Measured, not read

Baseline `go test -count=1 ./...` is **16.2s at 23% CPU** — sleeping, not
computing. Wall time is dominated by `internal/server`.

**Experiment 1 — set `CloseGrace` to 100ms, run `./internal/server/...`:**

| package | before | with 100ms grace |
| --- | --- | --- |
| `internal/server` | 15.4s | **2.74s** |
| `internal/server/ptyx` | 11.5s | **11.5s — unchanged** |
| `internal/server/term` | 3.57s | 3.57s — unchanged |

**Experiment 2 — change `p.Kill(2s)` to `p.Kill(100ms)` in `pane_test.go`
only (3 sites), leaving `reap_test.go` untouched:** ptyx **11.5s → 5.82s**.
The three spawn tests each went 2.05s → 0.14s; the reap-contract tests kept
their timings exactly (`TestKillReapsEscapedGrandchild` 2.24s,
`TestKillIsIdempotent` 2.03s).

**Experiment 3 — Go's test cache.** Unchanged tree: **0.12s**. Caching is
content-based on the compiled test binary, so a comment-only edit still hits
cache, and an *unused* added function does too (it gets stripped). A real
change to `internal/server` costs **15.7s** (`internal/server` + the
`cmd/wideboi` dependent).

**Experiment 4 — the 8 non-server packages, cold from `go clean -testcache`:
0.36s together.**

**Conclusion: three independent causes, not one.** #54 framed this as a single
injectability problem. It is not.

## 1. CloseGrace has exactly one call site

- `const CloseGrace = 2 * time.Second` — `internal/server/pane.go:20`
- Sole use: `internal/server/pane.go:314`, in `(*Pane).Close()`:
  `killErr := p.pty.Kill(CloseGrace)`
- `ptyx.Pane.Kill(grace time.Duration)` — `internal/server/ptyx/reap.go:135`.
  **The grace is already a parameter at the ptyx layer.** Only the
  `internal/server` layer hardcodes it.

Why the grace is actually spent (reading `Kill`, `reap.go:135-168`):
`waitForExit(grace)` returns *early* on `<-p.done` (`reap.go:193-200`), and
`anyAlive` returns false immediately when nothing is alive
(`reap.go:205-225`). So `Kill` is fast for a child that dies on SIGTERM and
costs ~`grace` for one that ignores it. Every affected test spawns an
interactive `/bin/sh`, which ignores SIGTERM.

Note this means `rootExited` is **already false** in those tests at a 2s
grace — shortening it does not move them into a different branch of `Kill`.

## 2. Constructors and call chain

- `server.NewPane(id int, argv []string, cols, rows int, dir string)` —
  `pane.go:57`; calls `ptyx.Spawn` (`pane.go:58`) and `term.NewVT(cols, rows)`
  (`pane.go:65`).
- Only production caller: `spawnPaneLocked()` — `server.go:285`.
- `server.NewServer(tp, shell, cwd string) *Server` — `server.go:60`; called
  at `cmd/wideboi/main.go:127` and `main.go:305`.
- `Server.Close()` — `server.go:711`, fans `p.Close()` over a goroutine per
  pane (`server.go:733-743`).
- **No options struct anywhere.** Every constructor takes positional params.

## 3. The established convention for a tunable: a post-construction setter

`Server.SetLayout(mode)` — `server.go:47-53`, called at `main.go:128,306`.
Its doc comment states the reasoning outright (`server.go:44-49`):

> "A method rather than a NewServer parameter: the zero value is already the
> scrolling strip, so only a caller that wants cards has to say so, and the
> eight existing NewServer call sites stay put."

This is the precedent to follow. No functional-options or `Options{}` pattern
exists in the repo (the only `Options` hit is stdlib `slog.HandlerOptions` at
`internal/logger/logger.go:23`).

**Constraint this creates:** white-box tests build `&Server{...}` and
`&Pane{...}` struct literals directly, bypassing the constructors —
`status_test.go:66-79`, `transport_close_test.go:35,75`,
`pane_wedge_test.go:100-108`. A new duration field is therefore zero in those
fixtures, so **zero must mean "use the default"**, resolved at the point of
use rather than in the constructor.

## 4. The idle timeout is a bare literal

`internal/server/term/grid.go:271`, inside `(*vtGrid).Status()`:

```go
if t := g.lastWriteTime.Load(); t != nil && time.Since(*t) > 3*time.Second {
```

Not a named constant, not a field. `term.NewVT(cols, rows int) Grid` —
`grid.go:181`, two params, no timing knob. `vtGrid` (`grid.go:139-178`) has no
duration field. Only production caller of `NewVT` is `pane.go:65`; ~20 test
call sites across `osc_test.go`, `grid_test.go`, `reflow_test.go`.

The one test that pays for it: `osc_test.go:112`,
`time.Sleep(3100 * time.Millisecond)`.

## 5. Which tests own the teardown contract

These assert grace/escalation/reaping *as the subject* and must keep paying:

- `TestKillReapsEscapedGrandchild` — `reap_test.go:14`, assertion at
  `reap_test.go:36-38` ("escapee survived Kill — the pane leaked a process")
- `TestKillReapsSIGTERMIgnoringEscapee` — `reap_test.go:47`, **already uses a
  500ms grace** (`reap_test.go:66`); assertion at `reap_test.go:70-72`
- `TestKillEscalatesEvenWhenRootExitsWithinGrace` — `reap_test.go:90`,
  assertion at `reap_test.go:107-109`. Its subject *requires* a grace longer
  than the root's own exit, so its 2s must stay. Costs only 0.12s because the
  root exits early.
- `scripts/ptycheck.py` — the end-to-end contract for the built binary; three
  assertions documented at `ptycheck.py:1-30`, plus a deliberately planted
  escapee (`ptycheck.py:24-30`). Run by `make verify-exit`.

**No test in `internal/server` asserts anything about grace, SIGTERM, SIGKILL
or reap deadlines.** Its 15.4s is entirely incidental teardown.

`TestKillIsIdempotent` (`reap_test.go:113`) asserts idempotence, not the grace
duration.

`TestCloseDoesNotHangOnWedgedResize` (`pane_wedge_test.go:93`) is a
liveness guard with bounded waits (2s/5s/2s at lines 117,133,142); it never
inspects signal delivery. Costs 0.03s.

## 6. Other hardcoded durations (class-of-bug sweep)

| file:line | value | consulted by |
| --- | --- | --- |
| `server/pane.go:20` | `CloseGrace` 2s | `pane.go:314` |
| `ptyx/reap.go:19` | `killWait` 1s | post-SIGKILL wait, `reap.go:163` |
| `ptyx/reap.go:22` | `aliveWait` 500ms | `anyAlive`, `reap.go:166` |
| `ptyx/reap.go:30` | `KillResidual` = 1.5s | `pane.go:21` → `main.go:351` |
| `ptyx/reap.go:221` | 10ms | `anyAlive` poll cadence |
| `term/grid.go:271` | 3s | idle fallback in `Status()` |
| `server/server.go:154` | 1000ms | `pollDescendants` tick |
| `server/server.go:157` | 33ms | frame/broadcast tick |
| `cmd/wideboi/main.go:25` | `signalExitMargin` 500ms | `main.go:351` |
| `cmd/wideboi/main.go:194,343` | 16ms | draw-loop frame ticks |

None are configurable. `killWait`/`aliveWait` are only reached *after* the
grace expires, and the measurements show they are not what these tests pay —
shortening `CloseGrace` alone took `internal/server` to 2.74s.

No hardcoded durations in `hostterm`, `transport`, `client`, `layout`,
`protocol`, `keys`, or `logger` production code.
