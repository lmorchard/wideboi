# Simplify teardown to tmux's hangup model Spec

**Goal:** Tear panes down the way tmux (and every terminal emulator) does: close the pty master and let the kernel's hangup do the rest. This deletes the escapee-tracking machinery, whose complexity produced a string of partial fixes and one mass-kill incident.

**Source:** Les, 2026-09-23. It followed #83, #89, #88, the abandoned #39 attempt, and a read of the tmux source (`~/devel/mine/tmux`).

## Current state

See `research.md` for `file:line` detail. The load-bearing facts:

- `ptyx.(*Pane).Kill` does the following:
  1. Snapshots descendants (a `ps` ppid walk) plus tty mates (`ps` tty).
  2. SIGTERMs the snapshot, the process group and the root.
  3. Waits for the grace period.
  4. Closes the master.
  5. SIGKILLs the snapshot, and the root if it is still alive.
  6. Reports an error for any survivor.

  It lives at `reap.go:183-220`, and its only production caller is `server.(*Pane).Close` (`pane.go:382`).
- The server polls `ps` every second under `s.mu`, recording descendants in `s.escapees`. `Close` SIGKILLs the survivors after the panes close (`server.go:244,263,878-927,990-1002`).
- `KillResidual` and `CloseResidual` exist to budget the post-grace SIGKILL work. Their only consumer is `cmd/wideboi/hangup.go:14` (`shutdownCeiling`).
- The stated guarantee is "nothing outlives the session", written in `README.md:25-30` and `LESSONS.md:72-135`. `make verify-exit`, `attachcheck.py` and ptyx's `reap_test.go` enforce it with planted `nohup`/`trap HUP` escapees.

What tmux does: `window_pane_destroy` closes the pty fd (`tmux/window.c:1533-1541`), and `kill-server` SIGTERMs tmux itself (`cmd-kill-server.c`). It never signals pane processes. I probed on macOS with the master closed:

| Pane root | After the hangup |
|---|---|
| Interactive `sh` | exits |
| Interactive `zsh` | exits |
| Interactive shell ignoring SIGHUP | exits (its next terminal read fails) |
| Non-shell root ignoring SIGHUP and not reading the terminal | survives |

## Desired end state

- **Closing a pane** (verb, shell exit, or `Server.Close`) closes its pty master, then waits up to the close grace for the root to exit. Nothing is signalled. Anything still alive afterwards is left alone.
- `Server.Close` keeps its ordering: stop listening, hang up every pane in parallel and wait (bounded by grace), then hang up on clients. For normal shells the client's acknowledgement still arrives only after the panes are gone.
- **Semantics are tmux's:**
  - The shell and its foreground job get SIGHUP from the kernel.
  - An interactive shell passes SIGHUP on to its jobs.
  - Anything that opted out survives by design: `nohup`, `disown`, `trap '' HUP`, `setsid`, daemons.
- There is **no process-table access anywhere in production code**. No `ps`, no escapee set, no background poll.
- **Tests assert the new contract:**
  - Pane shells and the `wideboi server` are gone after each shutdown route. `verify-exit` and `attachcheck` keep those assertions.
  - One test pins that a `nohup`'d job **survives** a pane hangup, so a reaper can't quietly come back.
- **Docs** (README, LESSONS, code comments) state the hangup model and why we chose it.
- **Issues:** #39 is closed as not planned, pointing at this change. #88 gets a comment saying its reaper was removed by design.

## Design decisions

- **Pure hangup, with no signal fallback for a stuck root.**
  - **Why:** interactive shells exit on hangup even when ignoring SIGHUP (probed). The only root that survives is one that deliberately ignores SIGHUP and never reads its terminal, the same "outlive my terminal" choice as `nohup`. Overriding it contradicts the model, and it would keep the escalation code, the timing budgets and a macOS wedge caveat alive for a case that almost never happens.
  - **Rejected:** SIGKILL of the root after grace. It's safe (our direct child) but has marginal value.
- **Rename `ptyx.(*Pane).Kill(grace) error` to `Hangup(grace)`, with no error return.**
  - **Why:** a method called `Kill` that sends no signals misleads readers. With no survivors to report, there is no error.
  - `Hangup` is idempotent. When the root has already exited, it closes the master (a second close is harmless) and returns.
