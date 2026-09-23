# Simplify teardown to tmux's hangup model Implementation Plan

**Goal:** Tear panes down by closing the pty master and letting the kernel's hangup do the rest. Delete every process-table read and every signal the server and ptyx send to pane processes.

**Approach:**
- `ptyx.(*Pane).Kill` becomes `Hangup(grace)`: close the master, then wait up to grace for the root. No signals, no error.
- The server's `ps` poll, `s.escapees` and Close's kill loop are deleted.
- Tests pin the new contract. Shells and the server are gone after every shutdown route, and a `nohup`'d job **survives** a hangup by design.
- Docs are rewritten around the model.

**Tech stack:** Go (`internal/server`, `internal/server/ptyx`, `cmd/wideboi`), plus the Python pty harness (`scripts/ptycheck.py`, `scripts/attachcheck.py`).

**Session rule from the incident** (`kill-path-parsing-mass-kill` memory): no test in this plan may send real signals to a *widened* selection. The only signals this plan's tests send are cleanup `kill`s of PIDs that the test itself planted and identified by a unique tag.

---

## Phase 1: The server stops tracking escapees

This deletes the server's `ps` poll and Close's `kill -9` loop. It is independently green: until Phase 2, `ptyx.Kill`'s own snapshot still reaps the in-tree `nohup` jobs that the scripts plant, and the reparented one via `TTYMates`, so `verify-exit` and `attachcheck` pass unchanged.

TDD opt-out: this phase is pure deletion of a subsystem. The behaviour change it causes is covered by the red-then-green test in Phase 2.

**Files:**
- Modify `internal/server/server.go`:
  - Remove the `escapees` field (:32) and its initialisation in `NewServer` (:124).
  - In `Run`, remove the 1s `ticker` and its `case <-ticker.C: s.pollDescendants()` (:244-245, :263-264).
  - Delete `procEntry`, `readProcTable`, `procTableFields`, `parseProcTable`, `pollDescendantsLocked` and `pollDescendants` (:828-927).
  - In `Close`, delete the escapee copy and its #83 comment (:963-970), and the kill loop with its comment (:990-1002).
  - Drop any imports (`os/exec`, `strconv`, `strings`, `os`) that the build reports as unused. Check each one: `os` is still used by `Getwd`.
- Delete `internal/server/escapee_test.go`. All four of its tests cover the deleted code.
- Modify these fixtures to remove the `escapees: make(map[int]string),` line: `internal/server/lifecycle_test.go:25`, `status_test.go:80`, `transport_close_test.go:37`, `:77`.

**Key changes:** `Close` after the deletion reads, in order:
1. `close(stopCh)`
2. close the listener
3. snapshot and clear `s.panes`
4. close all panes in parallel and `wg.Wait()`
5. hang up the transports

**Verification — automated:**
- [!] `grep -rn 'escapees\|readProcTable\|parseProcTable\|pollDescendants\|procEntry' internal/ cmd/` is empty: **two hits remain, both the word "escapees" in `ptyx/reap.go` comments** (:128, :202). The pattern was too broad for this phase. No identifier remains, and Phase 2 rewrites that file
- [x] `make quick` passes. **All ok**
- [x] `make check` passes. **Run 1: smoke 35/1, where the one failure was `idle emits no bytes`, the known load flake; smoke then passed 36/0 three times in a row. Run 2: exit 0, smoke 36/0, attach 18/0, verify-exit clean.** No stray this time: the worktree binary path differs from the old session's. The only acceptable `verify-exit` failure is the documented stray from a detached session predating the run; record its pid and start time

**Verification — manual:**
- [x] Les skims the `Close` diff: the ordering above is intact. **Les confirmed**

Commit: `Phase 1: server stops tracking escapees`

---

## Phase 2: ptyx hangs up instead of killing

Replace `Kill` with `Hangup`, delete the reaper, and flip one test to pin that a `nohup`'d job survives. The scripts lose their planted escapees in the same phase, because once `Kill` is gone `verify-exit` would fail on the surviving `nohup` job. `shutdownCeiling` loses the kill residual.

