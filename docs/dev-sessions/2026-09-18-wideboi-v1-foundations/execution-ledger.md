# wideboi Plan 1 — execution ledger

> Promoted verbatim from the subagent-driven-development scratch workspace, which
> is gitignored and deleted at session end. 26 rulings and 29 deferred/parked
> findings recorded during execution. Plan 2 should read the parked items.


Spec: docs/dev-sessions/2026-09-18-wideboi-v1-foundations/spec.md (read, authoritative)
Branch: plan-1-foundations
Merge base: 0580f70cc4b34e69e1fd9076c01945a5fc56815c

Ruling: work on branch `plan-1-foundations` in the primary checkout rather than a
git worktree — greenfield repo whose main holds only doc commits, no parallel work
to isolate from, and Tasks 3/6/10 need Les to run the binary interactively, which
is simpler in his primary checkout. Branch isolation satisfies the never-on-main
rule. Cost if wrong: trivial, the branch can move to a worktree at any point.

Note: no TodoWrite tool in this session. This ledger is the sole progress record.

## Pre-flight conflict scan

### Pairs sharing a file or interface

| Tasks | Produces → Consumes | Finding |
|---|---|---|
| T1 → T2 | `internal/hostterm/guard.go`; T1 `NewGuard`/`Stop`, T2 adds `Arm` | Clean. T1 test is `package hostterm`, T2 test is `package hostterm_test` — Go permits both in one dir. |
| T2 → T3 | `Guard.Arm`, `Guard.Stop` | Clean. |
| T3 → T6 | both write `cmd/wideboi/main.go` | Clean. T6 replaces `run()` wholesale and supplies a full import block; it drops `uv` and adds `golang.org/x/term`. Intentional (passthrough milestone). |
| T6 → T10 | both write `cmd/wideboi/main.go` | Clean. T10 replaces `run()` again, returns to `uv`, drops `x/term`. T10 Step 6 runs `make tidy` to shed the now-unused dep. Verified T10's import list matches its body: `fmt image log os sync syscall time uv client compose hostterm`, all used. |
| T4 → T5 | `ptyx.Pane`; T5's `Kill` observes T4's `done` channel | Clean after plan self-review fix. T4 now starts the single reaper goroutine; T5's `waitForExit` selects on `p.done` and never calls `Wait` itself. |
| T5 → T6, T10 | `(*Pane).Kill` inside the shutdown guard | Clean. |
| T7 → T8 | `compose.NewSurface`, `compose.Text` in term tests | Clean. |
| T7 → T10 | `compose.Surface`, `NewSurface`, `Blit`, `WriteString` | Clean. `*uv.TerminalScreen` satisfies `uv.Screen`, verified by compile check. |
| T8 → T9 | `term.NewVT` | Clean. |
| T8 → T10 | `term.Grid` inside `client.Pane` | Clean after plan self-review fix. `Grid` now carries `Read` and `SendKey`, which T10's two-goroutine pump and key forwarding require. |
| T9 → spec.md | rewrites the reflow bullet under Open questions | Clean. Target text exists verbatim. |

### Per-task self-consistency

| Task | Tests vs code, files created vs later touched | Finding |
|---|---|---|
| T1 | guard_test.go vs guard.go | Clean. `errSentinel` now declared inside the test file (self-review fix). |
| T2 | re-exec child test vs `Arm` | Clean. External test package imports `hostterm` explicitly. |
| T3 | no unit test; manual steps only | Acceptable — its deliverable is exit paths, already covered by T1/T2 tests. |
| T4 | asserts `PGID == pid`; `Setsid` makes that true | Clean. |
| T5 | `Descendants` deepest-first; `signalTree` consumes it | Clean. |
| T6 | no unit test; manual steps | Acceptable. |
| T7 | `WriteString(src, ...)` where src is `Surface` | Clean. `Surface` = `uv.ScreenBuffer` satisfies `uv.Screen`. |
| T8 | `Size() (int, int)` impl vs `Size() (cols, rows int)` iface | Clean. Go matches signatures by type, not parameter name. |
| T9 | probe test with two recorded outcomes | Clean. Not a placeholder — a decision point with both branches specified. |
| T10 | `dirty atomic.Bool` set by reader, swapped by `Surface()` | Clean. |

### Findings carried forward (minor, not blocking)

- Task 4 minor (deferred): `TestSpawn*` cleanups call `p.Close()`, which closes the
  PTY master but does not kill the child. The shell should exit on SIGHUP when the
  master closes, but the tests do not assert it, and `readUntil`'s reader goroutine
  outlives the test. Test hygiene only — Task 5 introduces `Kill` and asserts
  teardown properly. Flag to the final review.
- Task 3 minor (deferred): writes key names to stderr while in the alt screen, so
  output renders oddly. Deliberate debug affordance for a throwaway milestone;
  Task 6 removes it.

Scan result: no blocking conflicts. No pre-flight rulings required beyond the
branch/worktree ruling above.

## Progress

Task 1: dispatched (implementer, haiku — plan text carries complete code, so
transcription + testing). BASE 0580f70. Briefs for tasks 2-10 pre-generated.
Task 1: implementer DONE (commit 0f9308e, 3/3 passing). Review package
review-0580f70..0f9308e.diff (5 files, +173). Task reviewer dispatched (sonnet).
Task 1: review clean — spec compliant, quality approved.
  Resolved the reviewer's one cannot-verify item myself: `make check` exits 0
  (gofmt clean, go vet clean, 3/3 tests pass), working tree unchanged after fmt.
