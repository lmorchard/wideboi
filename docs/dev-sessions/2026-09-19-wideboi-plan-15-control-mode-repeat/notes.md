# Plan 15 — session notes

**Status:** Shipped. PR #15 against `main`, 13 commits, `make check` green.
Built with subagent-driven execution: six tasks, a review per task, one
whole-branch review, one fix wave.

## What shipped

The spec and plan in this directory describe the design. What actually
landed matches them, with the deviations recorded below.

## The deviations, and why

Five things diverged from the plan during execution. All are in the code;
this is the reasoning, which is the part that does not survive in a diff.

**`layerLocked()` instead of an inline branch in `Draw`.** The plan said
put the overlay check above the wipe check and prove it with a smoke case
that raises help during a focus-switch wipe. That case cannot fail: the
wipe is eight frames (~128 ms) and the case samples cumulative output over
a full second, so the overlay appears within the window whether the
ordering is right or wrong. An implementer ran the sequence three times and
reported it rather than shipping it. Tightening the timing would have
bought a test that has to win a race. Extracting the precedence into a pure
predicate made it assertable without a terminal at all.

**Eight smoke sequences swept, not one.** The plan identified a single
stale case. In fact every `\x02<verb>\x1b` in the file was stale, because
an unmodified verb now exits the mode and the trailing escape reaches the
pane. Only one failed visibly — `case_partly_clipped_pane_keeps_full_width`
parsed `stty size` output and got corrupted — but all eight were wrong.

**`case_control_mode_is_sticky` rewritten, not retired.** It asserted the
semantics this plan removes, and passed anyway: `b"marker" in s.output()`
after `s.type("echo marker\r")` matches readline's echo, not the command's
effect. Now on the ctrl form, asserting through `focus_pane_id`.

**`parsePrefix` hardened.** Out of the plan's scope, folded in because the
branch disproved that function's own comment. `WIDEBOI_PREFIX=ctrl+i`
started wideboi with no way into control mode and no exit but a signal.

**`scripts/attachcheck.py` deflaked.** Belongs to PR #14's surface; touched
here only because its reattach case (fixed sleep, single sample) was flaky
2-in-4 and blocking this gate.

## The thing worth knowing that is not in this plan

**OSC 133 agent status has never worked.** `internal/server/term/grid.go`
registers a handler and matches `strings.HasPrefix(s, "A")`, but the pinned
emulator hands it the full payload — `"133;A"`. Every arm of that switch is
dead. No status glyph (`!`, `✓`, `✗`) can render and `VerbSmartJump` can
never find a target, since it shipped.

It survived because the only test that touched it asserted by grepping for
text it had typed into a pane. Deleted here; root cause, the verified fix,
and what the fix drags in are in `docs/BEYOND-V1.md` §6.

Deliberately not fixed in this plan: different subsystem, and with zero OSC
tests in `internal/server/term` the fix immediately surfaces the
glyph-rendering path and the `D;0`-vs-`D;n` exit-code semantics. It wants
its own plan, and the BEYOND-V1 row is written so that plan starts from
facts rather than rediscovery.

## What I would check first, next time in here

- **`j`/`k` are never typed at the wire.** They are the bindings this plan
  exists to add. `TestEveryPlainFormMatchesItself` proves they are live
  bindings and the negative-delta path clamps correctly, but nothing proves
  scrolling changes what a pane shows. One smoke case — `seq 1 200`, then
  `\x02k`, assert an off-screen line appears — closes it. That gap predates
  this plan but is more conspicuous now.
- **The prefix eats one repeat form.** `WIDEBOI_PREFIX=ctrl+l` makes
  `C-l C-l` forward a literal `C-l` rather than repeating focus-right,
  because the doubled-prefix escape hatch is checked before the table. That
  is correct and now tested and documented, but it is the kind of thing
  that reads as a bug in a report.
- **The overlay pauses repainting**, including after a resize. Cosmetic,
  and a wipe caught mid-animation resumes rather than being skipped.

## Process notes

Two diagnoses were wrong in ways worth remembering.

An implementer called the smoke failure "pre-existing, unrelated" after
reproducing it at the Task 4 commit. But that commit already contained
Tasks 1–4 — the check could only ever have shown "not caused by Task 5."
Testing at the *pre-plan* commit showed it passing 3/3 there and failing
3/3 after. **Reproduce against the commit the work branched from, not the
commit before your own change.**

The per-task reviews were clean six times; the whole-branch review then
found three Important items, every one of them in a seam between tasks or
in text no task's diff touched — a doc comment on the `control` field
describing the removed semantics, and a prefix/ctrl-form collision created
by Task 1 and Task 2 jointly, visible in neither diff. The final review is
not a formality.
