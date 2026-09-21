# Plan 20 — Faster Go tests Implementation Plan

**Goal:** Take `go test ./...` from 16.2s to under 6s and add a `make quick`
tier, without weakening any teardown assertion.

**Approach:** Three independent causes, one phase each, biggest first. A
`closeGrace` field on `Server`/`Pane` exposed only through `export_test.go`;
shortened teardown literals in `ptyx` (which needs no production change); and a
named, injectable idle timeout in `term`. Then the Makefile tier.

**Tech stack:** Go, `make`.

**Commit per phase:** `Phase N: <name>`.

**Baseline, measured before any change** (`go test -count=1`):
`internal/server` 15.4s, `ptyx` 11.5s, `term` 3.57s, `cmd/wideboi` 2.28s,
all 16.2s wall; `make check` 90.8s.

**Short grace for tests: 100ms** everywhere (spec's default).

---

## Phase 1: A closeGrace field, visible only to tests

Takes `internal/server` from 15.4s to ~2.7s. Seven tests in `server_test.go`
each pay a 2s `CloseGrace` at teardown while asserting nothing about it.

**Files:**
- Modify: `internal/server/pane.go` — add field, resolve at use.
- Modify: `internal/server/server.go` — add field, assign in `spawnPaneLocked`.
- Create: `internal/server/export_test.go` — test-only setter.
- Create: `internal/server/grace_test.go` — the structural invariant test.
- Modify: `internal/server/server_test.go` — 7 sites opt into the short grace.

**Test first.** `grace_test.go` (package `server`, white-box) pins the
zero-value contract before the field exists:

```go
// A Pane built as a struct literal -- which every white-box fixture in
// this package does (status_test.go, transport_close_test.go,
// pane_wedge_test.go) -- has a zero closeGrace. Zero must mean the
// production default, or those fixtures would silently start tearing
// down with no grace at all and stop exercising the real path.
func TestZeroCloseGraceResolvesToTheDefault(t *testing.T) {
	if got := (&Pane{}).graceOrDefault(); got != CloseGrace {
		t.Errorf("(&Pane{}).graceOrDefault() = %v, want %v", got, CloseGrace)
	}
	if got := (&Pane{closeGrace: 50 * time.Millisecond}).graceOrDefault(); got != 50*time.Millisecond {
		t.Errorf("explicit grace = %v, want 50ms", got)
	}
}
```

**Key changes:**

`pane.go` — `CloseGrace` stays exactly as it is (still the default, still
exported), plus:

```go
// closeGrace overrides CloseGrace for this pane. Zero means the
// default; see graceOrDefault.
closeGrace time.Duration

// graceOrDefault resolves the grace at the point of use rather than in
// a constructor, because the white-box fixtures in this package build
// &Pane{} literals directly and would otherwise get 0s.
func (p *Pane) graceOrDefault() time.Duration {
	if p.closeGrace <= 0 {
		return CloseGrace
	}
	return p.closeGrace
}
```

and `pane.go:314` becomes `killErr := p.pty.Kill(p.graceOrDefault())`.

`server.go` — a `closeGrace time.Duration` field, and in `spawnPaneLocked`
after `NewPane` succeeds: `p.closeGrace = s.closeGrace`. **`NewPane`'s
signature does not change** — following `SetLayout`'s stated reasoning
(`server.go:44-49`) that existing call sites stay put.

`export_test.go` — compiled only into the test binary, so this adds no
production API:

```go
package server

import "time"

// SetCloseGrace shortens the SIGTERM grace for panes this server spawns.
// Test-only, and deliberately in export_test.go: nothing but a test would
// ever turn this knob, so it should not be product API.
func (s *Server) SetCloseGrace(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeGrace = d
}
```

`server_test.go` — each of the 7 `server.NewServer(...)` sites (lines
32, 56, 92, 140, 193, 254, 328) gains `srv.SetCloseGrace(testGrace)` on
the next line, with `const testGrace = 100 * time.Millisecond` declared
once in that file.

**Verification — automated:**
- [x] `go test -count=1 -run TestZeroCloseGraceResolvesToTheDefault ./internal/server/` fails before `graceOrDefault` exists (compile error is the expected first failure), passes after — **failed with `(&Pane{}).graceOrDefault undefined` + `unknown field closeGrace`, then PASS**
- [x] `go test -count=1 ./internal/server/` passes and is **under 4s** (from 15.4s) — **2.69s**
- [x] `make test` passes — **all 11 packages ok, 12.01s wall (was 16.2s); ptyx is now the critical path at 11.6s**
- [x] `make lint` and `make fmt-check` pass — **both clean**
- [x] `grep -rn 'SetCloseGrace' --include='*.go' .` shows hits only in `export_test.go` and `server_test.go` — no production caller — **1 definition, 7 test callers, plus one mention in a `server.go` comment (not a call)**

**Verification — manual:**
- [x] Confirm `export_test.go` is genuinely test-only: `go build ./...` succeeds and `go doc internal/server Server` does not list `SetCloseGrace`. — **`go build ./...` OK; `go doc` lists `SetLayout` and no `SetCloseGrace`**

---

## Phase 2: Shorten ptyx's teardown literals, and prove they still bite

Takes `ptyx` from 11.5s to ~3.9s. **No production change** — `ptyx.Pane.Kill`
(`reap.go:135`) already takes the grace as a parameter; three spawn tests and
the idempotence test simply pass `2 * time.Second` to tear down.

TDD is opted out here: these are existing tests and no behaviour changes. The
mutation proof below replaces it, and is the more important check.

**Files:**
- Modify: `internal/server/ptyx/pane_test.go` — 3 sites (lines 22, 46, 71).
- Modify: `internal/server/ptyx/reap_test.go` — `TestKillIsIdempotent` only
  (lines 118, 121); declare `const testGrace = 100 * time.Millisecond`.

**Key changes:** `p.Kill(2 * time.Second)` → `p.Kill(testGrace)` at exactly
those four call sites, with a comment at the constant:

```go
// testGrace is for teardown that is not itself the assertion. The
// reap-contract tests below keep their own graces: the escapee tests need
// a real window, and TestKillEscalatesEvenWhenRootExitsWithinGrace needs
// one longer than the root's own exit.
const testGrace = 100 * time.Millisecond
```

**Left at full price, deliberately:** `TestKillReapsEscapedGrandchild`
(`reap_test.go:32`, 2.24s), `TestKillReapsSIGTERMIgnoringEscapee`
(`reap_test.go:66`, already 500ms), `TestKillEscalatesEvenWhenRootExitsWithinGrace`
(`reap_test.go:103`, 2s but costs 0.12s since the root exits early).

**The mutation proof — this is the phase's real verification.** A shortened
teardown could stop noticing a leak and stay green. The four shortened tests
assert teardown only through `if err := p.Kill(...); err != nil`, and `Kill`
returns an error exactly when the tree survives (`reap.go:166-168`). So:
temporarily neuter the SIGKILL escalation and confirm they go red.

The mutation must target the path these tests actually depend on. They spawn
a bare `/bin/sh` with no descendants, so neutering `signalDescendants` would
change nothing and "prove" nothing. `/bin/sh` ignores SIGTERM, so what carries
them is the gated SIGKILL to the root (`reap.go:160-163`):

```go
// TEMPORARY, reverted before commit -- in signalRoot (reap.go:180):
func (p *Pane) signalRoot(sig syscall.Signal) {
	if sig == syscall.SIGKILL { return } // mutation
	if p.PGID > 0 { _ = syscall.Kill(-p.PGID, sig) }
	_ = syscall.Kill(p.Cmd.Process.Pid, sig)
}
```

With it applied, an interactive `/bin/sh` survives SIGTERM, never receives
SIGKILL, `rootExited` stays false past `killWait`, and `Kill` returns the
"survived SIGKILL" error (`reap.go:166-168`) that these tests check.

**Verification — automated:**
- [!] With the mutation applied, `go test -count=1 ./internal/server/ptyx/` **fails**, and the failures include the shortened tests — **DOES NOT HOLD, and the plan was wrong about why.** Three mutations were tried and the four shortened tests passed under every one:

      | mutation | result |
      | --- | --- |
      | `signalRoot` drops SIGKILL | **all 7 pass** |
      | `signalDescendants` drops SIGKILL | `TestKillReapsSIGTERMIgnoringEscapee` + `TestKillEscalatesEvenWhenRootExitsWithinGrace` fail; shortened tests pass |
      | **all** signalling neutered | only `TestKillEscalatesEvenWhenRootExitsWithinGrace` fails; shortened tests pass (stable over 3 runs) |

      Cause: `Kill` calls `p.Master.Close()` (`reap.go:149`) before the SIGKILL pass, and closing the pty master reaps a plain `/bin/sh` on its own. **The shortened tests therefore had no reaping teeth at 2s either** — shortening removed nothing that was there, which is the thing this checkbox existed to establish, just not the way it expected. Not a failure of the work. See notes.md and the filed issue.
- [x] Mutation reverted (`git diff internal/server/ptyx/reap.go` is empty), full suite green — **diff empty; `make test` all 11 packages ok**
- [x] `go test -count=1 ./internal/server/ptyx/` passes and is **under 5s** (from 11.5s; 5.82s was measured with only the three spawn tests shortened, so adding the idempotence test should reach ~3.9s) — **3.88s**
- [x] The three reap-contract tests still report their old timings in `-v` output (≈2.24s / 0.74s / 0.12s), confirming they were not shortened — **2.24s / 0.73s / 0.13s**
- [x] `make test` passes — **all 11 packages ok; wall 12.0s → 4.0s**

**Verification — manual:**
- [x] Re-read the diff: only the four intended `Kill` call sites changed, and no assertion was touched. — **confirmed; `reap_test.go` keeps 2s/500ms/2s at lines 32/66/103**

---

## Phase 3: Name the idle timeout and make it injectable

Takes `term` from 3.57s to ~0.5s. `term/grid.go:271` has a bare
`3*time.Second`, and `osc_test.go:112` sleeps 3100ms to wait it out.

**Files:**
- Modify: `internal/server/term/grid.go` — named constant, field, constructor
  variant, resolve at use.
- Modify: `internal/server/term/osc_test.go` — one test uses the variant.

**Key changes:**

```go
// DefaultIdleTimeout is how long a pane may go without output before the
// status fallback calls it idle. Consulted only when no OSC 133 has been
// seen -- a shell that reports its own status is authoritative.
const DefaultIdleTimeout = 3 * time.Second

// NewVT returns a Grid backed by charmbracelet/x/vt.
func NewVT(cols, rows int) Grid {
	return NewVTWithIdleTimeout(cols, rows, DefaultIdleTimeout)
}

// NewVTWithIdleTimeout is NewVT with the status fallback's idle window
// overridden. A second constructor rather than a setter because NewVT
// returns the Grid interface, and a setter would have to go on that
// interface and be implemented by every hand-rolled fake (statusGrid in
// status_test.go, newBlockingGrid in pane_wedge_test.go).
func NewVTWithIdleTimeout(cols, rows int, idle time.Duration) Grid {
	g := &vtGrid{em: vt.NewSafeEmulator(cols, rows), idleTimeout: idle}
	// ... rest of the existing NewVT body unchanged ...
}
```

`vtGrid` gains `idleTimeout time.Duration`, and `Status()` (`grid.go:271`)
resolves zero to the default for the same reason Phase 1 does:

```go
idle := g.idleTimeout
if idle <= 0 {
	idle = DefaultIdleTimeout
}
if t := g.lastWriteTime.Load(); t != nil && time.Since(*t) > idle {
```

`osc_test.go` — `TestMalformedOSC133LeavesTheIdleFallbackArmed` builds with
`term.NewVTWithIdleTimeout(20, 5, 100*time.Millisecond)` and sleeps 150ms
instead of 3100ms. Its doc comment's "Costs just over 3s" line is updated.

**The mutation proof.** The test exists to prove an unrecognised OSC 133
payload does not latch `sawOSC133`. Shortening the window must not cost it
that. Temporarily make the handler latch on unknown payloads — in `grid.go`'s
OSC 133 handler, change the `default:` branch to
`g.sawOSC133.Store(true); return false` — and confirm the test fails.

**Verification — automated:**
- [x] With the mutation applied, `go test -count=1 -run TestMalformedOSC133 ./internal/server/term/ -v` **fails** — **FAIL with its own message: `Status() = 1 after the idle window, want 0 -- an unrecognised 133 payload latched sawOSC133`.** Unlike Phase 2's, this test has real teeth at the shortened window.
- [x] Mutation reverted (`git diff internal/server/term/grid.go` shows only the intended changes), test passes — **PASS; diff is the const, the field, the constructor split and the Status resolution**
- [x] `go test -count=1 ./internal/server/term/` passes and is **under 1s** (from 3.57s) — **0.63s**
- [x] `grep -n '3 \* time.Second\|3\*time.Second' internal/server/term/grid.go` shows only the `DefaultIdleTimeout` declaration — **one hit, line 189**
- [x] `make test` passes — **all 11 packages ok; cold `go test -count=1 ./...` now 4.03s, from 16.2s**

**Verification — manual:**
- [x] `NewVT`'s existing callers (`pane.go:65` and ~20 test sites) are unchanged — the delegation preserves the two-arg signature. — **21 two-arg sites untouched; `NewVTWithIdleTimeout` has exactly one caller, the test**

---

## Phase 4: The make quick tier, and record the numbers

**Files:**
- Modify: `Makefile` — add `quick`, add it to `.PHONY`, extend the `check`
  comment to say what the two tiers are for.
- Modify: `docs/dev-sessions/.../notes.md` — final numbers.

**Key changes:**

```make
# quick is the inner-loop tier: everything that does not spawn a real
# binary in a real pty. It is the Go suite plus the three static checks,
# and it is what to run on save.
#
# No package list: once the Go tests stopped waiting out fixed grace
# periods the whole suite is a few seconds, so the useful boundary is
# "Go tests" vs "race + the pty suites" -- which is stable, where a
# hand-maintained list of fast packages would drift.
quick: fmt-check lint seam-check test
```

`check`'s existing comment gains a line pointing at `quick` for the edit loop.

**Verification — automated:**
- [x] `make quick` passes, and is **under 8s** from a cold `go clean -testcache` — **4.53s**
- [x] `make check` passes, and is recorded against the 90.8s baseline — **78.59s**
- [x] `env -u TERM make check` passes — the Plan 19 lesson that CI runners set no `TERM` — **78.17s, green**
- [x] `make check` run a second time, to catch anything that passes only once — **78.05s, green; three runs total at 78.59/78.05/78.17s**
- [x] `go test -count=1 ./...` recorded against the 16.2s baseline — **4.03s**

**Verification — manual:**
- [x] `make quick` is in `.PHONY`. — **added**
- [x] Confirm `check` still runs every target it ran before — `quick` adds a tier, it does not remove anything from the gate. — **`check`'s prerequisite list is byte-identical; only a comment was added above it**

---

## Spec coverage check

| Spec requirement | Phase |
| --- | --- |
| `go test ./...` under 6s | 1, 2, 3 |
| `closeGrace` field, `export_test.go`, zero-means-default | 1 |
| `ptyx` test literals only, no production change | 2 |
| Named + injectable idle timeout | 3 |
| Reap-contract tests keep their graces | 2 (explicit list) |
| `make quick`, no package list | 4 |
| `make check` unchanged in coverage | 4 |
| Every shortened test proved to still fail | 1 (test-first), 2, 3 (mutations) |
| `TestKillIsIdempotent` shortened (open question, default yes) | 2 |
| 100ms short grace (open question, default) | 1, 2 |