Task 1: minor (deferred): task-1-report.md:91 overstates scope, claiming an "Arm
  placeholder" that does not exist. Report-accuracy nit, not a code defect.
Task 1: PLAN DEFECT FOUND (carry to Task 2): the plan's Task 1 Step 5 import block
  listed `os`, `os/signal`, `syscall` — unused until Task 2 adds `Arm`, so the
  code as written would not compile. The implementer correctly dropped them.
  Ruling: the implementer's deviation stands; Task 2 must re-add those three
  imports when it implements `Arm`. Cost if wrong: a compile error in Task 2,
  caught immediately by its own test run.
Task 1: complete (commits 0580f70..0f9308e, review clean)
Task 2: implementer DONE (commit a90d347, 4/4 passing, re-ran new test 3x for
  flakiness). Task reviewer dispatched (sonnet).
Task 2: review clean — spec compliant, quality approved. Reviewer checked four
  named concurrency/lifecycle risks (re-raise racing shutdown, Stop/Reset order,
  test hang-vs-flake, goroutine leak); none are defects.
  Resolved the reviewer's cannot-verify item: "how Arm composes with caller-side
  defer Stop() on non-signal paths" is Task 3's wiring by design, not a Task 2 gap.
Task 2: minor (deferred): guard.go:63-64 signal.Stop+signal.Reset is redundant;
  Reset alone suffices. Plan-mandated text, harmless.
Task 2: minor (deferred): guard.go:61 discards the shutdown error in the signal
  goroutine, hiding a failed terminal restore. Plan-mandated. Worth revisiting if
  restore-failure visibility ever matters.
Task 2: complete (commits 0f9308e..a90d347, review clean)

Ruling: Task 3's Steps 3-4 are interactive manual verification (run the binary,
  press keys, signal it from another terminal) and cannot be done by a subagent
  with no TTY. I will not block the pipeline on them. The implementer does Steps
  1-2 and 5 (write, build, commit); the manual steps are batched for Les. This is
  safe because Task 3's only output is cmd/wideboi/main.go, consumed solely by
  Task 6, which replaces run() wholesale. Tasks 4, 5, 7, 8, 9 touch entirely
  different files and proceed regardless.
  Cost if wrong: a latent bug in a throwaway milestone binary that Task 6 rewrites
  anyway; caught by Les's verification whenever he runs it.
Task 3: implementer DONE (commit d1cc464). Build clean, go vet clean, gofmt clean.
  No unit tests by design. Steps 3-4 deferred to Les per the ruling above.
  Task reviewer dispatched (sonnet).
Ruling: keep task dispatches strictly sequential rather than overlapping Task 4's
  implementer with Task 3's reviewer. Their files are disjoint, but both commit to
  the same branch, and interleaved commits would corrupt the BASE..HEAD ranges the
  review packages and any fix loop depend on. Ledger integrity outranks wall-clock.
  Cost if wrong: a slower run, nothing else.
Task 3: review clean — spec compliant, quality approved. Reviewer verified the
  named risks against ultraviolet's source: LIFO defer ordering restores the
  terminal before a panic propagates (second Stop() is a harmless no-op thanks to
  idempotency); EnterAltScreen only buffers, so a failed t.Start() never wrote to
  the device and needs no cleanup.
  Its ⚠️ is Steps 3-4, already ruled as deferred to Les.
Task 3: minor (deferred): main.go:46-49 double-flushes — the guard calls
  ExitAltScreen+Flush and t.Stop() also calls Reset+Flush. Plan-mandated, harmless.
Task 3: minor (deferred): go.mod still marks ultraviolet `// indirect` though it is
  now directly imported. Self-resolves at Task 10, which runs `make tidy`.
Task 3: complete (commits a90d347..d1cc464, review clean)
  MANUAL VERIFICATION OWED BY LES: brief Steps 3-4 (happy path + SIGTERM -> 143).
Task 4: implementer DONE (commit b572e5e, 3/3 passing, also -race x3 clean).
  Task reviewer dispatched (sonnet).
Task 4: review clean — spec compliant, quality approved. Reviewer cleared three
  named risks against creack/pty source: Spawn's error paths close the master and
  tty (no leak); exactly one reaper goroutine, single unconditional close(done);
  and it empirically verified that os/exec dedupes cmd.Env keeping the LAST
  occurrence, so the TERM override actually works.
  Resolved its cannot-verify item: `make check` exits 0 repo-wide, and `ps` shows
  zero stray /bin/sh after the suite — which also settles its third minor.
Task 4: minor (deferred): pane.go:63-66,92-96 convert cols/rows to uint16 with no
  bounds check; a negative wraps to 65535, zero passes through. Plan-inherited.
  Real but low-likelihood. Flag to final review.
Task 4: minor (deferred): pane.go:58-61 sets Setsid/Setctty explicitly, which
  creack/pty's StartWithSize already forces unconditionally. Harmless; arguably
  useful as in-code documentation of intent.
Task 4: complete (commits d1cc464..b572e5e, review clean)

Ruling: `bin/` is produced by `make build` but is untracked and absent from
  .gitignore, so it will pollute `git status` for the rest of the plan and could
  be committed by accident. Folding the one-line .gitignore fix into Task 5's
  dispatch rather than fixing it in the controller session, so it still passes
  through review. Cost if wrong: trivial and self-evident in the diff.
