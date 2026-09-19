# wideboi Plan 5 — execution ledger

> Promoted verbatim from the gitignored subagent-driven-development workspace,
> which is deleted at the end of the run. Plans 2-4 kept no ledger and their
> rulings were lost; this one is committed so Plan 6 inherits a record rather
> than a memory.

Spec: none of its own. Binding authority is
docs/dev-sessions/2026-09-18-wideboi-v1-foundations/spec.md; Plan 5 repairs
deviations from it. Forward-looking work is docs/BEYOND-V1.md.
Branch: plan-5-usability. Merge base: 84f24fd.
Les authorised opening a PR when green (he is away).

Note: no TodoWrite tool in this session. This ledger is the sole progress record.
Plans 2-4 kept no ledger and their rulings were lost with the scratch workspace.
This one gets promoted into the session directory before the workspace is deleted.

## Pre-flight conflict scan

### Pairs sharing a file or interface

| Tasks | Produces -> Consumes | Finding |
|---|---|---|
| T1, T2, T3 -> scripts/smoke.py | each appends a case and registers it in CASES | Clean. Sequential dispatch, disjoint case names (`status line`, `control keys`, `resize`). Each must add its own entry to CASES; flagged in each dispatch. |
| T3 -> T4 | both modify internal/server/pane.go | Clean. T3 rewrites `Pane.Resize`; T4 rewrites `Start`'s pump defer and `Pane.Close`. Disjoint functions. T3 changes Resize's signature to return error; T4 does not call Resize. |
| T3 -> internal/server/server.go | new `resizePanesLocked` helper called from MsgResize and three verb arms | Clean. Verified field names against the real code: `s.panes map[int]*Pane` (server.go:25), `s.strip *layout.Strip` (:24), `s.strip.ComputePlacements(s.cols, s.rows)` (:206). |
| T4 -> internal/server/term/grid.go | comment-only update to `Close`'s doc | Clean. No behaviour change in that file. |
| T1 -> internal/client/client.go | status line rewrite | Clean. `c.cols` exists (client.go:28), so the truncation guard compiles. |

### Per-task self-consistency

| Task | Finding |
|---|---|
| T1 | **DEFECT — see ruling below.** The specified test asserts `b"alt+l"` appears in the output, but the specified help string is `alt+h/l focus`, which does not contain that substring. Verified: `'alt+l' in 'alt+h/l focus'` is False. The task as written cannot pass. |
| T2 | Clean. The `cat -v` case needs `os` and `time` in smoke.py; both are already imported there. |
| T3 | **DEFECT — see ruling below.** The plan's helper reads `pl.Dest.Dx()`, but `layout.Placement`'s field is `Dst`, not `Dest` (layout.go). Would not compile. |
| T4 | Clean, with one addition the plan states but does not show: `gridClosed chan struct{}` must be added to the `Pane` struct AND initialised in `NewPane` (pane.go:49-57), alongside the existing `closed:` line. |

### Rulings made before execution

Ruling: T1's test/help-string contradiction resolves in favour of the TEST's
  intent, not its literal. The point is that the status line names the real
  bindings; `alt+h/l` is better UX than spelling both out and eating width.
  Amending the test to assert on `alt+h`, `alt+n`, `alt+w`, `alt+q` — dropping
  the `alt+l` assertion, which `alt+h/l` covers visually. The no-`$mod`
  assertion, which is the defect's actual regression guard, is untouched.
  Cost if wrong: the help could later drop `alt+l` without the test noticing.

Ruling: T3's `pl.Dest` is a typo for `pl.Dst`. Verified against
  `layout.Placement{PaneID, Src, Dst, Z}`. Use `pl.Dst.Dx()` / `pl.Dst.Dy()`.
  Cost if wrong: none; it would not compile otherwise.

Ruling: T4's `gridClosed` channel must be declared in the struct and created in
  `NewPane` next to `closed: make(chan struct{})`. The plan says to add it but
  shows neither site. Carried into T4's dispatch explicitly.
  Cost if wrong: nil-channel receive blocks forever, caught by T4's own 1s
  timeout and by the test run.

Scan result: two defects in my own plan text, both ruled on above, both cheap.
No task-pair conflicts.

## Progress

Task 1: implementer DONE (commit 671af75). 10/10 smoke, make check green, tree
  clean. Observed RED twice: once for the defect, once because the brief's
  literal fix still failed.
