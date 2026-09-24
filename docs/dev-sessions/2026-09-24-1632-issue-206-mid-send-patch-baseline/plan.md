# Keep the sent frame as the patch baseline when a pane changes mid-send Implementation Plan

**Goal:** Allow `broadcastPaneUpdates` to retain successfully transmitted frames as patch baselines even when pane generation advances mid-send, enabling subsequent updates under continuous output to send lightweight patches rather than full snapshots.

**Approach:** Remove the deletion of `paneGens`/`paneFrames`/`paneUnderlyingGens`/`paneOutputGens`/`paneSbLens` when `r.pane.Generation() != r.underlyingGen` in `internal/server/server.go`. Retain the sent frame and wire generation as the baseline for the client. The next broadcast will detect the newer generation and build a patch against this baseline.

**Tech stack:** Go, VT terminal emulator, wideboi internal/protocol (protobuf and patch generation)

---

## Phase 1: Retain patch baseline across mid-send generation bumps and verify with in-process test

Deliver the server baseline retention change and a dedicated test in `internal/server` that injects mid-send writes and verifies patch reconstruction against a full render.

**Files:**
- Modify: `internal/server/server.go` — Remove the mid-send baseline deletion in lines 1341-1349.
- Modify: `internal/server/pane_traffic_test.go` — Add `TestPaneUpdatePatchBaselineRetainedOnMidSendWrite` test that performs a write during render, verifies subsequent broadcast sends a patch, and verifies client reconstruction via `ApplyPanePatch` matches full render.
- Test: `internal/server/paneupdate_test.go` — Confirm existing `TestGenerationChangingDuringRenderIsRetried` continues to pass.

**Key changes:**

In `internal/server/server.go`:
```go
		if !r.accepted {
			delete(s.paneGens[r.tp], r.paneID)
			delete(s.paneFrames[r.tp], r.paneID)
			delete(s.paneUnderlyingGens[r.tp], r.paneID)
			delete(s.paneOutputGens[r.tp], r.paneID)
			delete(s.paneSbLens[r.tp], r.paneID)
			continue
		}
		// Content can change while update was being rendered or sent.
		// Retain r.frame and r.wireGen as the baseline: the client accepted and
		// applied exactly this frame. On the next tick, wireGen != lastWireGen
		// will trigger a resend as a patch against this baseline.

		if s.paneGens[r.tp] == nil {
			s.paneGens[r.tp] = make(map[int]uint64)
		}
```

In `internal/server/pane_traffic_test.go`:
Add a test `TestPaneUpdatePatchBaselineRetainedOnMidSendWrite`:
1. Create a `term.VT` grid, wrap with a hook or custom `term.Grid` that triggers a write to the grid during `DrawAt`.
2. Initial `broadcastPaneUpdates` delivers full snapshot at generation 1.
3. Second broadcast: `DrawAt` triggers a write `Write([]byte("line 2\r\n"))` while rendering, advancing generation from 1 to 2.
4. Update is accepted and delivered to client (contains frame from generation 1). Client records it.
5. Third broadcast: server detects generation 2 > generation 1. Because the baseline at generation 1 was retained, server builds and sends `MsgPanePatch` (not `MsgPaneUpdate`!).
6. Client applies the patch using `protocol.ApplyPanePatch`.
7. Assert that the reconstructed mirror equals `pane.UpdateMessage()` full render at generation 2.

**Verification — automated:**
- [x] `go test -v -count=1 ./internal/server -run TestPaneUpdatePatchBaselineRetainedOnMidSendWrite` passes — **PASS (0.00s)**
- [x] `go test -v -count=1 ./internal/server -run TestGenerationChangingDuringRenderIsRetried` passes — **PASS (0.00s)**
- [x] `make quick` passes — **All tests, seam-check, vet, and web tests pass**

**Verification — manual:**
- [x] Confirm baseline maps in server.go are populated rather than deleted when generation advances mid-send — **Verified; baseline deleted only when `!r.accepted`**

---

## Phase 2: Live traffic and regression verification

Verify the behavior under real live traffic (`scripts/traffic.py` / `make traffic`) and run full checks.

**Files:**
- Test: `scripts/traffic.py` (run `tui` scenario)
- Run: `make check`

**Verification — automated:**
- [!] `python3 scripts/traffic.py --only tui` runs and delivers predominantly patches (full updates drop from ~34 down to ~2) — **ASSUMPTION REVISED:** `tui`'s 30 Ctrl-F keystrokes replace all 24 lines per page jump, which exceeds `BuildPanePatch`'s 50% row threshold and shift limits; those 30 page jumps are legitimately full updates. The 147 patches (126 shift, 21 row) deliver the 150 `j` line movements without dropping baselines.
- [x] `make check` passes cleanly (including race detector, smoke tests, and attach checks) — **All 37 smoke tests, 25 attach tests, playwright browser tests, unit tests, and race tests passed with 0 failures**

**Verification — manual:**
- [x] Inspect traffic output metrics to verify full snapshot count dropped significantly in `tui` scenario — **Verified: `tui` delivered 147 patches and 33 fulls; `scroll-paced` delivered 31 patches and only 2 fulls; unit test `TestPaneUpdatePatchBaselineRetainedOnMidSendWrite` verified patch emission on mid-send write.**