Task 5: implementer DONE (commit 6a8e41a, 5/5 passing incl. the escaped-grandchild
  test; .gitignore bin/ included). pane.go needed no edits. Reviewer dispatched.
Task 5: review returned spec ✅, quality CHANGES REQUESTED — 3 Important, 4 Minor.
  All are defects in the PLAN's code, transcribed faithfully. Reviewer verified
  the Wait invariant, deepest-first ordering, ps parsing, signal-to-self guard,
  and the reaper/Master.Close race all hold. TDD evidence confirmed non-fabricated
  by matching RED line numbers to committed call sites.

Ruling: Important #1 (a SIGTERM-ignoring escapee survives Kill entirely, because
  escalation is decided solely on the root's exit) is REAL and LOAD-BEARING. The
  spec's binding text is "A multiplexer that leaks background processes is worse
  than useless" and the global constraint "every exit path must reap child
  processes". My plan's code contradicts the spec; the spec wins. FIX.
  Evidence: reviewer demonstrated empirically that an interactive /bin/sh on a pty
  ignores SIGTERM, so the suite only reaches the SIGKILL path by accident.
  Cost if wrong: the fix is strictly more aggressive teardown; worst case a pane
  dies harder than necessary.

Ruling: Important #2 (repeated Kill signals an already-reaped, possibly recycled
  pid — blast radius SIGKILL to an unrelated process group) is REAL. Not addressed
  by the spec, but SIGKILLing a bystander process group is far worse than any leak
  it prevents. FIX via a p.done short-circuit.
  Cost if wrong: a second Kill becomes a no-op, which is already the intent.

Ruling: Importants #1 and #2 conflict as written — #2 returns early once the root
  is reaped, which would skip #1's escalation. Resolving both by capturing the
  Descendants snapshot ONCE at the top of Kill while the root is still alive, and
  escalating against that snapshot rather than re-walking ps. This is a design
  improvement over the plan, not merely a patch.

Ruling: Important #3 (Descendants has no pid<=0 guard and walk has no visited set)
  is unreachable today but the blast radius on Linux is "SIGKILL the machine" for
  a four-line omission in an exported kill-path primitive. FIX.

Ruling: Minor #1 (Kill's error return is always nil and carries no information) is
  normally deferred, but it is entangled with the #1 fix — the whole point of the
  change is that teardown either succeeded or did not. Folding it into this round.
  Cost if wrong: one more error path a caller may ignore.

Task 5: minor (deferred): reap.go:72 deepest-first rationale is overclaimed —
  ordering narrows the respawn race rather than closing it. Comment only.
Task 5: minor (deferred): reap.go:13-15 Descendants doc overstates reach; a
  double-forked daemon reparented to pid 1 is not a descendant and no route finds
  it. Worth one clause naming the known limit.
Task 5: minor (deferred): reap.go:73,81 unguarded p.Cmd.Process deref; Spawn is the
  only constructor so unreachable today.

Task 5: CARRY TO TASK 10: because an interactive shell root ignores SIGTERM, every
  pane teardown burns the full grace period. Task 10 kills two panes, so a serial
  teardown costs 2x grace on quit. Task 10 should kill panes concurrently.
Task 5: CARRY FORWARD: Kill always closes Master, which unblocks any reader
  goroutine a later task attaches. Implicit cross-task contract; Task 10 relies on
  it for its PTY-reader goroutine to exit.
Task 5: fix round 1/5 dispatched (resumed original implementer, context intact).
  4 findings sent: 3 Important + 1 entangled Minor, with the combined snapshot-once
  design. Required new test: escapee with `trap '' TERM` must still be reaped.
  FIX_BASE for the scoped re-review = 6a8e41a.
Task 5: fix round 1/5 (4 addressed, 2 open — non-discriminating regression test;
  prescribed-step-3 freed-pid signal; commits 6a8e41a..b164e1e)
  Re-reviewer verdicted all 4 findings ADDRESSED with file:line evidence, and
  independently validated the Master.Close() reordering: SIGHUP reaches only the
  foreground group, every member of which is already SIGKILLed, so it cannot mask
  a failed escapee kill; close happens exactly once per call on both paths; the
  report's own timing delta (3.30s -> 2.32s) corroborates the wedge hypothesis.

Ruling: the required `trap '' TERM` test does NOT verify Finding 1 and must be
  fixed. Proven, not asserted: the re-reviewer forked /bin/sh on a pty and showed
  it does not exit within 2.5s of SIGTERM, so the test's root is still alive at
  grace expiry and the ORIGINAL code would have reached the escapee via its ps
  re-walk. The implementer's claim at task-5-report.md:149 is false. I required
  this test specifically because the fix is otherwise unverified; accepting a test
  that passes pre-fix would be accepting no test at all. FIX in round 2.
  Cost if wrong: none — a discriminating test is strictly more coverage.

Ruling: New Breakage 1 is mine, not the implementer's — my step 3 said "SIGKILL
  the snapshot, then the pgid, then the root, regardless of whether the root
  exited," which reintroduces Finding 2's blast radius (SIGKILL to a recycled
  pid's group) inside a single call when the root exits during grace. The
  re-reviewer labelled it Minor, but I rated this same hazard Important as
  Finding 2 and the severity belongs to the blast radius, not the window width.
  Amending my own prescription: keep the SNAPSHOT SIGKILL unconditional (that is
  what preserves the anti-leak guarantee, since escapees live in the snapshot),
  but gate the -PGID and root-pid SIGKILL on `waitForExit(grace) == false`.
  Cost if wrong: a pane whose root exited cleanly gets one less redundant signal.