Task 1: PLAN DEFECT the implementer found (beyond my two pre-flight ones):
  writing to the terminal's LAST column makes ultraviolet bracket the write
  with autowrap-toggle escapes (\x1b[?7l / \x1b[?7h), splitting "alt+q" across
  escape sequences on the wire. Invisible on screen; fatal to a raw-byte
  assertion. Fixed by truncating to c.cols-1 and dropping the leading space.
  Worth promoting to docs/LESSONS.md at the end — it is a general trap for any
  wire-level assertion about the rightmost column.
Task 1: it also regenerated testdata/golden/startup.txt (not in the brief's file
  list) because the committed snapshot still contained the "$mod" words and
  make check runs golden.py in compare mode. Legitimate; reviewer will confirm.
Task 1: CARRY TO TASK 4: the implementer dropped a `make race` line from the
  README because that target does not exist yet. Task 4 creates it — Task 4 must
  add the README line back.
Task 1: task reviewer dispatched (sonnet).
Task 1: review spec ✅, quality CHANGES REQUESTED — 1 Important, 2 Minor.
  Reviewer verified rather than accepted: the truncation fix is a general
  invariant (c.cols-1 leaves the last column unwritten at ANY width, not just
  the 100-col smoke default); the regenerated golden is non-vacuous (new word
  list is 15 real key tokens, not empty); no new seam crossing.

Ruling: the Important finding is MY defect, in the brief's README text. I wrote
  "Everything else goes to the focused pane, including ctrl+w, ctrl+l and
  ctrl+c." That is false at this commit — main.go:126,130 route ctrl+l and
  ctrl+w to verbs. It becomes true only after Task 2, which is next.
  I am NOT deferring it to Task 2. Every commit should be honest on its own, and
  a README that lies about keys is precisely the defect this task exists to
  remove. But I also will not have two tasks edit the same sentence in opposite
  directions: Task 1 drops the specific examples ("Everything else goes to the
  focused pane."), and Task 2 adds ctrl+w/ctrl+l back once they are true.
  Cost if wrong: one sentence is briefly less specific than it could be.

Task 1: minor (deferred): client.go:141-142 truncates by byte index, so a
  multi-byte status glyph landing exactly on the boundary could be split.
  compose.WriteString degrades it to U+FFFD rather than crashing. Unexercised
  in either direction.
Task 1: minor (deferred): no smoke coverage below 100 columns; the truncation
  guard is argued correct at any width but only exercised at one.
Task 1: fix round 1/5 dispatched. FIX_BASE = 671af75.
Task 1: fix round 1/5 (1 addressed, 0 open; 671af75 amended to 8d2645e)
Task 1: complete (commits 84f24fd..8d2645e, review clean)
  Note: implementer AMENDED rather than stacked, per Les's git practice for
  small follow-ups on an unpushed branch. Verified the amend preserved all of
  Task 1's work (same 4 files, same shape) before accepting.
Task 2: implementer DONE (commit e43e22c). 11/11 smoke, make check clean, golden
  unchanged. All four ctrl keys now reach the pane; ctrl+q and ctrl+o kept.

Ruling: my brief's smoke-case setup was WRONG and the implementer was right to
  change it. `cat -v` alone can never observe ctrl+w passing through on ANY
  canonical-mode pty: the kernel's WERASE consumes 0x17 before cat reads it. It
  proved this independently with a bare pty.fork()+cat -v repro, then changed
  only the setup line to `stty -icanon -iexten -echo; cat -v` and re-ran the
  full RED->GREEN cycle. Assertions, bytes and the CASES entry are verbatim.
  Accepted. Cost if wrong: none — the corrected mechanism is strictly more
  faithful to what the test claims to measure.

Ruling: the implementer's out-of-scope observation is REAL and worse than it
  framed it, and I am pulling it into scope. case_new_column_opens_pane and
  case_cycle_width each type a control byte and then assert only that "typing
  reaches a pane" — they never check a column opened or a width changed.
  Verified by reading them: both are functional duplicates of
  case_typing_reaches_the_focused_pane wearing verb names. They did not become
  vacuous when Task 2 removed the ctrl fallbacks; they were vacuous when
  written in Plan 3, and Task 2 merely exposed it.
  Two of eleven cases in a suite whose entire premise is catching exactly this
  cannot be left asserting nothing. Fixing in Task 2's fix round 1.
  Cost if wrong: modest scope growth in a task that is otherwise complete.
Task 2: fix round 1/5 dispatched. FIX_BASE = e43e22c.
Task 2: fix round 1/5 (1 addressed, 0 open; commits e43e22c..22de805)
Task 2: review clean — spec ✅, quality approved. Reviewer independently verified
  the WERASE reasoning against POSIX termios rather than accepting it, grepped
  the shipped matrix to confirm the README claim is now true, and traced BOTH
  new smoke assertions to the source that makes them deterministic
  (layout.AddColumn reassigns focusIndex; CycleWidth 49->80 at a 100-col
  two-pane launch) — i.e. they key on something that genuinely varies with the
  verb, not something incidental that would make them flaky.
  Resolved its cannot-verify item myself: make check exits 0 and the tree is
  byte-identical before/after.
Task 2: minor (deferred): smoke.py:115-118 recompiles a regex per call.
Task 2: complete (commits 8d2645e..22de805, review clean)
Task 3: implementer DONE (commit 0b132b3). RED->GREEN on the smoke case, 12/12
  smoke, new TestResizePropagatesToPanes passes, make check clean, no golden
  drift. REFLOW IS NOW DEMONSTRABLY LIVE: it drove the real binary, wrapped a
  marker across two rows by narrowing, widened back, and captured the redraw
  showing the marker rejoined into one contiguous line. That is the entire
  payoff of Plan 2 becoming reachable.

Ruling: my brief's unit-test snippet could not compile — it reads srv.mu and
  srv.panes directly, but server_test.go is package server_test (external).
  The implementer followed the file's real shape instead and added one small
  exported Server.PaneSize() accessor, mirroring the existing CursorInfo. That
  is the right call: without it the test could only re-assert
  broadcastLayoutLocked, not that a pane actually resized. Accepted, and it
  flagged the new surface rather than slipping it in.
  Cost if wrong: one extra exported accessor on Server.

Task 3: carried finding — onPaneExit (organic child death) does not call
  resizePanesLocked; only VerbKillPane does, matching my brief's literal call
  site list. The implementer could not construct a case where it matters but
  did not prove it cannot. My read: in the no-shrink model a pane's width is a
  preset fraction independent of its neighbours, so a death changes positions
  and scroll, not sizes — which is why no case surfaced. Pointing the final
  review at it rather than guessing.
Task 3: the implementer independently rediscovered the vt.Emulator.Close vs
  pump Read race and confirmed it exists on the unmodified base. That is
  Task 4's job; correctly left alone.
Task 3: task reviewer dispatched (opus — the diff holds the server lock across
  a per-pane reflow, which is worth a careful look).
Task 3: review spec ✅, quality CHANGES REQUESTED — 1 Critical, 3 Important,
  4 Minor. The Critical and two Importants are defects in MY brief.

Ruling: CRITICAL 1 is mine and is the most serious error in this plan. My brief
  said "Placements already carry each pane's destination rect, which is its
  logical size." FALSE. Dst is the CLIPPED on-screen rect
  (layout.go: dst := screenRect.Intersect(viewportRect)); Column.Width is the
  logical size, and Src exists precisely so a full-width pane can be cropped
  into a partial Dst. A partly-visible column is the NORMAL state of a
  scrolling strip, so this is not an edge case.
  Consequence, verified by the reviewer against a scratch copy of the real
  server: pane 2 at Dst=10 cols gets TIOCSWINSZ for 10 columns and its
  emulator reflowed 59->10, destroying its layout. Left-clipped is worse — Src
  and the pane's size contradict, and client.go:149's cursor math goes
  negative. This is a REGRESSION Task 3 introduced: before it nothing resized,
  so nothing was corrupted.
  It also violates the v1 spec's central invariant verbatim: "A partly-covered
  pane keeps its full logical width, so the child receives no SIGWINCH and
  never learns it was occluded." The spec is binding; my brief contradicted it.
  FIX: resize to the column's LOGICAL width, not Dst.Dx(). Src.Dx() is not an
  alternative — it is clipped identically by construction.
  Cost if wrong: none. The current behaviour actively corrupts panes.

Ruling: IMPORTANT 2 (blocking pipe under the server lock) is REAL and I am
  fixing it. Nothing re-enters s.mu, but Emulator.Resize writes to an
  UNBUFFERED io.Pipe when the child has in-band resize set, and its only
  drainer is the pty-writer goroutine, which can itself be parked in
  Master.Write against a child not reading stdin. The server goroutine then
  parks inside resizePanesLocked holding s.mu and the whole multiplexer stops.
  Note DrawPane already drops s.mu before touching the emulator — this new
  path ignored that precedent. FIX by snapshotting under the lock, releasing,
  then resizing, which also closes the lock-hold question outright.
  Reviewer's honest calibration, recorded: the CPU cost is a non-issue
  (~0.83ms x N is small beside an existing per-second fork of `ps` under the
  same lock), and both Resize paths short-circuit on unchanged size.
  Cost if wrong: a resize can briefly race a concurrent layout change.

Ruling: IMPORTANT 3 (onPaneExit) — both the implementer and I reasoned from
  Column.Width being fixed, which is true, but Dst is clipped, which we both
  missed. Killing a middle column leaves pane 3 at Dst 29 while its PTY is 50.
  VerbKillPane corrects it; onPaneExit does not, so `exit` and alt+x leave
  different states. FIX together with Critical 1 rather than as a matched
  pair of bugs.

Ruling: IMPORTANT 4 — the new smoke case cannot fail on the thing it is about.
  In its scenario the focused pane goes 59x29 -> 59x19: only ROWS change, so
  it would still pass with width propagation entirely broken, which is the
  failure mode its own docstring names. FOURTH instance of this pattern in
  this project. FIX: narrow the host below one column width and assert on the
  columns field specifically.

Task 3: minor -> FIXING ANYWAY: server.go:224 log.Printf writes into the alt
  screen. Rated Minor but the blast radius is painting log lines over the
  user's panes and into the smoke byte stream, and we are editing that
  function regardless. Collect the errors instead.
Task 3: minor (deferred): pane.go:156-158 non-positive guard is unreachable
  from this path but correct defensive code; keep.
Task 3: minor (deferred): grid.go:236-266 read/resize/writeback is not atomic
  against a concurrent em.Write; no data race, but reflowed-stale content can
  paint over fresh output. Ordering already mitigates. Wants a comment.
Task 3: minor (deferred): server_test.go asserts against Dst so it structurally
  cannot catch Critical 1, and only covers panes present in Placements — which
  excludes exactly the clipped ones that go stale. Revisit after the fix.
Task 3: parked — MsgAttach also never calls resizePanesLocked, so pane 2 sits
  at 40 wide against a 39-wide Dst from the first frame. Same family; folds
  into Critical 1's fix.
Task 3: fix round 1/5 dispatched. FIX_BASE = 0b132b3.
Task 3: round-1 fix committed 2193a39. All four findings plus the log fold-in
  addressed. Both new tests confirmed RED against reverted code with numbers
  matching the reviewer's evidence exactly (cols=10 -> cols=59). 13/13 smoke,
  -race clean on the new tests, golden unchanged.

Ruling: the implementer DISPROVED my own suggested test scenario and was right.
  I suggested "narrow the host to 40 to force pane 1 down to 40". That assumes
  column widths are FRACTIONS of the viewport. Verified myself: they are
  ABSOLUTE cell counts — CycleWidth cycles a hardcoded 40/60/80 and Column.Width
  is set at creation and never rescaled. Pane 1 stays at 59 in a 40-wide
  viewport. So I was wrong twice in the same area: first that Dst was logical
  size, then that a viewport clamp existed. It rewrote the case to assert the
  opposite direction (a clipped pane's cols UNCHANGED), which matches the
  59-vs-10 evidence and the spec invariant it was citing. Accepted.

Task 3: SPEC DIVERGENCE FOUND (pre-existing, Plan 3, not Task 3) — the v1 spec
  says "Column widths are preset fractions of the viewport (1/4 default,
  cycling 1/4 -> 1/3 -> 1/2)". The code uses absolute cells, cycling 40/60/80.
  Consequence: a host resize changes pane ROWS only; widths change only via
  alt+w. Arguably the code is better than the spec here — a fractional width
  means shrinking the window shrinks every pane, which is what the no-shrink
  scrolling model exists to avoid — but the two disagree and one should move.
  Not Task 3's to settle. -> final review to triage, and record in BEYOND-V1.

Task 3: NEW BUG FOUND, not fixed, correctly out of scope — after a wrap
  collapses back to one line (40-col wrap rejoining at 60), stale glyph debris
  is left on the row that should have gone blank. term.Reflow itself re-touches
  every row correctly, so this is downstream in the emulator's touched-line
  tracking or the render/diff layer. Found with a real VT100 emulator (pyte,
  throwaway venv) after the implementer judged its own earlier ANSI-stripping
  regex unreliable. Reproducible. -> final review to triage.
Task 3: parked — narrow race window between Pane.Resize and Pane.Close opened
  by releasing the lock. Same family as Task 4's residuals.
Task 3: scoped re-review dispatched (opus).
Task 3: fix round 1/5 (5 addressed, 3 new — commits 0b132b3..2193a39)
  Re-reviewer re-derived Critical 1's numbers from layout.go by hand rather
  than trusting the transcript, independently confirmed the absolute-width
  model, and traced WHY the rewritten smoke case is a genuine guard: focusing
  a pane does NOT call resizePanesLocked, so nothing repairs pane 2 on the way
  in and the reverted server genuinely reports 10 columns.
  It also caught that the old TestResizePropagatesToPanes asserted
  `cols == pl.Dst.Dx()` — it had literally encoded the bug as its expectation.

Correction to my own earlier claim: I said releasing the lock means the
  multiplexer keeps running. It does not. The Run goroutine still blocks inside
  Pane.Resize, so message dispatch still stalls on a wedged child. What the
  change actually rescues is everything needing s.mu from ANOTHER goroutine —
  DrawPane/CursorInfo keep painting, and crucially srv.Close() from the client
  goroutine still works, so the user can still quit. That is the real win.

Ruling: NEW BREAKAGE 1 (Pane.Resize lost mutual exclusion) is REAL and is
  caused by MY two fixes in combination — releasing s.mu plus adding the
  onPaneExit call site. Two resizePanesLocked can now be in their unlocked
  windows simultaneously, from the Run goroutine and the pty-reader goroutine,
  both writing p.cols/p.rows (plain unguarded ints) and both doing a full
  read/Reflow/write-back of the same cell buffer. Trigger is ordinary: a shell
  exits while the user drags the window edge.
  Task 4's -race gate will NOT catch this — the tests never exit a pane
  mid-resize. FIX with a per-pane mutex across Pane.Resize and Pane.Close.
  Cost if wrong: one uncontended mutex on a path that already does syscalls.

Ruling: NEW BREAKAGE 2 (off-screen panes never resized) is REAL and defeats
  part of the task's purpose. resizePanesLocked iterates ComputePlacements,
  and a fully scrolled-off column is dropped by dst.Empty(), so it gets
  neither width NOR height. Repro: at 40x20 with pane 1 focused, pane 2 has no
  placement and keeps rows=29 while everything else is 19; focus verbs never
  correct it, so it renders a 29-row grid into a 19-row area. Not newly
  introduced, but the fix reasoned about exactly this and concluded wrongly,
  and the new doc comment now asserts the opposite. FIX by iterating the
  strip's columns with a height accessor alongside ColumnWidth.
  Cost if wrong: resizing a few panes that were already correct.

Ruling: NEW BREAKAGE 3 (errors collected then discarded at all six sites) —
  fixing, because "collect errors instead of logging" must not be recorded as
  done when the information is simply gone. Pane already has a failure sink
  (failMu/failures, surfaced by Pane.Close); route them there.
Task 3: fix round 2/5 dispatched. FIX_BASE = 2193a39.
Task 3: round-2 fix committed 171787b. All three new findings addressed.
  The concurrent test was built as asked and fires reliably: 4/4 caught the
  predicted p.cols/p.rows race without the fix, 5/5 clean with it. Notably it
  diffed the race SIGNATURE rather than pass/fail, because the adjudicated
  Close-vs-Read race fires on any pane close under -race and could have masked
  the result in either direction.
  Bonus race it found and fixed: Pane.Size() read cols/rows with no lock at
  all; harmless while Resize ran under s.mu, racy the moment it did not.
  Server.PaneSize was its only caller — which is the accessor I approved in
  round 1, so my approval carried a latent race with it.
Task 3: scoped re-review of round 2 dispatched (opus).
Task 3: fix round 2/5 (4 addressed, 3 new — commits 2193a39..171787b)
  Re-reviewer independently validated the concurrent test's methodology by
  stripping only the two resizeMu lines in a temp copy: 3/3 fired with the
  predicted signature (Resize's no-op check at pane.go:176, plus companion
  races inside term.usedWidth/Reflow), 6/6 clean restored. Confirmed that
  signature is distinct from the adjudicated Close-vs-Read race. Also verified
  no off-by-one in the shared AvailHeight, and that lock ORDER is consistent
  (s.mu -> resizeMu -> se.mu, no ABBA).

Ruling: NEW BREAKAGE 1 is mine and it is the worst regression in this plan.
  My round-1 instruction said to take the per-pane mutex across Resize "and
  ideally Close". Close's body is now resizeMu.Lock() -> pty.Kill ->
  grid.Close() — but those two calls are the ONLY wedge-breakers:
  ptyx.Kill closes Master (unparking the pump so it drains the pipe), and
  Emulator.Close does CloseWithError on the pipe writer without taking se.mu,
  which is exactly why it worked while se.mu was held.
  So: child stops reading stdin -> Resize wedges holding resizeMu -> user
  quits -> Server.Close parks on resizeMu -> wg.Wait() never returns ->
  wideboi hangs on exit with the pane unkillable. Round 1 traded "parked but
  killable" for "narrower window"; round 2 traded it for "unrecoverable".
  THIRD time in this project a lock I specified has blocked its own escape
  hatch — identical in shape to Plan 1's mutex that made SIGTERM unkillable.
  FIX: in Close, run pty.Kill and grid.Close FIRST, unlocked, then take
  resizeMu for bookkeeping only.
  Cost if wrong: a wedged Resize briefly observes a closing pane.

Ruling: NEW BREAKAGE 2 (Server.PaneSize holds s.mu across Pane.Size, which now
  takes resizeMu) reintroduces the exact pattern round 1's Important 2
  removed — on the accessor I approved in round 1. Latent today (tests only)
  but its doc comment advertises it for observability, so the next caller is
  the landmine. FIX: hold s.mu for the map lookup only, release, then Size().

Ruling: NEW BREAKAGE 3 (s.rows <= 0 no longer short-circuits) is real. The old
  code delegated to ComputePlacements, which returns nil for a non-positive
  viewport; AvailHeight(0) is 1, so a stray MsgResize{Rows:0} or any verb
  before attach would SIGWINCH every child to 1 row. The pane guard does not
  catch it because 1 is positive. FIX: early return.

Ruling: FALLBACK, if the round-3 shapes do not come out clean — remove
  resizeMu entirely and park the Resize-vs-Resize race. Reasoning: the root
  cause is architectural (the emulator's reply pipe is unbuffered and its only
  drainer can park), so every lock around Resize inherits that unboundedness.
  A data race that can corrupt one pane's display is strictly better than an
  unrecoverable hang on exit. The real fix is the parked bounded-write item in
  BEYOND-V1, which is not this plan's work.
Task 3: fix round 3/5 dispatched. FIX_BASE = 171787b.
Task 3: round-3 fix committed 221038f. New 1/2/3 and the nit all addressed.
  The implementer EVALUATED my fallback and rejected it with reasoning: the
  fallback does not avoid the reopened Resize-vs-Close window (Close would not
  wait for Resize either way) AND would additionally reopen Resize-vs-Resize,
  so the primary fix is strictly dominant. Correct analysis; fallback withdrawn.
  Built the wedge test modelling the real pipe coupling (fake Grid whose Resize
  blocks until Close breaks it), caught a bug in its own fake, RED at >5s hang
  -> GREEN at 50ms.
  INTEGRITY NOTE worth keeping: it nearly reported "20/20 clean" on the
  concurrency test, recognised that as `go test` result caching rather than a
  result, re-ran with -count=1, and reported the true 14/15 failure rate
  instead of the flattering cached one.

Ruling: Task 3's fix reopens a ptyx-level Resize-vs-Close race
  (Master.Close vs Setsize's ioctl — a real fd-reuse hazard per Go's Fd()
  docs, not merely a contained data race), and TestConcurrentResizeAndPaneExit
  Race is consequently red under -race at a high rate. This COLLIDES with
  Task 4, which adds -race to the gate.
  Resolution: carry it to Task 4 rather than growing Task 3 to a fourth round.
  Task 4 is the lifecycle-race task and this is a lifecycle race; the fix
  belongs in ptyx (guarding Master against use-after-close), not in another
  server-package mutex, which is precisely the move that has failed three
  times here. Task 4 must close BOTH races before -race can enter the gate.
  Cost if wrong: Task 4 grows by one finding and the gate lands a task later.
Task 3: scoped re-review of round 3 dispatched (opus).
Task 3: fix round 3/5 (4 addressed, 1 new Low; commits 171787b..221038f)
  Re-reviewer verified the wedge mechanism from x/vt source rather than the doc
  comment (SafeEmulator has NO Close override, so grid.Close reaches
  Emulator.Close which takes no se.mu; Emulator.Write can park on the same pipe
  WHILE HOLDING se.mu, wedging Resize one level up). Confirmed both
  wedge-breakers are genuinely the only two, and both now run unlocked.
  Judged the wedge test faithful with two caveats: it models only the
  grid.Close unwedge path, not Kill's pump-drain path, so its 5s bound fits the
  real ~3.5s latency by coincidence rather than construction; and the fake's
  unconditional close(resizeEntered) panics on a second Resize.
Task 3: minor (deferred) -> CARRY TO TASK 4: pane.go:246 silently dropped the
  only enforcement of ptyx.Kill's documented single-caller precondition.
  resizeMu was providing it for free; closeOnce guards only the channel.
  Unreachable today (every caller removes the pane from s.panes under s.mu
  first) but now a load-bearing cross-file invariant with no local enforcement.
  Task 4 reshapes Pane.Close anyway, so it is the natural home.
Task 3: complete (commits 22de805..221038f, review clean after 3 fix rounds)

CARRIED TO TASK 4 — its scope is now larger than the plan wrote:
  (a) The ptyx Resize-vs-Close fd-reuse hazard. Re-reviewer reproduced it 3/4
      fresh runs and characterised it precisely: the raced word is
      poll.FD.Sysfd, an int, but it is handed straight to SYS_IOCTL, so a
      stale-but-reused descriptor can receive the TIOCSWINSZ. Reuse is
      plausible in-process because Kill itself shells out to `ps` in the same
      window. Not memory-unsafe; worst case is resizing an unrelated pty.
      CHEAP FIX FOUND: creack/pty already ships the correct implementation as
      `ioctlNonblock` (ioctl.go:13) using SyscallConn().Control(), marked
      "NOTE: Unused. Keeping for reference." Under ten lines inside ptyx, no
      server-package change. Re-reviewer says explicitly: do NOT reconsider
      Task 3's change.
  (b) CursorInfo (server.go:350) still holds s.mu across p.CursorPosition()/
      CursorVisible(), which take se.mu.RLock() — the identical parking
      pattern New 2 just removed from PaneSize, and se.mu is exactly the lock
      a blocked Emulator.Write holds during a wedge. Pre-existing sibling one
      function away from the fix.
  (c) -race blast radius is at least THREE tests, not two:
      TestServerLifecycleAndAttach and TestResizePropagatesToPanes (both the
      adjudicated upstream Emulator.Close-vs-Read signature) plus
      TestConcurrentResizeAndPaneExitRace (the ptyx one).
Task 4: implementer DONE (commit e3adf81). I verified independently: -race
  clean on three consecutive -count=1 runs, `race` in check's prerequisites,
  README line restored.
Task 4: review spec ✅, quality APPROVED with one Important follow-up.
  Reviewer verified both load-bearing claims from source rather than the
  report: (1) Emulator.Read drains e.pr from an io.Pipe created in
  NewEmulator with no path from the PTY master, so the brief's ownership
  transfer would have produced a guaranteed 1s timeout on every close — the
  implementer's rejection was correct, not a dodge; (2) InputPipe() returns
  the concrete *io.PipeWriter, SafeEmulator overrides neither it nor Close so
  the promoted method takes no se.mu, and (*io.PipeWriter).Close substitutes
  io.EOF for nil — so the reader sees EXACTLY io.EOF, bit-for-bit identical
  to upstream's CloseWithError(io.EOF). The comma-ok assertion cannot panic.
  It also walked the wedge question explicitly and found writeResizeMu is
  acquired by Write and Resize only — never Read, never Close — so both
  wedge-breakers stay lock-free. Lock order is total: resizeMu -> writeResizeMu
  -> se.mu, no inversion possible.
  Nice detail: our ioctl.go is judged slightly BETTER than creack/pty's own
  reference version — sc.Control is synchronous so the chan is unnecessary,
  and our unsafe.Pointer->uintptr conversion sits inside the Syscall arg list,
  the compiler-recognised safe pattern, where creack's passes it through two
  frames in technical violation of the unsafe rules.

Ruling: the Important — vtGrid.Draw's scrollback branch dereferencing a live
  CellAt pointer past the lock — must be FIXED HERE, not handed to the branch
  review. The reviewer's argument is decisive and I am adopting it: this task
  just installed a -race gate, the gate is green, and a green gate reads as
  "no races" — but no test scrolls a pane while it produces output, so the
  gate structurally cannot see this one. Shipping that combination is exactly
  the false-assurance failure this entire plan exists to eliminate. Three-line
  fix, same function family the diff already touches.
  Cost if wrong: one more lock acquisition on the scrollback draw path.

Ruling: folding in two Minors because they are load-bearing rather than
  cosmetic. (1) grid.go:380's doc comment claims Close has one externally
  visible effect; there are two — e.closed also short-circuits Emulator.Write
  to ErrClosedPipe. The consequence is genuinely benign, but that comment IS
  the justification for the bypass, so it must be accurate about what it is
  justifying. (2) No test pins the substitution: a ~15-line term test
  asserting g.Read returns io.EOF after g.Close would catch an upstream bump
  breaking the type assertion, which the comma-ok fallback would otherwise
  turn into a silent return to the racy path.
Task 4: minor (deferred): grid.go:268-297 captures before[y][x] pointers, then
  Resize reslices in place on a narrowing, so the SetCell write-back can
  clobber a slot a later iteration still reads. Single-goroutine, not a race.
Task 4: fix round 1/5 dispatched. FIX_BASE = e3adf81.
Task 4: fix round 1/5 (3 addressed, 0 open; commits e3adf81..9fe58cd)
  Re-reviewer verified the new test is non-vacuous by removing the lock in a
  throwaway copy (3/3 failures, 41 race reports) AND by reproducing the
  implementer's own first-draft trap (swapping the partial offset for a full
  one makes it pass clean even unlocked). Arithmetic checked: at h=5 offset=2,
  three of five rows resolve through the live buffer.
  Wedge question answered NO for the fourth time of asking: Draw already
  blocked on se.mu against exactly those wedges, so writeResizeMu adds no new
  blocking edge; vtGrid.Close takes neither lock; and hostterm.Guard calls
  srv.Close() before taking screenLock, so a parked main loop cannot block its
  own rescue.
  Honest caveat recorded: TestCloseUnblocksRead pins the PROPERTY (Read
  unblocks with io.EOF) but its assertion would pass on the fallback too — it
  is the -race gate, installed by this same task, that pins the MECHANISM.
Task 4: minor (deferred): grid.go:141-157 field comment still names only
  Write/Resize as writeResizeMu holders; Draw is now a third.
Task 4: minor (deferred): grid.go:404-408 slightly overstates that the pump
  never reaches Write again; the next sentence covers the case as inert.
Task 4: minor (deferred): grid.go:334 samples sbLen before the lock, so the
  scrollback/live boundary can be one frame stale. Cannot panic (upstream
  returns nil out of range).
Task 4: minor (deferred): grid.go:354 holds writeResizeMu across a whole
  frame's cell walk while scrolled back, stalling the pump for that window.
  Bounded; buys frame consistency.
Task 4: parked — pre-existing and unchanged: a wedged Write still parks the
  main loop, so the in-app quit key waits on a signal-path srv.Close() or the
  pipe unblocking.
Task 4: complete (commits 221038f..9fe58cd, review clean after 1 fix round)

ALL 4 TASKS COMPLETE. Dispatching final whole-branch review.

## FINAL WHOLE-BRANCH REVIEW (opus, 84f24fd..9fe58cd)
Verdict: CHANGES REQUESTED. 1 Critical, 3 Important, 11 Minor, plus a
spec-drift ruling and a full test-vacuity audit of all 13 new tests.

Ruling: CRITICAL 1 confirmed by my own probe against the pinned libraries:
  before ["AAAAAAAAAA" "BBBBB" "CCCCC" ""] -> after Resize(5,4)
  ["AAAAA" "AAAAA" "AAAAA" "AAAAA"]. Content destroyed, first line smeared.
  Mechanism: vtGrid.Resize captures `r[x] = g.em.CellAt(x,y)`, and uv.Line.At
  returns &l[x] — LIVE pointers into the emulator's backing array.
  uv.Buffer.Resize narrows by reslicing in place, so the arrays are unchanged,
  and the SetCell write-back then clobbers source rows that later output rows
  still need. term.Reflow itself is correct.
  This supersedes TWO ledger entries: T3's "stale glyph debris" (mis-sited as
  a render/diff-layer bug) and T4's "captures before[y][x] pointers" (rated
  Minor on the true-but-irrelevant grounds that it is not a race). It is
  silent data loss on a one-keypress path — alt+w cycles 80->40 directly.
  Fix: clone on capture. MUST ship with a MULTI-ROW narrowing test: every
  existing test writes exactly ONE line, where the aliasing is benign, and
  that single-line shape is what hid this through Plan 2, eight Plan-1
  reviews, and four task reviews here.

