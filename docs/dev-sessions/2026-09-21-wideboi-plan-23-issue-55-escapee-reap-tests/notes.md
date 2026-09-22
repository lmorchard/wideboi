# Notes: Issue #55 - Escapee Reap Tests Signal Delivery

## Overview
Found in #54, and tracked in #55: `Kill` calls `p.Master.Close()` before the SIGKILL pass. Closing the PTY master delivers SIGHUP to the session. If the escapee does not ignore SIGHUP, the shell or kernel delivers SIGHUP to the escapee, reaping it "for free" regardless of whether `Kill`'s descendant signalling runs.

Furthermore, removing root signalling made `TestKillReapsSIGTERMIgnoringEscapee` pass even when descendant signalling was neutered because an intact root shell receives SIGHUP on master close and forwards SIGHUP to background jobs on exit; abruptly SIGKILLing the root prevents that forward.

## Findings & Changes
1. `TestKillReapsEscapedGrandchild` updated to trap `HUP` (`trap "" HUP`). When `signalDescendants` is neutered, it now fails as expected instead of being killed by SIGHUP upon master closure.
2. `TestKillReapsSIGTERMIgnoringEscapee` updated to trap both `TERM` and `HUP` (`trap "" TERM HUP`). When SIGKILL escalation to descendants is neutered, it now reliably fails regardless of whether root is signalled or not.
3. `TestKillEscalatesEvenWhenRootExitsWithinGrace` was updated to use `linkSleepAs` instead of `exec -a` (bashism that previously failed on Ubuntu/dash).
4. Mutation testing proved:
   - Neutering all signalling causes all 3 reap tests to fail.
   - Dropping SIGKILL in `signalDescendants` causes `TestKillReapsSIGTERMIgnoringEscapee` and `TestKillEscalatesEvenWhenRootExitsWithinGrace` to fail while `TestKillReapsEscapedGrandchild` passes via the SIGTERM pass.
5. All 4 consecutive test runs passed with zero flakes or leaked processes.

6. Copilot review addressed: corrected the verification command in `plan.md` to `TestKill` to match the 3-test assertion claim.
7. CI fix for `TestWriteBoundedDeadline`: on Linux, the PTY flip buffer initially accepts ~18KB before a write times out; during the first timeout wait, the kernel's asynchronous `flush_to_ldisc` workqueue drains 2KB into `n_tty`, temporarily creating room for a subsequent write. Waiting for 3 consecutive write timeouts ensures both the flip buffer and `n_tty` queues are fully saturated before testing the deadline timeout. Verified in Linux container and macOS.
8. Addressed Copilot round 2 comments: asserted `timeouts >= 3` in both saturation loops (`pane_test.go` and `pane_wedge_test.go`) to prevent loops from proceeding if 64 iterations exit without reaching 3 consecutive timeouts. Also matched `pane_wedge_test.go` saturation check to require zero progress (`n == 0 && os.ErrDeadlineExceeded`) and fail immediately on unexpected errors.

## Plan & Progress
- [x] Phase 1: Update escapee reap tests to trap SIGHUP and use `linkSleepAs`
- [x] Phase 2: Mutation verification & 4-run stability check
