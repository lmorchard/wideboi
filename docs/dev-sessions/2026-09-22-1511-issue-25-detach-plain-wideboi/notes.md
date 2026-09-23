# Notes: issue #25, detach from plain wideboi

## Phase 1: shutdown end to end

**Red proof** (tests in place, binary unchanged apart from the `MsgShutdown` type):
- `server reaps its panes on signal`: FAIL. The planted `sleep 987653` survived SIGTERM, because `wideboi server` took Go's default disposition and never ran `srv.Close()`. That confirms the leak the research found.
- `kill-session ends the session`: FAIL on every assertion. The server stayed up, its descendants and the socket were left behind, and the attached client stayed.
- `kill-session without a server says so`: FAIL. The unknown subcommand fell through to starting a session and died with "failed to set terminal to raw mode".
- `TestShutdownClosesServerAndHangsUpEveryClient`: FAIL. `handleClientConnLoop did not return`, because `MsgShutdown` was ignored.
- `TestKillSessionShutsDownAServer`: did not compile (undefined `runKillSession`).

**Deviation, and why: attachcheck now pins SHELL/PS1 for its server.** The
new escapee plant flaked 1 run in 4. The diagnosis showed the server's panes
were running `/bin/zsh`, the developer's login shell:
- `Server()` is a plain `Popen` with the ambient env. ptylib's pin only covers processes started on a pty.
- A themed zsh prints no `$`, so the prompt wait always ran out its ceiling.
- ZLE sometimes discarded the text typed during startup.

Pinning `SHELL=/bin/sh` and `PS1="$ "` in `bin_env()` took the diagnostic to
12/12. Side effect: `attach-check` dropped from the ~60s the Makefile quotes
to ~33s, because that 60s was mostly zsh startup.

**Timing:** `make check` took 34.7s wall, parallel.

## Phase 2: explicit detach, and q really quits

**Red proof:**
- `TestDetachHangsUpOnlyThatClient`: `handleClientConnLoop did not return`, because MsgDetach fell through to `handleClientMsg` and was ignored.
- The transport roundtrip failed with `gob: type not registered for interface: protocol.MsgDetach`. That shows the registration test works.
- `quit from an attached client ends the session`: the server was still running, its descendants were alive, and the socket was left behind. `q` was detaching.

**Added beyond the plan:** `case_signalled_attached_client_restores_and_detaches`.
- The plan had this as a manual check. It's cheap to automate, so it's automated.
- Red proof: with only the attach client's `guard.Arm` disabled, it fails on "left the terminal in the alt screen". The death-by-SIGTERM assertion alone passes under the default disposition, the same weakness ptycheck's docstring describes.
- One slip: my first attempt disabled all three `Arm` calls with a sed, and the build failed on an unused import. The rerun disabled only the attach client's.

**`hungUp`** is written in this phase and read only by Phase 3's owner teardown.

## Phase 3: owned sessions

**Red proofs:**
- `TestOwnerEOFWithoutDetachEndsTheSession` and `TestOwnerDetachGivesUpOwnership` failed on their assertions, with `SetOwner` as a stub. `TestNonOwnerEOFLeavesTheSession` passed from the start, which is correct: it pins today's behaviour.
- Against the Phase 2 binary, the attachcheck owner cases `plain wideboi offers detach`, `... detaches and the session survives` and `SIGKILLed owner ...` all failed with "never spawned a server". `plain wideboi attaches to a running server` passed there too, which is correct: it pins existing behaviour.
- **The ptycheck red proof did not hold** (`[!]` in the plan):
  - *Handshake disabled, fallback kept:* ptycheck passes. The server's owner-EOF shutdown reaps all four tracked pids within the 2s `still_alive` window.
  - *Both disabled:* ptycheck fails. All four survive and the socket is left behind.
  - So the test guards the guarantee that matters (nothing outlives the owner), but not the handshake's ordering (reaped *before* the client dies).
  - Les chose to accept and record this over adding a tighter timing assertion.
  - The spec's reason for the handshake ("the 2s window gets tight" without it) turned out wrong. It is still worth having: the prompt returns only once the panes are dead, and a server that doesn't finish shutting down gets reported instead of ignored.

**Golden:** no diff. The startup wire summary is identical whether pane content comes from `srv.DrawPane` directly or from `MsgPaneUpdate` mirrors, so nothing was regenerated.

**Timing:**
- `make check` went from ~35s to ~51s. That's the six new attach-check cases, since attach-check runs serially.
- smoke is unchanged: 25s on both the Phase 2 and Phase 3 trees.
- verify-exit is ~3s.
- Along the way I found my own `plant_escapee` waiting for 2 prompts, which burned 5s per case. At 80x24 in the card layout only the focused pane's prompt is drawn, so it now waits for one.

**Not in the plan but required by it:** smoke's `pane_children` import became unused and was removed.

## Phase 4: dead render hooks and docs

- `Client.Draw`, `drawToScreenLocked` and `composeFrameLocked` lost their `drawPane`/`cursorInfo` parameters. 43 test call sites were updated, all of which passed `nil`: 38 by regex and 5 in motion_test.go that used an inline `newFakeHostScreen(...)` argument the regex didn't match.
- `Server.DrawPane` and `Server.CursorInfo` are deleted. The comments that cited "DrawPane's precedent" now cite PaneSize, or state the reason directly.
- README has a detach/reattach/kill-session section, and the `d`/`q` rows are updated.
- LESSONS: the teardown section restates the contract across the process boundary, including the finding that ptycheck can't see the handshake. There's a new entry: the env pin only covers pty spawns.
- The Makefile attach-check comment is rewritten, because smoke is no longer blind to the wire.
- The `make check` timing in README and CLAUDE.md was "~12s", already stale before this branch (~35s). It now says ~50s.