Ruling: IMPORTANT 2 — the status line truncates alt+q off at 80 columns, the
  commonest width AND main.go's own fallback size. Measured: at 80 the line
  ends at "...alt+j jump  a". Task 1's defect reintroduced at a different
  width. The ledger filed "no smoke coverage below 100 columns" as a coverage
  gap; it is a live defect. Fix with rune-aware truncation (closing the
  byte-index minor in the same edit) plus an 80-column smoke case.

Ruling: IMPORTANT 3 — ctrl+q and ctrl+o are the deliberate escape hatch for a
  terminal not sending Option as Meta, and they appear in NO user-facing
  surface: not the README key table, not the Option-as-Meta section, not the
  status line. The README tells the affected user that every shortcut will
  appear to do nothing, then omits the one key that still works. Fix.

Ruling: IMPORTANT 4 — three new doc comments assert that releasing s.mu keeps
  srv.Close() available, but broadcastLayoutLocked runs on the next line still
  under s.mu and SendServer is a blocking channel send. Hard to reach, but
  this branch's central design argument is written into those comments as a
  guarantee. Soften the comments here; hoisting SendServer is a behaviour
  change for another plan.

Ruling: SPEC DRIFT (a) — adopting the reviewer's ruling: move the SPEC, not
  the code. Width must stay independent of the viewport or the no-shrink model
  collapses, and this branch's whole correctness story depends on it. Rewrite
  spec.md to absolute cell presets. The reviewer also found a real consequence
  nobody had recorded: the cycle 40/60/80 does not include the spawn default
  (max((cols-1)/2, 40)), so on a 200-col terminal you spawn at 99 and the
  first alt+w SHRINKS you to 80, permanently. That is a behaviour change ->
  BEYOND-V1, not this branch.
  Also fix spec drift (b): spec declares Placement{Dest, Src image.Point};
  code has {Src, Dst image.Rectangle}. That mismatch is what made my own
  brief say pl.Dest and fail the pre-flight scan.
  Drifts (c) client-side placement, (d) missing Strategy interface, (e) no
  rapid property tests -> record in BEYOND-V1, do not fix here.