**Files:**
- Rename `internal/server/ptyx/reap.go` → `internal/server/ptyx/hangup.go` with `git mv`, and rewrite it (below).
- Rename `internal/server/ptyx/reap_test.go` → `internal/server/ptyx/hangup_test.go` with `git mv`, and rewrite it (below).
- Modify `internal/server/ptyx/pane.go`:
  - Delete the `PGID` field (:22) and `PGID: cmd.Process.Pid` (:88), plus its "Setsid makes the child a session and group leader" comment.
  - `Setsid` itself stays: it gives the pane its own session and controlling tty, and that is what the hangup acts on.
  - Change the `Close` doc comment (:126) to `// Close releases the PTY master. Hangup is the teardown path.`
- Modify `internal/server/ptyx/pane_test.go`:
  - Replace the three `Kill(testGrace)` error-checking cleanups (:37, :61, :86) with `t.Cleanup(func() { p.Hangup(testGrace) })`, and delete the "Kill, not Close" comment (:81-84).
  - Delete the `PGID` assertions (:91-96).
  - Change `:136` to `p.Hangup(testGrace)`.
- Modify `internal/server/pane.go`:
  - Delete `const CloseResidual` (:22).
  - In `Close`, replace `killErr := p.pty.Kill(...)` with `p.pty.Hangup(p.graceOrDefault())` and start `errs` empty.
  - Change "SIGTERM grace" in the `graceOrDefault` doc to "hangup grace".
- Modify `cmd/wideboi/hangup.go`: `const shutdownCeiling = server.CloseGrace + signalExitMargin`, and change its comment to "the pane grace and the margin".
- Modify `scripts/ptycheck.py`:
  - Delete `ESCAPEE_SLEEP`, `ESCAPEE_CMD`, `plant_escapee`, the `plant` parameter and branch in `run_check`, and the `--no-escapee` flag.
  - `tracked = list(shells) + [(srv, "wideboi server")]`.
  - Rewrite docstring assertion 3 (below).
- Modify `scripts/attachcheck.py`:
  - Delete `plant_escapee` and `case_server_reaps_a_reparented_escapee`, plus its `CASES` entry.
  - In `case_sigkilled_owner_takes_the_session_with_it`, drop the planting lines. Keep `kids = descendants(srv)`, the `still_alive([srv, *kids], 6.0)` check and the socket check, and change the "kill residual" comment to "the pane grace, with headroom". Its docstring loses "escapees included".
  - In `case_server_reaps_its_panes_on_signal`, drop the planting lines and keep the `srv.leaked` check.
  - Drop the `ps_rows` import if nothing else uses it.
- Modify `Makefile`: rewrite verify-exit comment item 3 (:109-112) to "Nothing wideboi spawned outlived it: its server and that server's pane shells. Teardown is the pty hangup, so a nohup'd job in a pane is deliberately not asserted on."

**Key changes:** `hangup.go` in full:

```go
package ptyx

import "time"

// Hangup ends the pane the way closing a terminal window does, and the
// way tmux does. It closes the pty master, so the kernel hangs up the
// terminal and sends SIGHUP to the shell and its foreground job. It
// then waits up to grace for the shell to exit.
//
// It signals nothing itself and walks no process table. A process that
// opted out of the hangup (nohup, disown, trap '' HUP, setsid) keeps
// running by design, as it would under tmux or after closing a real
// terminal. An interactive shell exits even if it ignores SIGHUP,
// because its next read from the hung-up terminal fails.
//
// Hangup is idempotent. A second call finds the master already closed
// and the root already reaped, and returns at once.
func (p *Pane) Hangup(grace time.Duration) {
	_ = p.Master.Close()
	p.waitForExit(grace)
}

// waitForExit observes the single reaper goroutine started in Spawn. It
// never calls Wait itself, so it is safe to call repeatedly.
func (p *Pane) waitForExit(within time.Duration) bool {
	select {
	case <-p.done:
		return true
	case <-time.After(within):
		return false
	}
}
```

`hangup_test.go` keeps `linkSleepAs`, `waitForProcess` and `processExists` verbatim (reap_test.go:175-209). It deletes `ppidOf` and every `TestKill*`, and adds:

```go
// The flipped test: teardown is the hangup, and a job that opted out
// of it survives by design. If this fails, something is reaping
// again -- read docs/LESSONS.md's Teardown section before "fixing" it.
func TestHangupLeavesANohupJobRunning(t *testing.T) {
	p, err := ptyx.Spawn([]string{"/bin/sh"}, 40, 10, t.TempDir())
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	tag := fmt.Sprintf("wideboi-nohup-%d", time.Now().UnixNano())
	cmd := fmt.Sprintf("%s && nohup ./%s 300 >/dev/null 2>&1 &\n", linkSleepAs(tag), tag)
	if _, err := io.WriteString(p.Master, cmd); err != nil {
		t.Fatalf("write to pty: %v", err)
	}
	if !waitForProcess(t, tag, true, 5*time.Second) {
		t.Fatal("nohup'd job never started")
	}
	t.Cleanup(func() { _ = exec.Command("pkill", "-f", tag).Run() })

	p.Hangup(2 * time.Second)

	select {
	case <-p.Done():
	default:
		t.Error("the shell outlived its terminal's hangup")
	}
	if !processExists(tag) {
		t.Fatal("a nohup'd job did not survive the hangup -- something is reaping again")
	}
}

func TestHangupEndsAnInteractiveShell(t *testing.T) {
	p, err := ptyx.Spawn([]string{"/bin/sh"}, 40, 10, t.TempDir())
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	p.Hangup(2 * time.Second)
	select {
	case <-p.Done():
	default:
		t.Fatal("an interactive shell outlived its terminal's hangup")
	}
}

func TestHangupIsIdempotent(t *testing.T) {
	p, err := ptyx.Spawn([]string{"/bin/sh"}, 40, 10, t.TempDir())
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	p.Hangup(testGrace)
	start := time.Now()
	p.Hangup(2 * time.Second)
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("second Hangup took %v; want an immediate return", d)
	}
}
```

The `pkill -f tag` cleanup matches only the test's own uniquely tagged binary name, following the session rule above.

**The red step comes before any rename.** Add the flipped test to `reap_test.go` first as `TestKillLeavesANohupJobRunning`, calling `_ = p.Kill(2 * time.Second)` in place of `p.Hangup(...)`. Run it, and see it fail with "a nohup'd job did not survive the hangup": today's `Kill` reaps it through the descendant walk. Only then do the rename and rewrite.

New ptycheck docstring assertion 3:

```
 3. Every process wideboi spawned before the signal is gone afterwards:
    the `wideboi server` it owns its session through, and that server's
    pane shells. The server and the panes are separate processes (#25),
    so this is also the check that killing the owning client ends the
    session rather than orphaning it. Teardown is the pty hangup, as in
    tmux, so a job that opted out of it (nohup, setsid) is deliberately
    not asserted on.
```

**Verification — automated:**
- [x] Red: `go test -count=1 -run TestKillLeavesANohupJobRunning ./internal/server/ptyx/` fails with "did not survive the hangup" before the rename. **Failed as expected: `reap_test.go:252: a nohup'd job did not survive the hangup`**
- [x] `grep -rn 'Descendants\|TTYMates\|KillResidual\|CloseResidual\|PGID\|\.Kill(' internal/ cmd/ | grep -v 'Process.Kill\|guard.go'` is empty. **Empty**
- [x] No `"ps"` or `"kill"` exec remains in non-test Go code: `grep -rn '"ps"\|"kill"' --include='*.go' internal cmd | grep -v _test.go` is empty. **Empty**
- [x] `go test -race -count=4 -run TestHangup -v ./internal/server/ptyx/` passes 12/12. **12/12, no tagged job left behind**
- [x] `make quick` passes. **Passes, after `make fmt` and the lifecycle test adaptation (see notes)**
- [x] `make check` passes four times. **4/4: exit 0, smoke 36/0, attach 17/0, verify-exit clean, no strays.** Record each run's result, and flag any failure other than the documented pre-existing stray
- [x] `go test -count=4 -run TestKillSessionShutsDownAServer ./cmd/wideboi/` passes 4/4 under the new 2.5s `shutdownCeiling`. **4/4**

**Verification — manual:**
- [x] **Les confirmed**: Les runs `./bin/wideboi`, opens a second pane, types `nohup sleep 999 &` in one, then quits with `ctrl+b q`. The panes close promptly and `pgrep -f 'sleep 999'` still finds the sleep. Les kills it by hand.

Commit: `Phase 2: ptyx hangs up instead of killing`

---

## Phase 3: Docs describe the hangup model; close out the issues

TDD opt-out: this phase changes docs and comments only.