- **Delete, not deprecate:**
  - In ptyx: `Descendants`, `TTYMates`, `appendNew`, `signalDescendants`, `signalRoot`, `anyAlive`, `processAlive`, `killWait`, `aliveWait`, `KillResidual`, and the `PGID` field (whose only use is `signalRoot`).
  - In server: `procEntry`, `readProcTable`, `parseProcTable`, `procTableFields`, `pollDescendants(Locked)`, `s.escapees`, the 1s poll ticker, and Close's kill loop.
  - **Why:** each exists only to serve the removed guarantee, and dead reaper code invites resurrection.
- **`CloseResidual` goes, and `shutdownCeiling = CloseGrace + signalExitMargin`.**
  - **Why:** after grace, Close does only in-process work (closing grids and transports). The residual budgeted SIGKILL passes that no longer exist.
- **One flipped test, at the ptyx level:** `TestHangupLeavesANohupJobRunning` plants a `nohup`'d job, calls `Hangup`, asserts the job is **still alive**, then cleans it up.
  - **Why:** it's the cheapest and most deterministic place to pin the design.
  - **Rejected:** flipping one of the script cases, which would be slower and would leave a live process behind on failure.
- **Script cases keep their shell and server assertions and lose their planted escapees.**
  - In `ptycheck.py`, `plant_escapee` and `--no-escapee` go. `attachcheck.py` loses `plant_escapee` and `case_server_reaps_a_reparented_escapee`.
  - `case_sigkilled_owner_takes_the_session_with_it` and `case_server_reaps_its_panes_on_signal` drop their planting and keep asserting the shells and server are gone.
  - **Why:** the shutdown routes (signal, SIGKILLed owner, quit, kill-session) are still load-bearing, and the escapee part is what changed.
- **The LESSONS "Teardown" section is rewritten, not appended to.**
  - New contract: nothing outlives the session *except what opted out of hangup*, which is tmux's model.
  - It keeps a short "why we stopped tracking escapees" history: the whack-a-mole of #83/#89/#88/#39, and the `ps` mass-kill incident with its rule. Any `ps` parse that feeds a kill must be exact.
  - The #83/#87, #88 and #39 detail paragraphs are removed. They describe code that no longer exists.

## Patterns to follow

- Parallel pane close in `Server.Close` (`server.go:977-988`): unchanged apart from calling `Close`, which now calls `Hangup`.
- `waitForExit` on the `done` channel from `Spawn`'s single-`Wait` goroutine (`reap.go:259-266`, `ptyx/pane.go:95-98`). `Hangup` keeps this, and it is safe because only that goroutine reaps.
- Test style in `reap_test.go`: a renamed sleep via `linkSleepAs` (:183-185) and `waitForProcess` (:187-199). The flipped test reuses both.
- Waits are ceilings, not durations (CLAUDE.md). A timing change is verified by four repeated runs.

## What we're NOT doing

- No SIGKILL or SIGTERM of the root, process group or anything else, even after grace.
- No Linux `PR_SET_CHILD_SUBREAPER` or cgroups.
- No changes to detach, owner and `MsgShutdown` semantics, the re-raise guard (`hostterm/guard.go`), or `spawn.go`'s `Setsid`.
- No `remain-on-exit`-style behaviour, and no change to what happens in the UI when a pane exits.
- No edits to earlier dev-session specs and plans (v1, #25, plan-2). They are history.
- No `ptycheck` stray-scan changes, beyond removing the escapee planting.
- No changes to smoke's teardown helpers, which SIGKILL their own descendants as harness cleanup and don't test the product.

## Open questions

- **Does dropping `CloseResidual` make `shutdownCeiling` (from 4s to 2.5s) too tight for `TestKillSessionShutsDownAServer` or the scripts under load?** Default: go with 2.5s, and verify with four repeated `make check` runs. If it flakes, add back a named margin rather than a sleep.
- **Must `Pane.Close` still surface grid-close and dropped-keystroke errors?** Default: yes, unchanged. Only the Kill error disappears from the join.
