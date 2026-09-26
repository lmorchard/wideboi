# Issues 265 & 266 Implementation Plan

**Goal:** Implement protocol version / schema drift guards and enforce mutex / lock discipline across client and server.

**Approach:**
1. Establish a golden test for the deterministic wirepb file descriptor marshal hash, create `web/src/version.ts`, update web tests/clients to import it, and assert Go-to-TS sync.
2. Refactor client `SendVerb`, `SendSplit`, `HandleServerMsg`, and search methods to release `c.mu` before `transport.SendClient`; add `TestBlockedTransportDoesNotFreezeDraw`.
3. Separate server resize into `prepareResizePanesLocked` and `applyResizeJobs` executed after `s.mu.Unlock()`.
4. Add `p.SendBytes` queued through `p.input` for `MsgInput`, run `MsgSendInputRequest` writes outside `s.mu`, and test key/mouse/byte queue ordering.
5. Fix server double-reader race by tracking started transport loops.
6. Run full verification (`make quick`, `make race`, `smoke`, timing tests 4x).

---

## Phase 1: Issue 265 — Protocol Version & Schema Guard

**Files:**
- Create: `web/src/version.ts`
- Create: `internal/protocol/version_guard_test.go`
- Modify: `web/src/client.ts`
- Modify: `web/src/client.test.ts`
- Modify: `web/src/lifecycle.test.ts`
- Modify: `web/tests/browser-fixture.ts`
- Modify: `web/tests/*.spec.js` (8 test files)

**Verification:**
- [x] `go test -count=1 ./internal/protocol` passes — verified
- [x] `cd web && npm test` passes — verified
- [x] Verify failure on intentional mismatch (tampering golden or version string) — verified failure modes

---

## Phase 2: Issue 266 — Client Send Lock Discipline

**Files:**
- Modify: `internal/client/client.go` (`SendVerb`, `SendSplit`, `HandleServerMsg`)
- Modify: `internal/client/search.go` (`SearchCommit`, `SearchNavigate`, `SearchEnd`, `applyHistoryLocked`)
- Test: `internal/client/blocked_send_test.go`

**Verification:**
- [x] `go test -count=1 ./internal/client` passes — verified
- [x] `TestBlockedTransportDoesNotFreezeDraw` passes — verified
- [x] `go test -race -count=1 ./internal/client` passes — verified

---

## Phase 3: Issue 266 — Server Resize Critical Section & Input Queueing

**Files:**
- Modify: `internal/server/pane.go` (add `RawBytes`, `SendBytes`)
- Modify: `internal/server/server.go` (`prepareResizePanesLocked`, `applyResizeJobs`, `handleClientMsg`, `onPaneExit`)
- Modify: `internal/server/input_queue_test.go` (test key, mouse, and byte ordering)
- Modify: `internal/server/paneupdate_test.go` (adjust callers of resizePanes)

**Verification:**
- [x] `go test -count=1 ./internal/server` passes — verified
- [x] `go test -race -count=1 ./internal/server` passes — verified

---

## Phase 4: Issue 266 — Server Double-Reader Race Fix

**Files:**
- Modify: `internal/server/server.go` (`startTransportLoopLocked`, `admitSocketConn`, `ListenWebSocket`, `Run`, `dropClientLocked`)
- Test: `internal/server/double_reader_test.go`

**Verification:**
- [x] `go test -count=1 ./internal/server` passes — verified
- [x] `go test -race -count=1 ./internal/server` passes — verified

---

## Phase 5: Overall Verification & 4x Repeat Runs

**Commands:**
- [x] `make quick` passes — verified
- [x] `make race` passes — verified
- [x] `make check` passes — verified (all 10 targets clean)
- [x] Timing sensitive tests run 4x cleanly per CLAUDE.md — verified (4 clean runs)