## PR: rebase and self-review

**Rebase** onto origin/main picked up #72/#75, #76, #73, #77/#79 and #78/#34/#33 (mouse, OSC 52, present-only-changed-frames). Conflicts:
- in `runAttach`/`run`: `present()` replaced `dirtyFrames`; mouse handling
- in `Draw` call sites: 9 new ones in mouse_test/cards_test
- in `drawSelectionLocked`
- in the smoke/attachcheck imports

Main's in-process `run` guarded `writeClipboard` with `!stopped`, and that guard moved into `runClient`.

**Review** (fresh-context subagent, same model, per LESSONS). Fixed:
- *Medium: a server that was shutting down still accepted connections and spawned panes.* A `wideboi` run during the ~2s reap attached to the dying server, spawned two shells nobody would reap, then flashed and exited.
  - Fix: `Close` shuts the listener first, `spawnPaneLocked` refuses once Close has begun, and a connection accepted in the gap is closed rather than registered.
  - `stopCh` is now closed under `s.mu`, so it's ordered against the snapshot.
  - Tests: `TestClosedServerSpawnsNoPanes` (red: 3 panes spawned) and `TestCloseStopsListeningBeforeReaping` (red: still accepting 1s into the reap). The second needed `os.MkdirTemp`, because `t.TempDir()` with that test name exceeds darwin's 104-byte socket path limit.
- *Low: exit status could race the re-raise* when a signal landed during a `q`/`d` hang-up, and in `runServer` when Run returned on the guard's own Close. Fix: an `awaitReRaise` step before those returns, and a `signalled` flag in runServer. Not covered by a test: the race is timing-only.
- *Low: a timed-out `q` doubled the wait*, because the guard sent a second shutdown. Fix: `hungUp` is set whether or not the shutdown was acknowledged.
- *Low: `hangUp`'s ceiling didn't cover the send.* Fix: a `context.WithTimeout` over both. Test: `TestHangUpCeilingCoversTheSend` (red: blocked 2s).
- *Test gap: nothing checked "hang up only after reaping".* The attachcheck `q` and kill-session cases now require the panes gone within `REAPED_BY_ACK = 0.3s` of the acknowledgement. Red: with Close's hang-up moved above the reap, both fail. Stable over 4 runs.

Skipped:
- *Concurrent plain launches: the loser errors instead of attaching.* The spec chose no retry deliberately (#27 territory).
- *The server guard has no ceiling.* Every path under it is bounded today, so this is defence-in-depth only.

`make check` ×4 green after the fixes, ~54s each: smoke 33/33, attach 17/17.

## Les's manual pass, and the detach notice

Les confirmed that attach, detach and several clients at once all work. His nit: detaching gave no sign that wideboi was still running, or where. So a detach now prints, after the terminal is restored (it has to come after, or the alt-screen exit wipes it):

    [wideboi detached; the session is still running at <socket>]
    [reattach: wideboi   end it: wideboi kill-session]

The commands include `-s <socket>` only when the socket isn't `config.DefaultSocketPath()`.

**Red proof:** both attachcheck detach cases failed with "no notice". A slip along the way: the first implementation checked `stopped` *after* `guard.Stop()`, whose teardown sets it, so the owner case stayed red. It now samples `stopped` before the Stop.

**Tests:**
- `TestDetachNoticeNamesTheSocketOnlyWhenNeeded` covers the `-s` branch, which attachcheck's private socket never reaches.
- Detach cases: 4× green. `make check`: green.

## Copilot review (against ca77a90)

The first `--add-reviewer` looked like a no-op (`reviewRequests` stayed empty), but the timeline shows the request did register. The review landed at 00:27, and my poll had missed it.

Fixed:
- **A stalled client could wedge the server's exit** (High).
  - `ServerSocketConn.SendServer` blocked on a full queue, and closing the conn didn't release it. `Run` could hang in a broadcast after `Close` finished, so the server lingered after kill-session. Before this PR, `wideboi server` never exited on its own, so it never showed.
  - Fix: a `closed` channel. `Close` shuts it, `SendServer` selects on it, and the write pump calls `Close` when it dies.
  - Test: `TestServerSendReturnsOnceClosed` (red: stayed blocked).
- **`hangUp` counted a protocol failure as an acknowledgement.** It now requires `conn.Err() == nil`.
- **User arguments could override `--owner-fd`.**
  - Copilot's suggested order (ours last) is itself a trap. I probed it: `parseCLI(["server","foo","--owner-fd","3"])` gives ownerFD -1, because the flag parser stops at the first non-flag. The server would start ownerless, and nobody would read the socketpair.
  - Fix: `serverArgs` puts ours first and drops any user-supplied `-owner-fd`/`--owner-fd`, spaced or `=`.
  - Test: `TestServerArgsAlwaysNameOurOwnerFD`. Red: 4 of 7 cases fail with the filter removed.

Skipped:
- **A signal teardown racing a queued `C-b d`** (High). Two explicit intents arrive in the same instant, and honouring the detach is defensible. A `stopped` gate only narrows the window. The leak guarantee is about unattended death, which the EOF route still covers.
- **The fixed 0.4s in `Client.quit()`.** It mirrors the existing `detach()` helper.

`make check` ×4 green (~54s).
