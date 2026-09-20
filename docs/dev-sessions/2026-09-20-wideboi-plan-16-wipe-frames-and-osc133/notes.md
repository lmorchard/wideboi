# Plan 16 — notes

Two features that were implemented, unit-tested, green, and completely
non-functional. Both for the same reason. Fixed, plus the test seam whose
absence let them ship.

## What landed

| Phase | What |
| --- | --- |
| 1 | `HostScreen` interface so `Draw` is callable from a test |
| 2 | The wipe composes real frames instead of two blank surfaces |
| 3 | OSC 133 parses its payload — **and** the status actually reaches the client |
| 4 | Smart jump ranks targets instead of taking the first map entry |
| 5 | Smoke case restored, asserting through `focus_pane_id` |
| 6 | `handleClientConnLoop` closes the transport it drops |
| 7 | `BEYOND-V1.md` and `LESSONS.md` made true |

Plus a pre-session commit reconciling `BEYOND-V1.md` §1/§2/§7 against what
Plans 7, 8, 9 and 12 actually shipped — the roadmap was describing several
landed things as unbuilt.

## The thing worth remembering

Both defects are the same shape: **a unit test that supplies its own inputs
proves the mechanism, not the wiring.** `WipeTransition` was tested against
frames the test wrote; its only caller passed blanks. The OSC 133 switch was
tested against nothing and matched `"A"` against `"133;A"`.

The root enabler was mechanical, not carelessness: **`Draw` took a concrete
`*uv.TerminalScreen`**, which a unit test cannot cheaply build. So no test
ever called the function that assembles the screen. Narrowing it to an
interface was most of the fix. Written up in `LESSONS.md`.

Corollary that bit twice here: **check a new test fails for the reason you
think.** Two planned assertions passed *before* the fix —
`TestMalformedOSC133DoesNotLatch` (status is `Working` right after any write
either way) and the `output start` OSC row (`Write`'s activity fallback sets
`Working` regardless). Both were replaced or annotated. A test written against
a broken system and passing on arrival is evidence about the test.

## Surprises

**Fixing the OSC handler was not sufficient.** With all 9 unit rows green, no
glyph reached the wire. `PaneStatuses` rides on `MsgLayoutSnapshot`, which only
went out for verbs, spawns and kills; the 33 ms frame ticker sends cells, not
statuses. So a status change sat invisible until you pressed a verb key. Added
`broadcastLayoutIfStatusChanged` with change detection. **Fixing a dead
component can just reveal the next dead link in the chain** — worth expecting
next time rather than being surprised.

**A stale row in `BEYOND-V1` §6.** Reading the section end to end, as Phase 7
asked, turned up "`transport.SendServer` is called under `s.mu`" — not true
today. Both call sites unlock before sending, and the `broadcastLayoutLocked`
it names does not exist as a function. Corrected, with an explicit note that
the rest of that wedge chain was **not** re-derived.

**The wipe costs ~3.06x a snap**, not the ~1.9x `BEYOND-V1` §1's table
predicts. Not a regression: the table models a diffed change set and the
shipped `WipeTransition` blits whole clipped rects. Same order, different
constant. Recorded in §1.

## What the Copilot review caught

Three comments on PR #16, all three worth acting on — and two of them were
**my fix being half a fix**, which is a useful calibration on this session's
own lesson.

1. **Resize during an *active* wipe.** My guard only covered the pending
   window. Once `Draw` realized the transition, `activeWipe` held frames sized
   to the old viewport while `SendResize` moved `c.cols`/`c.rows` on, so a
   resize after the first frame kept painting the previous layout for the rest
   of the transition. Fixed with `WipeTransition.Fits` plus a drop in `Draw`.
   My own `TestResizeDuringPendingWipeSnapsInsteadOfAnimating` covered exactly
   the half I had handled — a tidy example of a test that confirms the fix you
   wrote rather than the behaviour you wanted.
2. **`lastStatuses` marked clean before delivery.** `SendServer` returns false
   on a full buffer. Status broadcasts are edge-triggered, so unlike the 33 ms
   pane updates they never repeat — a dropped snapshot would leave the client
   stale until an unrelated later change. Now `broadcastLayout` only records
   the glyph set once a send succeeded, and the next tick retries. This one
   was introduced by my Phase 3 addition, not pre-existing.
3. **A stale sentence in §2** still saying the status glyph does not work.

Both code fixes were verified to **fail against the pre-fix code** before
being accepted, using `git checkout HEAD -- <files>` with the working copies
staged aside — not `git stash`, for the shared-stack reason above.

## Known nuances, deliberately not chased

- **Two snapshots between draws makes frame A the intermediate state.** If
  focus moves 1→2→3 with no `Draw` in between, `pendingWipe.from` is
  overwritten, so the wipe starts from a frame the user never saw. Bounded,
  never blank, needs two snapshots inside 16 ms. Fix if it ever shows: only
  overwrite `from` when `c.pendingWipe == nil`. Three lines, but it wants a
  test, and there wasn't one to hang it on.
- **`VerbKillPane` changes focus *and* prunes a mirror in the same message**,
  so frame A can show the killed pane blank for ~128 ms.
- **Wide glyph at the wipe's split column is still untested** — and now
  reachable for the first time, since the frames carry real content. Flagged
  in §1.
- **`realizePendingWipeLocked` runs before the layer switch**, so a wipe armed
  while the help overlay is up waits rather than being skipped. Pre-existing
  behaviour (Plan 15 notes it); preserved deliberately.

## Environment gotchas

- **Git signing needs the Bitwarden agent.** `SSH_AUTH_SOCK` is not set in
  Claude Code's shell, so commits fail with `Couldn't get agent socket`. Use
  `SSH_AUTH_SOCK=/Users/lmorchard/.bitwarden-ssh-agent.sock git commit`. Saved
  to project memory. Don't go hunting for the socket — probing `lsof` on the
  agent pid gets denied as credential exploration, correctly.
- **`git config --local` inside a linked worktree writes to the *shared*
  `.git/config`**, so it silently affects the main checkout. True per-worktree
  scope needs `extensions.worktreeConfig true` + `git config --worktree`. Both
  were used briefly and fully reverted; the repo config is back to original.
- **Don't `git stash` in a worktree** — the stack is shared. Phase 5 needed to
  temporarily revert one file and used `git checkout <sha> -- <path>` instead.
- The skill symlinks under `~/.claude/skills/` were all dangling at session
  start (repo had moved to `devel/mine/`). Les fixed them mid-session.

## Where to pick up

`BEYOND-V1.md` §6 now carries three new rows that are all the same audit
finding: **`CardStrategy` is built, property-tested, and unreachable** — no
verb, key or config calls `Strip.SetStrategy`. That is likely the cheapest
real feature left; the hard part is written. §2 records the two open questions
it needs first (sliver chrome vs. real content, and what happens to cards that
fall off the viewport).

Also open from this session: the `SendServer`/`s.mu` wedge chain deserves one
focused re-derivation now that its headline claim is known false.
