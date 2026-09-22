# Remove the dirtyFrames follow-up tick: Implementation Plan

**Goal:** Present a frame only when `cli.Draw` reports a change.

**Approach:** `present()` (#75) already writes out the held-back `MoveTo` within the frame, so the follow-up tick has nothing left to do except write an empty synchronized update. Replace the counter with a direct `if cli.Draw(...) { present(scr) }` in both loops.

**Tech stack:** Go, the pty smoke harness (`scripts/smoke.py`).

**Verification deviation from spec:** the spec suggested asserting that `present` on an unchanged screen leaves the cursor in place. That holds on current main too, so it can't fail first. The wire shows the difference instead. On main, every changed frame is followed by the exact bytes `?2026h ?25h ?25l ?2026l` (a bracket holding only Flush's hide/show wrapper). No real frame produces that with the cursor shown, because `copyToHostScreen` writes its own `?25h` before the bracket.

---

## Phase 1: Present only changed frames

**Files:**
- Modify: `cmd/wideboi/main.go`: attach loop (~L298, ~L346-355) and standalone loop (~L436, ~L481-491). Drop `var dirtyFrames int` and the counter logic.
- Modify: `scripts/smoke.py`: new case `no empty frames` (`EMPTY_SYNC_UPDATE` signature, counted after startup plus one command).

**Key changes:**

```go
case <-frame.C:
	screenLock.Lock()
	if cli.Draw(scr, nil, nil) {
		present(scr)
	}
	screenLock.Unlock()
```

(The standalone loop keeps its `!stopped.Load()` guard around the same body.)

**Verification — automated:**
- [x] `no empty frames` fails on main. **FAIL: 4 empty synchronized updates.**
- [x] `no empty frames` passes after the change. **OK.**
- [x] `make quick` passes. **All packages ok.**
- [x] `make check` ×4 (frame-timing change). **4/4 OK: exit 8/8, smoke 30/30, golden matches.**

**Post-review addition (Copilot):** `scripts/attachcheck.py` gets `attached client presents no empty frames`, covering the attach loop.
- [x] Attach case passes here and fails with an extra `present` put back into the attach loop only. **OK / FAIL (4 empty updates).**
- [x] `make check` ×4 after adding it. **4/4 OK: attach-check 9/9, smoke 30/30, golden matches.**

**Verification — manual:**
- [ ] Focus switch, entering and leaving control mode, and help open/close still put the cursor in the right place, with no stale frame.
