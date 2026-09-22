# Research: Issue #55 - Escapee Reap Tests Signal Delivery

## 1. Sequence of Events in `Pane.Kill(grace)` (`internal/server/ptyx/reap.go:135-170`)

When `Pane.Kill(grace)` is invoked:
1. If `p.done` is already closed, close master and return nil (line 136-141).
2. `descendants, _ := Descendants(p.Cmd.Process.Pid)` snapshots the child process tree deepest-first while root is alive (line 143).
3. `p.signalDescendants(descendants, syscall.SIGTERM)` signals each snapshotted descendant with SIGTERM (line 145).
4. `p.signalRoot(syscall.SIGTERM)` signals `-p.PGID` (the process group) and `p.Cmd.Process.Pid` with SIGTERM (line 146).
5. `rootExited := p.waitForExit(grace)` waits up to `grace` duration for `p.done` (closed by the goroutine waiting on `cmd.Wait()`) (line 148).
6. `_ = p.Master.Close()` closes the master PTY descriptor (line 150).
7. `p.signalDescendants(descendants, syscall.SIGKILL)` unconditionally signals all snapshotted descendants with SIGKILL (line 155).
8. If `!rootExited`, `p.signalRoot(syscall.SIGKILL)` signals `-p.PGID` and root PID with SIGKILL, and waits up to `killWait` (1s) (lines 157-164).
9. If `!rootExited || anyAlive(descendants, aliveWait)`, returns error (line 166).

## 2. Current Setup in `reap_test.go`

`internal/server/ptyx/reap_test.go` has three reap tests:

1. `TestKillReapsEscapedGrandchild` (lines 14-39):
   - Root: interactive `/bin/sh` on PTY.
   - Escapee command: `fmt.Sprintf("%s && ./%s 300 &\n", linkSleepAs(tag), tag)`
   - The sleeper is backgrounded (`&`). Under interactive `/bin/sh` with job control, it gets its own PGID (`pgid == pid`).
   - Signal dispositions: default (no traps).
   - Expected reap mechanism: SIGTERM or SIGKILL pass in `Kill`.

2. `TestKillReapsSIGTERMIgnoringEscapee` (lines 47-73):
   - Root: interactive `/bin/sh` on PTY.
   - Escapee command: `fmt.Sprintf("%s && sh -c 'trap \"\" TERM; exec ./%s 300' &\n", linkSleepAs(tag), tag)`
   - The sleeper traps SIGTERM (`SIG_IGN`), backgrounded (`&`).
   - Signal dispositions: SIGTERM ignored, SIGHUP default.
   - Expected reap mechanism: SIGKILL escalation against descendants snapshot.

3. `TestKillEscalatesEvenWhenRootExitsWithinGrace` (lines 90-111):
   - Root: non-interactive `/bin/sh -c ...` on PTY (dies promptly on SIGTERM).
   - Escapee command: `sh -c 'trap "" TERM HUP; exec -a %s sleep 300' & sleep 60`
   - Note: traps BOTH `TERM` and `HUP`.
   - Note: still uses `exec -a %s sleep 300` rather than `linkSleepAs` (a bashism that was fixed in the other two tests in commit 775d80e but missed here).

## 3. PTY Master Close and SIGHUP Propagation

- When `p.Master.Close()` executes:
  - The OS kernel detects the closing of the controlling terminal.
  - The kernel sends `SIGHUP` to the session leader (the root `/bin/sh`).
- If the root `/bin/sh` is alive and exits via SIGHUP:
  - An interactive shell sends SIGHUP to all of its background jobs as part of normal exit handling.
  - If the escapee has not trapped SIGHUP, it receives SIGHUP and terminates immediately.
  - This occurs regardless of whether `Kill` sent SIGTERM or SIGKILL to descendants or process groups.

## 4. The "Stranger: removing more makes fewer tests fail" Mystery

Observed in issue #55:
- When `signalDescendants` drops SIGKILL, `TestKillReapsSIGTERMIgnoringEscapee` fails.
- When BOTH `signalDescendants` AND `signalRoot` drop SIGKILL, `TestKillReapsSIGTERMIgnoringEscapee` PASSES!

Explanation verified by probe:
- When `signalDescendants` drops SIGKILL, but `signalRoot` sends SIGKILL:
  - The root shell (`/bin/sh`) is killed abruptly by SIGKILL.
  - Because SIGKILL cannot be caught or handled, `/bin/sh` terminates instantly without executing cleanup or sending SIGHUP to its background jobs.
  - The escapee (which trapped SIGTERM, and was not sent SIGKILL because `signalDescendants` dropped it) never receives SIGHUP or SIGKILL. It remains alive, failing the test.
- When BOTH `signalDescendants` and `signalRoot` drop SIGKILL:
  - The root shell (`/bin/sh`) is NOT killed by SIGKILL.
  - Instead, the root shell is terminated by the SIGHUP triggered when `p.Master.Close()` closes the controlling terminal.
  - When the shell terminates via SIGHUP, the shell itself propagates SIGHUP to all of its background jobs.
  - The escapee (which only trapped SIGTERM, not SIGHUP) receives SIGHUP from the dying shell and terminates!
  - Therefore, the escapee dies and the test passes, even with all SIGKILL signaling completely neutered.

## 5. Analogue Paths and Related Fixtures

- `TestKillEscalatesEvenWhenRootExitsWithinGrace` (`reap_test.go:92`):
  Uses `exec -a %s sleep 300`, which was fixed in the other two tests (commit 775d80e) to use `linkSleepAs` because `exec -a` is an unportable bashism that breaks on systems where `/bin/sh` is `dash`.
- `scripts/ptycheck.py` (`verify-exit` target in Makefile):
  Spawns an escapee via `setsid python3 ...` and tests whether the full process tree is reaped. Note that `setsid` completely detaches from the session, so SIGHUP from master close does not reach it.
