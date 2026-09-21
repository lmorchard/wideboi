# Escapee Reap Tests Signal Invariant Spec

**Goal:** Ensure `internal/server/ptyx`'s escapee reap tests fail if `Kill`'s descendant signalling is broken or removed, proving `Kill`'s anti-leak guarantees rather than relying on incidental SIGHUP from PTY master closure.

**Source:** https://github.com/lmorchard/wideboi/issues/55

## Current state

- `internal/server/ptyx/reap.go:135-170` implements `Pane.Kill(grace)`. It snapshots descendants while the root is alive, signals them with SIGTERM, signals root/PGID with SIGTERM, waits up to `grace`, closes `p.Master`, and then sends SIGKILL to descendants snapshot and (if root alive) root/PGID.
- `TestKillReapsEscapedGrandchild` (`internal/server/ptyx/reap_test.go:14-39`) spawns an escapee via `./<tag> 300 &`. The escapee does not trap `SIGHUP`.
- `TestKillReapsSIGTERMIgnoringEscapee` (`internal/server/ptyx/reap_test.go:47-73`) spawns an escapee via `sh -c 'trap "" TERM; exec ./<tag> 300' &`. It traps `TERM` but does not trap `SIGHUP`.
- When `p.Master.Close()` is called in `Kill` (`reap.go:150`), closing the controlling terminal causes the shell to exit and propagate `SIGHUP` to background jobs, killing both escapees without requiring any descendant signalling from `Kill`.
- Consequently, neutering `signalDescendants` or `signalRoot` leaves both tests passing.
- Additionally, `TestKillEscalatesEvenWhenRootExitsWithinGrace` (`reap_test.go:90-111`) traps `TERM HUP` as intended, but still uses `exec -a` (a bashism that breaks on `dash`/Linux) instead of `linkSleepAs`.

## Desired end state

- `TestKillReapsEscapedGrandchild` sets up its escapee to trap `HUP` (`trap "" HUP`), ensuring it ignores `SIGHUP` and can only be reaped by `Kill`'s explicit signals (`SIGTERM` or `SIGKILL`).
- `TestKillReapsSIGTERMIgnoringEscapee` sets up its escapee to trap both `TERM` and `HUP` (`trap "" TERM HUP`), ensuring it ignores both `SIGTERM` and `SIGHUP` and can only be reaped by `Kill`'s explicit `SIGKILL` pass on descendants.
- `TestKillEscalatesEvenWhenRootExitsWithinGrace` is updated to use `linkSleepAs` instead of `exec -a`, ensuring portability across shells.
- All three tests pass under normal conditions.
- Mutation verification:
  - If `signalDescendants` drops `SIGKILL`, `TestKillReapsSIGTERMIgnoringEscapee` and `TestKillEscalatesEvenWhenRootExitsWithinGrace` fail.
  - If `signalDescendants` drops `SIGTERM` and `SIGKILL`, all three tests fail.
  - If both `signalDescendants` and `signalRoot` are neutered, all three tests fail.

## Design decisions

- **Decision:** Trap `HUP` in the escapee shell commands (`trap "" HUP` and `trap "" TERM HUP`).
  - **Why:** `TestKillEscalatesEvenWhenRootExitsWithinGrace` already established this precedent (`reap_test.go:85-89`). It isolates the test assertion to the behavior `Kill` actually promises (reaching escaped descendants via targeted signals) without allowing incidental SIGHUP from `p.Master.Close()` to falsely pass the test.
  - **Rejected:** Asserting on signal delivery via custom wrappers (adds complex IPC/logging scaffolding to what should be a fast unit test).
  - **Rejected:** Moving `p.Master.Close()` after the SIGKILL pass (closing the master before SIGKILL is load-bearing for macOS PTY exit teardown and reader unblocking).

- **Decision:** Replace `exec -a` with `linkSleepAs` in `TestKillEscalatesEvenWhenRootExitsWithinGrace`.
  - **Why:** Aligns with commit 775d80e, removing the bashism so tests don't fail during setup on Linux distributions using `dash`.
  - **Rejected:** Leaving `exec -a` in place (latent portability bug).

## Patterns to follow

- Existing `linkSleepAs` helper and `trap` pattern in `internal/server/ptyx/reap_test.go:57, 92, 146-147`.
- Diagnostic wait and polling patterns in `waitForProcess` (`reap_test.go:149-159`).

## What we're NOT doing

- Changing `Pane.Kill` production logic or reordering `Master.Close()`.
- Modifying `scripts/ptycheck.py` or other integration/smoke harnesses.
- Adding signal delivery logging hooks or IPC to production code.

## Open questions

None.
