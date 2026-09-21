# Escapee Reap Tests Signal Invariant Implementation Plan

**Goal:** Ensure `internal/server/ptyx`'s escapee reap tests fail if `Kill`'s descendant signalling is removed or broken, by preventing incidental SIGHUP from reaping escapees.

**Approach:** Update `TestKillReapsEscapedGrandchild` to ignore SIGHUP (`trap "" HUP`), update `TestKillReapsSIGTERMIgnoringEscapee` to ignore both SIGTERM and SIGHUP (`trap "" TERM HUP`), and replace the bashism `exec -a` in `TestKillEscalatesEvenWhenRootExitsWithinGrace` with `linkSleepAs`. Verify via explicit mutation testing that neutered signalling fails the tests.

**Tech stack:** Go, POSIX shell, syscall signals.

---

## Phase 1: Update escapee reap tests to trap SIGHUP and use linkSleepAs

**Files:**
- Modify: `internal/server/ptyx/reap_test.go` — trap `HUP` in `TestKillReapsEscapedGrandchild`, trap `TERM HUP` in `TestKillReapsSIGTERMIgnoringEscapee`, and use `linkSleepAs` + `trap "" TERM HUP` in `TestKillEscalatesEvenWhenRootExitsWithinGrace`.

**Key changes:**
In `TestKillReapsEscapedGrandchild`:
```go
	tag := fmt.Sprintf("wideboi-escapee-%d", time.Now().UnixNano())
	cmd := fmt.Sprintf("%s && sh -c 'trap \"\" HUP; exec ./%s 300' &\n", linkSleepAs(tag), tag)
```

In `TestKillReapsSIGTERMIgnoringEscapee`:
```go
	tag := fmt.Sprintf("wideboi-escapee-%d", time.Now().UnixNano())
	cmd := fmt.Sprintf("%s && sh -c 'trap \"\" TERM HUP; exec ./%s 300' &\n", linkSleepAs(tag), tag)
```

In `TestKillEscalatesEvenWhenRootExitsWithinGrace`:
```go
	tag := fmt.Sprintf("wideboi-escapee-%d", time.Now().UnixNano())
	script := fmt.Sprintf(`%s && sh -c 'trap "" TERM HUP; exec ./%s 300' & sleep 60`, linkSleepAs(tag), tag)
```

**Verification — automated:**
- [x] `go test -v -run TestKillReaps ./internal/server/ptyx/` passes — **both passed in 3.10s**
- [x] `go test -v -run TestKill ./internal/server/ptyx/` passes — **all four passed in 3.25s**
- [x] `make quick` passes — **fmt, vet, seam-check, go test clean in 3.85s**

**Verification — manual:**
- [x] Code comments accurately document why SIGHUP is trapped and link back to `p.Master.Close()`.

---

## Phase 2: Mutation verification & 4-run stability check

**Files:**
- Temporary mutations to `internal/server/ptyx/reap.go` (and revert):
  - Test mutation 1: neuter both `signalRoot` and `signalDescendants`.
  - Test mutation 2: neuter `signalDescendants` for `SIGKILL`.

**Verification — automated:**
- [x] Mutation 1 (both neutered): `go test -count=1 -run TestKill ./internal/server/ptyx/` fails all reap tests — **all three failed with process tree survived SIGKILL**
- [x] Mutation 2 (`signalDescendants` drops SIGKILL): `go test -count=1 -run TestKill ./internal/server/ptyx/` fails `TestKillReapsSIGTERMIgnoringEscapee` and `TestKillEscalatesEvenWhenRootExitsWithinGrace` — **both failed as expected; TestKillReapsEscapedGrandchild passed via SIGTERM pass**
- [x] Mutations reverted cleanly (`git diff internal/server/ptyx/reap.go` is empty) — **verified clean diff**
- [x] 4x consecutive runs of `go test -count=1 ./internal/server/ptyx/` pass (satisfying CLAUDE.md concurrency rule) — **4 runs passed (3.855s, 3.848s, 3.867s, 3.853s)**
- [x] `make check` passes cleanly — **vet, seam-check, tests, race, smoke (25/25), attachcheck (7/7), golden all passed**

**Verification — manual:**
- [x] Confirm no leftover processes matching `wideboi-escapee` in process table (`ps aux | grep wideboi-escapee`) — **verified no leaked processes**