Final fix wave dispatched (one dispatch, per process). FIX_BASE = 9fe58cd.
FINAL FIX WAVE re-review: READY TO MERGE (commits 9fe58cd..f36f0b6).
  All findings ADDRESSED. Re-reviewer confirmed the Critical diagnosis against
  the vendored dependency (uv.Line.At returns &l[x]; Buffer.Resize reslices in
  place) and confirmed the shallow clone is adequate because Line.Set copies by
  value and the emulator replaces whole cells rather than mutating fields.
  It verified the new width-sweep test by MUTATION (removed the quit
  pre-accounting via go test -overlay; the test fails at cols 84-88 and
  93-104), and hand-computed the golden diff to confirm it absorbed nothing
  beyond the intended help-string change.
  Both of the implementer's corrections to the review checked out:
  ServerSend IS buffered at 256 (my Important 4 said unbuffered — wrong), and
  the defer ordering holds.
  Clone-on-capture confirmed COMPLETE: only two sites ever let a *uv.Cell
  escape the emulator, and both are now covered. No fourth.

Ruling: parking four non-blocking residuals rather than opening a second fix
  wave, per the one-wave rule. All are doc or cosmetic:
  (1) client.go:205-206 — runeLen's comment claims equivalence with
      compose.WriteString's cell advance; false for DOUBLE-WIDTH glyphs
      (runeLen counts runes, WriteString advances by Cell.Width). Unreachable
      today: the status line's only non-ASCII is » ! ✓ ✗, all single-width.
      The risk is the comment masking it for whoever adds a wide glyph — which
      BEYOND-V1 already flags as coming due. MOST WORTH FIXING of the four.
  (2) spec.md:346 — a FOURTH spec-vs-code drift found during the fix:
      "negative Placement.Src.Min.Y" for scrollback, but no code produces a
      negative Src; scroll offset is grid-internal. BEYOND-V1 §7 lists three
      drifts, not four.
  (3) help_test.go:3-5 — two single-import declarations instead of a block.
  (4) A ctrl+q status-line segment would fit from ~92 columns at zero cost to
      the 80-column case. The re-reviewer also corrected the implementer's
      stated reason for omitting it: helpFor pre-accounts for quit, so quit is
      structurally unlosable and adding segments could never push it off.
      Conclusion still right (no room at 80), reason was wrong.
  Cost if wrong: four small known items ship, all recorded here and in the PR.

Ruling: LESSONS.md gets three new entries before the workspace is deleted —
  the CellAt aliasing pattern (three instances this plan), the last-column
  autowrap-toggle trap, and the go-test-caching trap. These are repo-specific
  hazards that cost a fix round each and would otherwise die with the ledger.