Task 5: parked — New Breakage 2: the p.done short-circuit is itself a leak path.
  If the root exits on its own before Kill is called (user types `exit` after
  backgrounding a nohup'd job), escapees are never signalled and Kill reports
  success. Real, reachable in Task 10, and the same leak shape as Finding 1 from
  the other direction. Ruling: park for Plan 1. Closing it needs a descendant
  snapshot maintained while the root is alive, which this design does not keep and
  cannot bolt on without racing the reaper. Plan 2 gives the server ownership of
  pane lifecycle and is where this belongs. CARRY TO PLAN 2 — record in the spec.
Task 5: parked — New Breakage 3: processes forked after the snapshot are invisible
  to both the kill and the error return. Inherent to the snapshot-once design I
  chose. Ruling: accepted tradeoff, on the record.
Task 5: parked — New Breakage 4: anyAlive treats zombies and EPERM as alive, so a
  pid recycled inside the 500ms window can make Kill report a false survival.
  Conservative in the safe direction. Accepted.
Task 5: minor (deferred): reap.go:81-86 states the macOS exit-teardown wedge as an
  unconditional property; it is conditional on an undrained master. Folding the
  one-clause doc fix into round 2 since the file is open.
Task 5: parked — out of scope: Kill has no mutual exclusion; two concurrent callers
  both pass the short-circuit. Pre-existing, no caller exists yet.
Task 5: fix round 2/5 dispatched (resumed original implementer). Scope: make the
  regression test discriminate (root must exit within grace); gate the pgid/root
  SIGKILL on rootExited per my amended prescription; one-clause doc fix on the
  macOS wedge; correct the false claim at task-5-report.md:149.
  FIX_BASE for round 2's scoped re-review = b164e1e.
Task 5: round-2 fix committed 60afabd. signalTree split into signalDescendants
  (unconditional) + signalRoot (gated on rootExited). New test
  TestKillEscalatesEvenWhenRootExitsWithinGrace with a non-interactive root; the
  escapee traps HUP as well as TERM so the kernel's session-exit SIGHUP broadcast
  cannot kill it for free — implementer's own catch, and the thing that makes the
  test genuinely discriminate. Discrimination proved by reintroducing the pre-fix
  early return in scratch and observing RED. 7/7 passing. Scoped re-review
  dispatched (opus).
Task 5: fix round 2/5 (3 addressed, 0 open; commits b164e1e..60afabd)
  Re-reviewer independently verified rather than trusting the report: forked
  /bin/sh -c to confirm a non-interactive root dies on SIGTERM in ~200ms; probed
  that `trap "" TERM HUP` survives exec; confirmed the snapshot SIGKILL at
  reap.go:120 sits OUTSIDE the `if !rootExited` block, so the anti-leak guarantee
  survives the gating; confirmed the checkout is clean of the scratch experiment.
  It also corrected its own earlier reasoning: round-1's test was not weaker than
  diagnosed — that escapee had its own pgid from job control, so foreground-group
  SIGHUP would not have reached it. Round 1's sole defect was the root not dying.
  Honest note against my own ruling: on Darwin a pgid with living members is not
  eligible for pid reuse, so my amended gating is conservative rather than
  strictly necessary. Harmless, but I over-corrected.
Task 5: minor (deferred): reap.go:83 doc sentence ("SIGKILL against an already-dead
  snapshot pid is a harmless ESRCH") now contradicts the recycling rationale two
  bullets above it. Text only.
Task 5: minor (deferred): the rootExited==true fast path lost ~1s of reaping slack
  before the 500ms anyAlive poll. Flake-under-load vector, not a defect.
Task 5: complete (commits d1cc464..60afabd, 5 parked, 2 fix rounds)

Ruling: PLAN DEFECT in Task 6 — its Step 1 says `go get golang.org/x/term &&
  go mod tidy`, which contradicts the global constraint "do not run go mod tidy".
  Verified the consequence rather than assuming it: x/vt is pinned at
  v0.0.0-20260913004009-c615ff2f7805 but marked `// indirect` and imported by no
  code until Task 8, so tidy at Task 6 would EVICT it, and Task 8 would re-add it
  — plausibly at @latest, silently breaking the pin that exists because x/vt has
  no tagged release. The global constraint is binding; the step text is not.
  Task 6 runs `go get golang.org/x/term` ONLY. Task 10 already owns the single
  `make tidy`, by which point every dependency has a real importer.
  Cost if wrong: go.mod carries one unused require line for four more tasks.
Task 6: implementer DONE (commit 88f64ba). go get x/term only, no tidy; x/vt pin
  verified intact. Build/vet/gofmt/test clean. Steps 3-4 deferred to Les.
  Task reviewer dispatched (sonnet).
Task 6: review clean — spec compliant, quality approved. Reviewer confirmed both
  controller overrides were honoured (x/vt pin untouched at go.mod:12; interactive
  steps deferred rather than fabricated) and that x/sys 0.47->0.48 plus new
  x/term v0.46.0 are ordinary MVS results of the go get.
  It traced two safety properties rather than asserting them: dropping the
  panic-recover wrapper is NOT a regression (Go runs pending defers during panic
  unwind regardless of recover, so defer guard.Stop() still restores), and the
  unjoined stdin goroutine cannot leak (process exit takes it).
  Resolved both cannot-verify items: SIGWINCH is out of this task's scope and is
  already carried to Task 10 / Plan 2; Steps 3-4 are the standing manual debt.
Task 6: minor (deferred): main.go:56 unjoined stdin goroutine deserves the same
  explanatory comment its sibling on line 57 has.
Task 6: complete (commits 60afabd..88f64ba, review clean)
  MANUAL VERIFICATION OWED BY LES: brief Steps 3-4 (shell/vim/top, leak probe).
Task 7: implementer DONE_WITH_CONCERNS (commit 5ad96e1, 3/3 passing). It deviated
  from "verbatim" by changing ONE character of a test's expected value, and
  flagged it rather than doing it silently.

Ruling: the deviation is CORRECT and approved. I verified it independently with a
  standalone probe against the real library rather than accepting the explanation,
  because a changed test assertion is the most sensitive kind of deviation.
  Result: blit1 Rect(0,0,6,1) -> "UUUUUU  "; blit2 Rect(2,0,5,1) covers x in [2,5)
  = cols 2,3,4 only, so col 5 retains blit1's 'U' -> "UUOOOU  ". My plan's literal
  "UUOOO   " was a one-character counting error on my part. The implementer's
  "UUOOOU  " is what correct painter's-algorithm compositing produces.
  Cost if wrong: none — verified empirically twice, by the implementer and by me.

Task 7: PLAN GAP CLOSED: clipping — the behaviour I admitted I had never actually
  exercised — works as the design assumes. TestBlitClipsToDestination passed as
  written, and the implementer confirmed by reading ultraviolet buffer.go:422 that
  Draw only iterates inside the dest rectangle and never resizes or reads past it.
  This is load-bearing: panes wider than the viewport must be cropped, not
  resized, and that now has evidence behind it rather than an assumption.
Task 7: task reviewer dispatched (sonnet).
Task 7: review clean — spec compliant, quality approved. Reviewer independently
  re-derived the approved deviation from ultraviolet source (Draw subtracts
  area.Min and only touches coords inside area) rather than trusting either my
  probe or the implementer's trace, and confirmed no OTHER assertion was altered.
  It also verified out-of-bounds WriteString is safe: Buffer.SetCell no-ops on a
  bad y, Line.Set no-ops on a bad x — silent drop, no panic.
  Explained why `Surface = uv.ScreenBuffer` must be an ALIAS: a defined type would
  not inherit ScreenBuffer's value-receiver WidthMethod(), so it would stop
  satisfying uv.Screen without hand-written forwarding.
Task 7: minor (deferred): surface_test.go:129 cites a .superpowers artifact that
  may not persist; the rationale is already restated inline, so it's cosmetic.
Task 7: CARRY TO TASK 10 (both plan-inherited, confirmed real):
  - compose.Text ignores Cell.Width; a multi-rune grapheme cluster silently loses
    everything past the first rune, so a snapshot test could pass while
    misrepresenting the real screen.
  - compose.WriteString writes one cell per rune and always advances x by 1,
    bypassing Buffer.Set's wide-cell placeholder bookkeeping.
  => Task 10 must stick to single-width glyphs in chrome. Its divider is U+2502
     and its status line is ASCII, so it is within that limit as planned.
Task 7: complete (commits 88f64ba..5ad96e1, review clean)
Task 8: implementer DONE (commit 0626527). 4 behaviour tests + 7 key-encoding
  subtests green on first run against the real x/vt; no assertions weakened.
  Task reviewer dispatched (sonnet).
Task 8: review clean — spec compliant, quality approved, zero findings against the
  implementer. Reviewer re-ran the suite under -race itself, checked all seven key
  encodings character-by-character against the measured values (no softened
  assertions), and confirmed go.mod/go.sum untouched with the x/vt pin intact.
  Verified Grid has not accreted toward x/vt's wider Terminal interface: exactly
  six methods, every one exercised by a test. Confirmed Size() reads through to
  the emulator rather than a cached field, so TestGridHonoursResize proves real
  behaviour.
Task 8: minor (deferred): the brief's drain goroutine in grid_test.go:161-171
  sends nothing if Read ever returns (0, nil), parking that goroutine for the
  process lifetime on that one failure mode. Plan artifact, latent, not a blocker.
Task 8: complete (commits 5ad96e1..0626527, review clean)
Task 9: PROBE RESULT — x/vt does NOT reflow. Commit 02ceb3c. Both tests red,
  marked t.Skip with assertions intact, spec updated with the "truncates" variant.
  I verified independently rather than relaying the subagent's conclusion, since
  this decides the emulator. My own probe confirmed BOTH findings and separated
  them cleanly:
    DEFECT A (reflow): uv.Buffer.Resize hard-truncates each line on narrow
      (b.Lines[i] = b.Lines[i][:width]); widening pads with blanks. Data is
      permanently destroyed. direct CellAt after narrow = "abcdefghij", after
      widen = "abcdefghij          ". Exactly gwae's ADR-004 defect.
    DEFECT B (Touched reset): Emulator.Draw paints only e.Touched() lines, and
      Screen.Resize sets Touched = nil rather than all-dirty. So Draw renders a
      FULLY BLANK screen after any resize until later output touches each line.
      Confirmed: drawn row is blank after narrow AND after widen, then returns to
      "abcdefghij          " once a subsequent write lands.
  Defect B is separate from, and more immediately visible than, Defect A. It is
  also cheap to fix in our own adapter (force full redraw after Resize).
Task 9: STOPPING to put the emulator decision to Les, as committed when he chose
  this execution mode. Task 10 does not resize panes, so it is unaffected and
  could proceed in parallel with that decision.
Task 9: LES'S DECISION (not my ruling): keep x/vt, revisit the emulator in Plan 2
  when width-cycling actually exercises reflow; proceed with Task 10 now.
  Rationale: nothing in Plan 1 resizes a pane, the Grid interface keeps the swap
  cheap, and the Go ecosystem has no drop-in replacement, so "swap" would really
  mean owning the grid ourselves — a Plan 2-sized decision best made with the
  layout core in hand.
  => CARRY TO PLAN 2 SPEC: both defects, the evidence, and the four options.
Task 9: complete (commits 0626527..02ceb3c, probe answered, decision taken by Les)
Task 10: implementer DONE (commit 458b07a). make build + make check green. tidy
  run once as designed: x/vt pin unchanged, // indirect dropped (now a real
  importer via internal/server/term); ultraviolet and creack/pty promoted to
  direct; golang.org/x/term dropped. Concurrent teardown correction applied.
  Steps 4-5 deferred to Les. Task reviewer dispatched (sonnet).
Task 10: review returned spec ✅, quality CHANGES REQUESTED — 3 Important, 2 Minor.
  All three Importants are defects in MY brief's template, not implementer work.
  Reviewer verified clean, with evidence: two-goroutine pump correct; SendKey used
  everywhere and .String() nowhere; the dirty/Surface race is self-correcting (a
  Write landing between Swap and Draw is always followed by Store(true) in the
  same goroutine, so a redraw is deferred one tick, never lost) and Write/Draw are
  mutually exclusive under SafeEmulator's mutex; onExit cannot double-close quit
  (shared sync.Once); zero-size panes degrade to empty allocations rather than
  panicking, checked against pinned ultraviolet source.

Ruling: Important #1 (pane-spawn failure leaks the terminal AND an already-spawned
  child) is REAL and LOAD-BEARING. It violates the global constraint "every exit
  path must restore the host terminal and reap child processes" AND the plan's own
  done-criterion "No process survives any exit path." My template built the guard
  only after both spawns, so the second spawn failing strands pane 0's shell and
  leaves the host in alt-screen raw mode. FIX.
  Cost if wrong: none; the fix only adds cleanup on a path that currently has none.

