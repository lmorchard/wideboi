# Plan: Detect Agent Permission Prompts and Blockers (Issue #376)

**Goal:** Detect when an interactive AI coding agent or CLI prompt is waiting for human confirmation/permission and promote the pane's status to `StatusNeedsInput` (`!`), while also keeping status in `StatusWorking` when title spinners or progress indicators are active.

**Approach:** Implement a screen-tail and title heuristic scanner in `internal/server/term/heuristic.go`. Integrate it into `vtGrid.Status()` with an output-generation (`outputGen`) cache to eliminate CPU overhead on the 33ms server broadcast ticker.

**Tech stack:** Go, standard regex/strings, ultraviolet VT terminal emulator.

---

## Phase 1: Core Heuristic Scanner & Tests

Implement pure pattern matching logic for terminal titles and screen tails in `internal/server/term/heuristic.go` with unit tests.

**Files:**
- Create: `internal/server/term/heuristic.go`
- Test: `internal/server/term/heuristic_test.go`

**Key changes:**
- `isWorkingTitle(title string) bool`: Detects Braille spinners (`\u2800-\u28FF`) and arc quadrant spinners (`\u25D0-\u25D3`).
- `isBlockerTitle(title string) bool`: Detects `"Action Required"`.
- `scanScreenTail(lines []string) (protocol.PaneStatus, bool)`:
  - Scans recent lines (bottom 12 non-empty lines).
  - Returns `(StatusNeedsInput, true)` for agent blockers and `[y/N]` prompts.
  - Returns `(StatusWorking, true)` for interrupt hints (`esc to interrupt`) or progress bars (`(■|⬝){4,}`).
  - Returns `(StatusIdle, false)` if no patterns match.

**Verification — automated:**
- [x] `go test -v ./internal/server/term -run TestHeuristic` passes — **verified TestIsWorkingTitle, TestIsBlockerTitle, TestScanScreenTail**

---

## Phase 2: Integrate into `vtGrid.Status()` with Generation Caching

Integrate the scanner into `vtGrid.Status()` in `internal/server/term/grid.go`.

**Files:**
- Modify: `internal/server/term/grid.go`
- Test: `internal/server/term/osc_test.go`
- Test: `internal/server/term/heuristic_test.go`

**Key changes:**
- Add cache fields to `vtGrid`:
  ```go
  heuristicMu            sync.Mutex
  cachedHeuristicStatus  protocol.PaneStatus
  lastHeuristicGen       uint64
  lastHeuristicTitle     string
  ```
- Update `vtGrid.Status()`:
  - If `sawAuthoritativeStatus`: return stored status directly.
  - If writes are active (`time.Since(*lastWriteTime) < heuristicDebounceWindow` (~500ms)): return `StatusWorking`.
  - When writes pause (>= 500ms):
    - Fast path: If `g.outputGen.Load() == lastHeuristicGen` and current title matches `lastHeuristicTitle`, return `cachedHeuristicStatus`.
    - Slow path: Lock `heuristicMu`, read title and dump bottom 12 screen lines, evaluate via `heuristic.go`, update cache and `lastHeuristicGen`.
    - If no pattern matches, return `StatusWorking` if `time.Since < idleTimeout` (3s), else `StatusIdle`.
- On new `Write()`, status becomes `StatusWorking` as before.

**Verification — automated:**
- [x] `go test -v ./internal/server/term -run TestGridStatus` passes — **verified TestGridStatusHeuristicBlockerPrompts, TestGridStatusHeuristicTitleSpinner, TestGridStatusHeuristicGenericConfirm, TestGridStatusHeuristicPlainDecaysToIdle**
- [x] `go test -v ./internal/server/term -run TestOsc` passes — **all OSC tests pass**
- [x] `go test -v ./internal/server/...` passes — **all tests in internal/server, ptyx, term pass**

---

## Phase 3: Project Verification & End-to-End Checks

Verify all project standards and ensure no regressions.

**Verification — automated:**
- [x] `go test ./internal/... ./cmd/...` passes — **all Go unit test suites passed**
- [x] `make quick` (fmt-check, lint, seam-check, test, web-test) passes cleanly — **182 web tests passed, seam-check OK, go vet OK**
