# Support OSC 9;4 Progress Implementation Plan

**Goal:** Support the ConEmu / Windows Terminal progress reporting protocol (`OSC 9;4`)
in `internal/server/term/grid.go` to drive `term.PaneStatus` and latch authoritative status mode.

**Approach:** Generalize `sawOSC133` into `sawAuthoritativeStatus`, register an OSC 9 handler with
`x/vt` that parses `9;4;<state>` payloads into `PaneStatus` (`StatusWorking`, `StatusDone`,
`StatusFailed`, `StatusNeedsInput`), latch on recognized sequences, and verify via comprehensive unit tests.

**Tech stack:** Go, `github.com/charmbracelet/x/vt`.

---

## Phase 1: Generalize Authoritative Status Latch and Add Failing OSC 9;4 Tests

Refactor `sawOSC133` to `sawAuthoritativeStatus` across `internal/server/term/grid.go`, and
write comprehensive failing tests in `internal/server/term/osc_test.go` covering OSC 9;4
driving `PaneStatus` and latching.

**Files:**
- Modify: `internal/server/term/grid.go` — rename `sawOSC133` to `sawAuthoritativeStatus`.
- Modify: `internal/server/term/osc_test.go` — add `TestOSC9ProgressDrivesPaneStatus`, `TestMalformedOSC9LeavesTheIdleFallbackArmed`, and `TestOSC9And133Interleaving`.

**Key changes:**
In `internal/server/term/grid.go`:
```go
type vtGrid struct {
	...
	sawAuthoritativeStatus atomic.Bool
	...
}
```

In `internal/server/term/osc_test.go`:
```go
func TestOSC9ProgressDrivesPaneStatus(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    term.PaneStatus
	}{
		{"turn end (clear)", "\x1b]9;4;0;\x07", term.StatusDone},
		{"turn end (clear without trailing semicolon)", "\x1b]9;4;0\x07", term.StatusDone},
		{"progress percent", "\x1b]9;4;1;50\x07", term.StatusWorking},
		{"turn error", "\x1b]9;4;2;0\x07", term.StatusFailed},
		{"turn error without progress val", "\x1b]9;4;2\x07", term.StatusFailed},
		{"turn start (busy/indeterminate)", "\x1b]9;4;3;\x07", term.StatusWorking},
		{"warning / paused", "\x1b]9;4;4;0\x07", term.StatusNeedsInput},
	}
	...
}
```

**Verification — automated:**
- [x] `go test -run TestOSC133 ./internal/server/term -v` passes (regression safety) — **PASS**
- [x] `go test -run TestOSC9 ./internal/server/term -v` **fails** (TDD proof) — **FAIL: Status() after "\x1b]9;4;0;\a" = 1, want 3**

**Verification — manual:**
- [x] Confirm `sawAuthoritativeStatus` accurately names the role of the latch.

---

## Phase 2: Implement OSC 9;4 Progress Handler

Register OSC command `9` on `x/vt.SafeEmulator` in `NewVTWithIdleTimeout`. Parse `9;4;<state>`
payloads, map them to `PaneStatus`, update `g.status`, and latch `g.sawAuthoritativeStatus`.

**Files:**
- Modify: `internal/server/term/grid.go` — register OSC 9 handler.

**Key changes:**
In `internal/server/term/grid.go`:
```go
	g.em.RegisterOscHandler(9, func(data []byte) bool {
		parts := strings.Split(string(data), ";")
		if len(parts) < 3 || parts[1] != "4" {
			return false
		}

		var st PaneStatus
		switch parts[2] {
		case "0":
			st = StatusDone
		case "1", "3":
			st = StatusWorking
		case "2":
			st = StatusFailed
		case "4":
			st = StatusNeedsInput
		default:
			return false
		}

		g.status.Store(int32(st))
		g.sawAuthoritativeStatus.Store(true)
		return true
	})
```

**Verification — automated:**
- [x] `go test ./internal/server/term/... -v` passes — **PASS (1.217s)**
- [x] `TestOSC9ProgressDrivesPaneStatus` passes — **PASS**
- [x] `TestMalformedOSC9LeavesTheIdleFallbackArmed` passes — **PASS**
- [x] `TestOSC9And133Interleaving` passes — **PASS**

**Verification — manual:**
- [x] Verify that unhandled OSC 9 sequences (e.g. `OSC 9;notification`) return `false` without latching. — **Verified by TestMalformedOSC9LeavesTheIdleFallbackArmed**

---

## Phase 3: Verification & Repetition Gate

Run the full project test suite and the repetition gate required for concurrency/timing changes.

**Files:**
- None (verification only).

**Verification — automated:**
- [x] `make quick` passes (vet, seam-check, go test) — **PASS**
- [x] `make check` passes (smoke, exit contract, pty attach, race detection) — **PASS**
- [x] Concurrency repetition gate: run `make check` 4 times in succession with zero failures. — **PASS (4/4 clean runs)**

**Verification — manual:**
- [x] Inspect git diff to ensure client/server seam is maintained and no unintended files are touched. — **Verified (clean)**