Ruling: Important #2 (signal teardown races the frame loop on scr/t) is REAL.
  Practical impact is small because the signal is re-raised immediately, but it is
  a genuine unsynchronised race between the guard's goroutine and the render loop,
  and the specific failure mode is a corrupted terminal restore — precisely the
  outcome this project treats as a hard guarantee. FIX, with the minimal correct
  change rather than a redesign.
  Cost if wrong: slightly more shutdown sequencing than strictly needed.

Ruling: Important #3 (emulator->PTY pump can never exit; Grid has no Close) is
  real but bounded by process lifetime in Plan 1 — the leak is invisible today and
  Step 5's ps check will still read 0. I am fixing it anyway rather than parking
  it, because the fix is ~6 lines (Grid gains Close, vtGrid delegates to
  Emulator.Close, Pane.Close calls it), it makes Pane.Close actually complete, and
  Plan 2's per-pane teardown needs it regardless. Widening Grid by one method is
  justified: Grid owns a resource, so Close belongs on it.
  Cost if wrong: one more method on an interface I deliberately kept narrow.

Task 10: minor (deferred): pane.go:113 and main.go:73 discard ptyx.Kill's error,
  which is exactly the "tree survived SIGKILL" signal Task 5 added. Self-defeating,
  but logging during screen teardown is awkward; wants a real diagnostic path.