**Files:**
- Modify `README.md:25-31`. Keep the paragraph, and change its tail after "the whole session is torn down with it" to: "Each pane's terminal is hung up, as tmux does, so the agents in it get SIGHUP and a forgotten `wideboi` never leaves them running where you cannot see them. A job you deliberately detached from the terminal (`nohup`, `disown`, `setsid`) keeps running, as it would after closing any terminal." Leave the "Once detached…" sentences as they are.
- Modify `docs/LESSONS.md:72-135`. Replace the whole "Teardown is the load-bearing guarantee" section with the text below.
- Modify `cmd/wideboi/main.go`:
  - :285-288 → "Without this, SIGTERM took Go's default disposition and srv.Close never ran, which left the socket file behind."
  - :399 → "the panes are then hung up before this process re-raises".
- Modify `internal/protocol/messages.go:163-165` → "MsgShutdown asks the server to end the session: hang up every pane, hang up on every client, and exit. The client hang-up is the acknowledgement, and it happens only after every pane has been hung up."

New LESSONS section:

```markdown
## Teardown is a hangup, as in tmux

Closing a pane closes its pty master (`ptyx.Hangup`), and the kernel does the
rest: SIGHUP to the shell and its foreground job, and an interactive shell
passes it on to its jobs. Nothing else is signalled and no process table is
read. A job that opted out of the hangup (`nohup`, `disown`, `trap '' HUP`,
`setsid`) keeps running, by design. That is tmux's model (`window.c` only
closes the fd) and every terminal emulator's. `TestHangupLeavesANohupJobRunning`
pins it. If it fails, something is reaping again.

Since #25 every session runs in a separate `wideboi server`, so the contract
crosses a process boundary: **the session's panes are hung up when the client
that started it ends, unless that client detached.** There are two routes:

- A signal to the owner sends `MsgShutdown` and waits for the server to hang
  up on it, which it does only after every pane is hung up. Then the owner
  restores the terminal and re-raises.
- An owner that dies without running any code (SIGKILL) is caught by the
  server: the owner's socketpair reaches EOF with no `MsgDetach` before it.

A detached, ownerless server lives until `kill-session`, a `q`, or a signal,
and it arms the same guard on that signal.

**Why we stopped hunting escapees.** Until September 2026 wideboi promised
more than tmux: nothing outlives the session, `nohup` or not. Keeping that
promise meant reconstructing, from `ps`, a relationship the kernel doesn't
keep. #83, #89, #88 and an attempt at #39 each plugged one hole and found the
next. Some holes can't be plugged at all: once a pane's shell exits, its
escapees have neither a parent chain nor a controlling tty leading back.
Then an uncommitted #39 attempt added a `ps` column that printed **blank**
inside `go test`. The fields shifted, and a test's `Close` SIGKILLed most of
the processes its developer had started that day. The guarantee was dropped
for the hangup model.

If process-table code ever comes back, one rule is the price of the lesson: a
`ps` parse that feeds a kill must be exact (a fixed field count, with any
other row dropped), must be probed from the process that will run it, and
must be dry-run before a test sends real signals to what it selects.
```

**Issue actions** (run at PR time, not before):
- #39: close as not planned, commenting "Won't fix: teardown moved to tmux's hangup model in <PR>. Escapees that opt out of the hangup survive by design; see LESSONS 'Teardown is a hangup'."
- #88 (already closed): comment "The TTYMates reaper from #97 was removed by design in <PR>: teardown is now the pty hangup, as in tmux."

**Verification — automated:**
- [x] `grep -rn 'escapee\|reap every pane\|TTYMates\|Descendants' README.md docs/LESSONS.md cmd internal scripts Makefile` finds nothing except the LESSONS history paragraph, which uses "escapees" on purpose. **Also turned up a stale `Server.Close` doc comment ("cleans up escapee processes"), now fixed. A second sweep for `ptyx.Kill` found `pane.go`'s Close comment quoting Kill's old doc, also fixed. Only the history paragraph remains**
- [x] `make quick` passes. **Passes**

**Verification — manual:**
- [ ] Les reads the new README paragraph and the LESSONS section

Commit: `Phase 3: docs describe the hangup model`

---

## After the phases

- Update the memory notes `server-close-final-poll-is-noop` and `kill-path-parsing-mass-kill`: the escapee machinery is gone, and the rule survives only in LESSONS.
- `/dev-session pr`.
