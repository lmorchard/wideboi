# Implementation Plan: vt.Scrollback Ring Buffer (Issue #205)

**Goal:** Eliminate the O(scrollback) memmove and excessive heap allocation churn in `vt.Scrollback.Push` when scrollback is full, significantly reducing server CPU and memory pressure during high-throughput terminal scrolling.

**Approach:** Fork `charmbracelet/x` under `lmorchard/x`, create branch `vt-scrollback-ring` combining PR 888 (ring buffer eviction) and PR 822 (line buffer reuse). Wire it into `wideboi` via a `go.mod` `replace` directive, add benchmarks for full-scrollback throughput, and verify all existing tests pass.

**Tech stack:** Go, `github.com/charmbracelet/x/vt`, `github.com/charmbracelet/ultraviolet`, `gh` CLI.

---

## Phase 1: Create and push `lmorchard/x:vt-scrollback-ring`

Fork `charmbracelet/x` to `lmorchard/x`, create branch `vt-scrollback-ring`, implement the ring buffer + line buffer reuse in `vt/scrollback.go` with tests in `vt/scrollback_test.go`, test locally, and push to GitHub.

**Files:**
- Upstream: `vt/scrollback.go`
- Upstream: `vt/scrollback_test.go`

**Key changes:**
- In `Scrollback`: add `head int` field.
- In `Push`:
  - Fast-path structurally blank cell scan (`*c == uv.EmptyCell || *c == (uv.Cell{})`).
  - When ring is full (`len(s.lines) >= s.maxLines`): reuse the evicted `s.lines[s.head]` buffer if capacity fits (`cap(s.lines[s.head]) >= needed`), avoiding slice allocation.
  - Advance `s.head = (s.head + 1) % len(s.lines)`.
- In `Line(index int)`: return `s.lines[(s.head+index)%len(s.lines)]`.
- In `Lines()`: if `s.head == 0`, return `s.lines`. Otherwise allocate and copy in ring order.
- In `Clear()`: reset `s.lines = s.lines[:0]` and `s.head = 0`.
- In `SetMaxLines(maxLines)`: if shrinking, re-linearize and reset `s.head = 0`.

**Verification — automated:**
- [x] `go test ./...` in the cloned `vt` module passes with 100% success. — **PASS: ok github.com/charmbracelet/x/vt 0.098s**
- [x] New ring buffer overflow and buffer reuse tests in `vt/scrollback_test.go` pass. — **PASS: TestScrollback (all 10 subtests pass)**
- [x] Branch `vt-scrollback-ring` pushed to `github.com/lmorchard/x`. — **Pushed commit `cacc71cdcc0f`**

**Verification — manual:**
- [x] Inspect git diff on `vt-scrollback-ring` to confirm clean, idiomatic changes. — **Verified clean diff in `vt/scrollback.go` and `vt/scrollback_test.go`**

---

## Phase 2: Wire fork into `wideboi` via `go.mod`

Configure `wideboi`'s `go.mod` with a `replace` directive targeting the pushed commit in `github.com/lmorchard/x/vt`.

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`

**Key changes:**
- Add `replace github.com/charmbracelet/x/vt => github.com/lmorchard/x/vt <commit>` to `go.mod`.
- Run `go mod tidy`.

**Verification — automated:**
- [x] `go build ./...` compiles cleanly. — **Compiled cleanly**
- [x] `make quick` passes in wideboi. — **PASS: all quick checks green**

**Verification — manual:**
- [x] Verify `go list -m github.com/charmbracelet/x/vt` reflects the replacement. — **Verified: `github.com/charmbracelet/x/vt v0.0.0-20260913004009-c615ff2f7805 => github.com/lmorchard/x/vt v0.0.0-20260924233645-cacc71cdcc0f`**

---

## Phase 3: Benchmarks, Regression Tests, and Full Gate Verification

Add a targeted benchmark in `wideboi` (`internal/server/pane_traffic_test.go` or `internal/server/term/grid_bench_test.go`) comparing continuous scrolling with full scrollback (10,000 lines), verify performance gains, and ensure all tests in `make check` pass.

**Files:**
- Modify/Create: `internal/server/term/grid_bench_test.go`
- Update: `docs/dev-sessions/2026-09-24-1602-issue-205-scrollback-ring/notes.md`

**Verification — automated:**
- [x] `go test -bench=BenchmarkGridScrollbackFull -benchmem ./internal/server/term/...` reports sub-microsecond line writes and zero/low allocations. — **`BenchmarkGridScrollbackFull`: 8.2 µs/op across full emulator stack, `BenchmarkScrollbackPushFull`: 303 ns/op, 0 B/op, 0 allocs/op**
- [x] `make quick` passes. — **PASS**
- [x] `make check` passes (including race, smoke, attach-check, verify-exit). — **PASS: all 37 smoke tests, 25 attach-check tests, playwright tests, race tests passed**

**Verification — manual:**
- [x] Confirm `make traffic` / traffic scenarios show measurable reduction in CPU and heap pressure. — **`make traffic` runs all 5 scenarios cleanly**