Task 10: minor (deferred): recover() at pane.go:59,77 swallows the panic value with
  no trace, so a real parser bug would vanish silently.
Task 10: fix round 1/5 dispatched. FIX_BASE = 458b07a.
Task 10: round-1 fix committed 98afc1a. All three Importants addressed. Implementer
  verified rather than assumed, twice: a throwaway test blocked a goroutine in
  Grid.Read, called Close(), and observed (0, io.EOF) in ~100ms; a second
  reproduced the spawn-failure branch with a tagged background process and
  confirmed via ps that the already-spawned pane is reaped. Both scratch files
  deleted, not committed. Scoped re-review dispatched (opus).
Task 10: fix round 1/5 (3 addressed, 2 open — both NEW, introduced by the fix;
  commits 458b07a..98afc1a). Re-reviewer verified every claim against vendored
  source rather than the report, and explicitly discounted the implementer's
  deleted scratch evidence as weak (Finding 1's ps check exercised Pane.Close, not
  main.go's error path) while confirming the mechanisms independently.

Ruling: New Breakage 1 (child reaping now gated behind screenLock) is REAL and a
  REGRESSION against pre-fix behaviour. The guard takes screenLock before
  closePanes; if the main loop is parked in Flush to a stalled consumer — a dead
  ssh link, which is a first-class case for this project — the guard never reaches
  closePanes, Guard.Arm never re-raises, and the process becomes UNKILLABLE by
  SIGTERM with children unreaped. Pre-fix, closePanes ran before the guard's own
  Flush, so children still died. FIX: hoist closePanes above screenLock.Lock() —
  it touches neither scr nor t and never needed the lock.
  Cost if wrong: none; strictly restores a guarantee the fix removed.

Ruling: New Breakage 2 (a queued frame repaints AFTER the restore) is REAL and the
  mutex made it near-deterministic rather than merely possible: a waiter parked
  ~2s enters starvation mode and gets a direct handoff, so the main loop wakes and
  paints onto the restored cooked screen before the re-raise lands. The named race
  is gone; the torn restore survives from the other side. FIX with a `stopped`
  bool under the same lock, checked in the frame and resize cases.
  Cost if wrong: one redundant boolean check per frame.

Ruling: the reviewer's out-of-scope observation IS in scope and I am pulling it in.
  On the signal path closePanes kills the panes, the pumps exit, onExit closes
  quit, and the main loop can `return nil` so main() exits 0 — racing the re-raise
  and defeating hostterm's 128+signo contract. The reviewer correctly called it
  pre-existing at 458b07a and not worsened by the fix diff. I am fixing it anyway
  because it is user-visible in the FINAL deliverable, it defeats the contract
  Task 2 exists to provide, and Les has a manual verification step that checks
  exactly this exit status. Leaving it would make his check fail.
  Cost if wrong: a bounded wait on an exit path that is already terminating.

Task 10: minor (deferred): grid.Close() races x/vt's `closed` flag — the reviewer
  reproduced it under -race in a standalone module at emulator.go:263 vs :270.
  Upstream defect, practical consequence nil, but it WILL fire for anyone running
  this path under -race. Wants a comment at grid.go:58-63 and an upstream issue.
Task 10: fix round 2/5 dispatched. FIX_BASE = 98afc1a.
Task 10: round-2 fix committed e48d895. Implementer flagged a deviation from my
  prescribed shape rather than substituting silently, and it is a real catch:
  ptyx.Kill closes Master partway through the grace wait, so <-quit fires ~97us
  BEFORE Kill returns (measured, not assumed). A `stopped` flag set after
  closePanes — the literal shape I suggested — would therefore still read false
  when <-quit fires, silently failing to fix Open 3. It used atomic.Bool set at
  the top of the closure instead, since Open 1 forbids gating closePanes on
  screenLock. Scoped re-review dispatched (opus).
CONTROLLER VERIFICATION (my own, not a subagent's): built a pty harness to test
  the exit-status contract non-interactively, since it needs a real TTY.
  RESULT — Open 3's fix WORKS: at 80x24, 200x50, 4x2 and even 1x1 the process
  dies by signal 15, so a shell reports 143. The hostterm contract holds.
  BUT the harness found a NEW crash nobody caught:

Task 10: NEW FINDING (Important) — startup panic on a 0x0 pty.
  When a pty has no winsize set, t.GetSize() returns (0, 0, nil) — no error, so
  main.go's `if err != nil { width, height = 80, 24 }` fallback never fires.
  Then paneCols = (0-1)/2 = 0 and paneRows = 0-1 = -1, and NewSafeEmulator(0, -1)
  panics in uv.NewBuffer with "makeslice: len out of range".
  Two consequences: the binary crashes on startup, AND the panic happens at
  main.go:55, before the guard exists, so the terminal is stranded in alt screen
  (I can see ESC[?1049h emitted and never undone).
  NOTE: Task 10's reviewer explicitly checked this risk and cleared it — but it
  verified NewScreenBuffer(0,0), and the value that actually reaches the emulator
  is rows = height-1 = MINUS ONE. Zero degrades gracefully; negative panics.
  A careful review got this wrong; running the binary found it. Worth remembering
  that static review and execution catch different things.
  Reachable in CI/automation/expect harnesses with a pty but no winsize, not in a
  normal interactive terminal. Fix is a clamp.
Task 10: fix round 2/5 (3 addressed, 0 open from the review; commits
  98afc1a..e48d895). Re-reviewer confirmed the atomic.Bool deviation SOUND on all
  four legs, and strengthened the implementer's own argument: Master.Close() at
  reap.go:115 is followed by up to ~1.5s of escalation, so the measured ~97us is
  the FLOOR, not the typical case — my prescribed shape would have failed
  reliably, not occasionally.
  Confirmed the Open 1 regression is genuinely gone: a wedged Flush can still hang
  the process, but children are now reaped first, so it hangs with no orphans.
Task 10: parked — residual exit-status window: if the closure needs >2s after
  <-quit fires (SIGKILL escalation plus a screenLock wait), the main goroutine
  wakes and can still race the re-raise to exit 0. Inherent to the bounded-wait
  shape I prescribed; 2s covers the normal path with margin. Reviewer recommends
  leaving it, and I agree.
Task 10: fix round 3/5 dispatched — my 0x0 startup panic, two comment-accuracy
  nits, and capturing my pty harness as a repeatable script.
  FIX_BASE = e48d895.
Task 10: fix round 3/5 (4 addressed, 0 open; commits e48d895..441d736)
  Re-reviewer traced the double-restore through pinned ultraviolet source rather
  than accepting idempotence as claimed: second ExitAltScreen sees altScreen
  false and only queues the reset mode; second Stop's Reset re-queues ShowCursor
  so the cursor is not left hidden; Reset's MoveTo early-returns. Net cost is one
  redundant ESC[?1049l. No ordering hazard, and the two restores are strictly
  serialized by guard.Stop()'s sync.Once.
  Confirmed round-1 Finding 1 did not regress while being tidied: closePanes is
  still the first statement on the spawn-failure path.
Task 10: minor (deferred) -> SEED TO FINAL REVIEW: scripts/ptycheck.py asserts
  "died by the sent signal", which /bin/cat satisfies (demonstrated). So
  `make verify-exit` would stay green if guard.Arm() were deleted — it does not
  prove the guard ran. It IS a real net (catches the panic, a hang, and the
  exit-0 race), but Makefile:44-47 and the script docstring claim it verifies
  "hostterm's restore-then-re-raise design", which overstates it. Fix the wording;
  optionally strengthen the assertion to require the alt-screen-exit sequence in
  the pty output, which would distinguish wideboi from a bare default kill.
Task 10: minor (deferred) -> SEED TO FINAL REVIEW: main.go:77-87 — a PANIC (not an
  error) from the second client.NewPane restores the terminal but still leaks
  pane 0, since defer guard.Stop() isn't registered until :146. Pre-existing; the
  clamp makes the known trigger unreachable; the new defer's comment claims panic
  coverage that is terminal-only, not process-tree.
Task 10: minor (deferred): max(...,1) at 1x1 is non-crashing but degenerate — pane
  1 and the divider land off-screen and the status line overwrites pane 0's single
  cell. Safe (SetCell/Line.Set drop out-of-range, Blit clips) but should not be
  read as "1x1 renders correctly". Hardcoded layout; Plan 2 owns the real one.
Task 10: complete (commits 02ceb3c..441d736, review clean after 3 fix rounds)
  MANUAL VERIFICATION OWED BY LES: brief Steps 4-5.
ALL 10 TASKS COMPLETE. Dispatching final whole-branch review.

## FINAL WHOLE-BRANCH REVIEW (opus, 0580f70..441d736)
Verdict: CHANGES REQUESTED. 1 Critical, 9 Important, 10 Minor.
go build / go vet / go test -race ./... all clean.

Ruling: C1 (SIGINT not armed) is CONFIRMED and is MY defect. Verified directly:
  plan.md:423 (Task 3) arms all four signals; plan.md:980 (Task 6) and :1729
  (Task 10) arm only three. I dropped SIGINT when writing Task 6's replacement
  run(), with no rationale, and spec.md:362 names it in the adopted matrix. No
  ruling exists because nobody noticed — it survived Task 6's review, Task 10's
  review and three re-reviews. Ctrl+C masks it (raw mode clears ISIG), so only an
  explicit kill -INT exposes it: process dies by default disposition, ESC[?1049l
  never emitted, console.Restore never runs, guard closure never runs so
  closePanes never runs. FIX.
Ruling: dispatching ONE fix wave with C1, I1, I2, I3, I5, I6, I7, I8, I9 and the
  cheap doc/Makefile items, per the process's one-dispatch rule. I2 (SendKey
  blocking the event loop) is included with an explicit escape hatch: if it needs
  more than a small per-pane buffered channel, report rather than restructure.
Ruling: M1 (no tests in internal/client) is real and NOT in the wave — adding a
  test suite for the seam is Plan 2 work, not a fix-wave item. Recorded as the
  top testing gap for Plan 2.
Ruling: M7 (ps -axo portability on Linux) cannot be settled here — there is no
  Linux host and no CI. Recorded as a Plan 2 precondition, since the anti-leak
  guarantee would degrade silently if Descendants returned a short list there.

## SCOPED RE-REVIEW OF THE FIX WAVE (opus, 441d736..950775e)
C1 + 8/9 Importants + all Minors ADDRESSED, each verified red-then-green.
Re-reviewer independently confirmed BOTH implementer deviations were correct and
my instructions wrong: it deleted closePanes in a scratch tree and watched MY
prescribed harness pass green 3/3 (the escapee version goes red), and it
instrumented both code paths to confirm my teardown-error placement is never
reached on the signal path. It also closed the I5 test gap itself by injecting a
panicking Grid into a scratch build and observing real isolation.

Ruling: I2 is PARTIALLY ADDRESSED and the residual is a genuine Plan 2 item, not
  a fix-wave item. The direct SendKey block is gone, but x/vt's SafeEmulator
  .SendKey holds the EXCLUSIVE se.mu across a blocking pipe write, so Surface() ->
  Draw blocks behind it while holding screenLock. Reproduced end-to-end in the
  real binary: ~4KB typed into a wedged pane => 0 bytes rendered in 2s, ctrl+q
  unreachable. The implementer's claim that it could not reproduce this was wrong,
  and its own harness contradicts the stated reason.
  Root cause is the pty-writer pump parking in Master.Write; the fix is a bounded
  write on the master, which trades dropped child-bound bytes and needs its own
  review. Parking it.
  MITIGATING AND VERIFIED: teardown still works from the fully wedged state
  (SIGTERM -> died by signal 15, ESC[?1049l seen) precisely because the guard
  closure runs closePanes BEFORE taking screenLock — the Task 10 round-2 fix.

Ruling: BLOCKING, and I agree — the new comments at pane.go:179-185 and :95-99
  claim the event loop is protected from an unresponsive child, which is false.
  This entire review wave was about comments and tests claiming more than they
  deliver (I3, I5); shipping a fresh instance of that is not acceptable.
  Dispatching a comment-and-spec-only correction. No behaviour change, no
  re-test beyond make check. This is not a second fix wave.
