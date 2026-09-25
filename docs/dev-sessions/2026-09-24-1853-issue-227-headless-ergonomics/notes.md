# Notes — issue 227

## Deviations from plan

- **Phase 1:** the "extra operands" test failed pre-fix with a *dial* error,
  not a nil error as the plan said. runSend dialled before checking arity.
  Same test, same verdict.
- **Phase 3, the drain ordering (added; not in plan):** reviewing
  `watchKeptPane` showed the reap and the pty reader's EOF are independent.
  If the pane is marked exited at the reap, `wait` → `capture` can miss the
  child's last output. The kept pane now reports its exit only after the reap
  **and** the reader draining to EOF, capped by `keptDrainCeiling` (1s) for a
  background job holding the pty.
  - `TestKeptPaneExitFollowsItsOutput` guards it, but **it never failed
    before the fix** (100 iterations under -race). The window is sub-ms on
    this machine. The fix rests on the ordering argument, not the test.
  - `TestKeptPaneExitDoesNotWaitOnBackgroundJobs` never reaches the ceiling
    on macOS: the session leader's exit hangs up the pty, so EOF comes
    anyway. `TestWatchKeptPaneHonoursDrainCeiling` drives the ceiling
    directly with a drain that never fires.
- **Spec deviation (agreed in plan):** `exit_code` is always present in
  status JSON; `exited` says whether it means anything.

## Observations

- **Phase 6, fixed (not in plan):** running SKILL.md's examples showed that
  `split` hoisted every dash-leading operand out as its own flag, so
  `split --keep sh -c '…'` and `split make -j4 test` failed with "flag
  provided but not defined". It was noted earlier as pre-existing and "to
  document", but this PR's argv mode made it easy to hit. `split` now parses
  with plain `fs.Parse`, so its flags end at the first command word, as with
  tmux and env. Pinned by `TestSplitLeavesCommandFlagsAlone`, which failed
  first with exactly that error. `send`/`capture`/`wait` keep reorderFlags
  (`send 1 text -e`).
- `capture` returns the screen as drawn, so long lines come back split at
  the pane width. That's pre-existing; SKILL.md now says so.
- Every SKILL.md example was executed against `./bin/wideboi` with the real
  login shell:
  - smoke test (verbatim, extracted from the doc): ok.
  - interactive REPL pattern (verbatim): ok.
  - Pattern A, argv quoting, one-string, Ctrl-C (wait → 130), status JSON,
    send to exited, and wait on a gone unkept pane: all behaved as
    documented.
- **Rebase onto main (#183, #225):** #183 had taken protocol v9 and oneof
  slots server 13 / client 16. This work is now **protocol v10**, with
  `wait_response = 14` and `wait_request = 17`. Bindings were regenerated and
  the web version strings moved to `wideboi.v10`. plan.md's "v9" references
  predate the rebase. The per-phase history was kept on local branch
  `issue-227-phases`.

## Copilot review (PR #229)

- **Fixed:** waiters were not answered when the session ended.
  - Two routes: `kill-session`/owner shutdown (`Server.Close`), and closing
    the last pane, where `s.Close()` fired 50ms later while the pane's
    hangup could take its 2s grace.
  - The root cause was pre-existing: `ServerSocketConn.Close` drops anything
    still queued.
  - The fix is targeted, not a transport-wide drain. There's a new optional
    `ServerSocketConn.Drain(within)` (an inflight counter), and only
    connections just sent a wait answer are drained, with a 500ms ceiling,
    before the session hangs up. `Close` answers each pane's waiters, and the
    last-pane path waits on the removal (`removePaneLocked` returns a done
    channel).
  - `TestWaitAnsweredWhenSessionEnds` (e2e, real socket) failed first on both
    routes with "server closed connection before sending response".
    `TestServerSocketConnDrain` covers the transport. Both pass 4× under
    -race and on Linux.
  - `TestWaitAnsweredWhenSessionEnds` pauses 1s for the background `wait` to
    register, because there's no externally observable state to poll. A
    late registration fails the test; it can't pass wrongly.
- **Fixed:** SKILL.md now qualifies "capture after wait sees the end" for
  the background-job case.
- **Skipped:** "timed-out waiter responses can be lost". The waiting client
  drains continuously while blocked, so a full 256-slot queue plus a 1s
  stall isn't realistic.
- **Skipped:** "exit metadata is not broadcast". Out of scope by spec (no
  TUI/web rendering of exited panes); `status` gets it on request.
