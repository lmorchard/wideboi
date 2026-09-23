# Notes: client-owned layout mode (#92, #47, #91)

## How this started

Les saw "narrow slivers, no equal-width cards" and suspected PR #73. It was scroll mode: an accidental `C-b c` in a long-lived session, and the mode was server-side session state, so it outlived every reattach. Screenshots settled it. Filed #91 (layout indicator) and #92 (this). Nothing in #73 needed reverting.

## 2026-09-23 ~10:00: two sessions collided in this worktree

A second Claude session resumed this dev session in the same worktree while this one was mid-Phase 1.

- **What it did:**
  - rebased onto origin/main (13801a9, #89 server reaper change) through a temporary WIP commit, then reset;
  - folded #91 into the spec and plan as Phase 4 (Les's choice: same PR, indicator right-aligned before the hint, hint drops first; the left edge is pinned by smoke.py's focus-digit column 13/14);
  - replaced notes.md.
- **Why its notes disagreed:** it recorded keys / router / `main.go` / attachcheck as not done because it read an older snapshot. They were done, in this session's working tree.
- **Consequence:** the first `make check` ×4 overlapped the rebase and can't be trusted. Red proofs were re-run on the rebased tree (below).
- Les told this session to carry on. **Lesson:** two agents in one worktree clobber each other silently, and neither can see the other's in-flight state. See the retro.

## Plan vs. reality

- **Rebased before planning.** `main` had gained #85 (change-only pane updates) and #80/#74 (new keys, `ActionFocusColumn`). Both were relevant.
- **Spec gap found at plan time: mirror pruning.** The client pruned mirrors by placement. That was safe only because #85 force-resends every pane after every snapshot, and a local toggle changes placements with neither. Added to the spec as a design decision (prune by column).
- **Mechanical test migration.** A Python pass moved 24 `Layout:` snapshot fields to `cli.SetLayoutMode(...)`, followed by hand edits for:
  - the three "mode from snapshot" tests (renamed and rewritten);
  - the empty-snapshot mode test (now `TestEmptySnapshotKeepsClientLayoutMode`);
  - a redundant `SetLayoutMode` in `TestEmptySnapshotClearsHiddenCardMarker`;
  - `motion_test` (mode set in `focusTo`, removed from the status loop and from `TestNoMotionWhenPlacementsAreUnchanged`);
  - the `snap` variable in `mouse_test`.
- **`keys_test.TestTableIsWellFormed` enumerates accepted Actions** and flagged the new one. It was working as intended, so I added `ActionToggleLayout` to its list. The router is the only non-test code that switches on `Action`.
- **Plan wording fix:** attachcheck's `--only` matches case *display names*. The cases are named "layout toggle affects only its own client", "attach honours its own layout flag" and "reattach starts from the configured layout".

## Red proofs (Phase 1)

First pass, before the collision. Client unit tests against the unmodified snapshot handling (new methods present, old `HandleServerMsg`):

- `TestSnapshotDoesNotChangeLayoutMode`: `layoutMode = 0 after a snapshot saying scroll, want the client's 1`. Placements followed the snapshot.
- `TestSetLayoutModeBeforeFirstSnapshot`: the first snapshot's zero-value `Layout` dragged a cards client to scroll.
- `TestToggleRevealsOffscreenPaneContent`: pane 3's region was entirely blank.
  - After the fix it read `│ONTENT-THREE`, because the card's left spine covers column 0.
  - The first assertion (`"CONT"`) therefore failed on its own wording. I changed it to `"ONTENT-THREE"`; pre-fix the region was blank, so the proof stands.
- `TestToggleLayoutSendsNothing` and `TestToggleLayoutArmsMotion` exercise new API, so their red was the compile failure. The same goes for the `keys_test` and `router_test` changes (`undefined: keys.ActionToggleLayout`).

attachcheck, all three against a `main` build (built over `./bin/wideboi`, run, rebuilt in one command):

- toggle: "toggling in client a moved client b's cursor from column 23 to 43; the layout is still session state"
- flag: "attach --layout scroll put the cursor at column 23, the same as the cards client; the flag was ignored"
- reattach: "reattached client's cursor is at column 43, want the configured layout's 23 (the toggled layout put it at 43)"

At 80x24 with three panes, cards puts the focused cursor at column 23 and scroll at 43.

Second pass, after the collision, on the rebased tree (base 13801a9):

- **Client tests, by sabotage.** Restoring `c.layoutMode = m.Layout` / `ApplyMode(m.Layout)` in the snapshot handler and the placement-keyed prune turns red:
  - `TestSnapshotDoesNotChangeLayoutMode`
  - `TestSetLayoutModeBeforeFirstSnapshot`
  - `TestToggleRevealsOffscreenPaneContent`
  - `TestEmptySnapshotKeepsClientLayoutMode`

  Restored from a copy; package green.
- **attachcheck** against an origin/main (13801a9) binary: all three FAIL. On the rebuilt branch binary: all three OK.

## Phase 2 (server side removed, #47)

- **Compiler as the red step.** Deleting `Layout` / `Placements` broke exactly the planned readers, in waves as each package compiled:
  - `client.go` fallback, `server.go` snapshot literal (×2), `transport/wire_test.go:160`, `layout_mode_test.go:91`;
  - then `main.go` `SetLayout`;
  - then `status_test.go` `s.layout`;
  - then `server_test.go` `snap.Placements`.

  No unlisted reader.
- **Plan miss in `TestServerVerbHandling`.** The plan said to rewrite its "2 placements after NewColumn" as `len(snap.Columns) != 2`, but the session starts with two panes, so there are 3 columns after the verb. The old assertion counted panes *visible* at 80 columns in scroll mode (2 either way), so it never showed the verb did anything. It now asserts 3, with a comment.
- **`TestReservedToggleVerbChangesNothing`** proven red by sabotage (`s.strip.GrowWidth(10)` in the ignored case → "pane 3 width 40 -> 50"), then restored.
- **Greps.**
  - The remaining `ComputePlacements` mentions in `server.go` (~444-450, ~499) explain why `resizePanesLocked` walks `PaneIDs` rather than placements. They're still true as rationale, so I kept them.
  - `s.cols` / `s.rows` still have readers (new-pane sizing, resize height), so they stay.
  - The remaining `.Layout` hits are config and flag parsing only.
- **`parseLayout` removal.** Its "typo is an error" property is still enforced by `config.Load` (`TestLoadInvalidLayout`). `config.Load` lowercases, so `"Cards"` is accepted there, which is a difference from `parseLayout`'s test, but `config.Load` was always the live path.
- **CLAUDE.md.** The premise pointer now names `TestReservedToggleVerbChangesNothing`, and the architecture fact says placements and mode are client-side. **Not fixed, observed:** the other two premise pointers are already stale from earlier PRs (`server.go:240` is the Run loop's stop case, `layout.go:247` is inside `RemoveColumn`). They're out of scope, so I recorded them in project memory.

## Phase 3 (docs)

- The README keys table never listed `c`. I added a row alongside the per-client paragraph, which was a small addition beyond the plan's wording changes.
- Left alone: the flag package's own usage strings (`main.go:96-97`, "layout strategy: cards or scroll"). wideboi prints its own help text, so those never show.

## Phase 4 (#91 status-line tag)

- **Tests went into a new `layout_tag_test.go`**, not `help_test.go`. They need `NewClient` (`SetLayoutMode` needs the strip), whereas `help_test.go`'s fixtures are struct literals.
- **Red proofs.**
  - `TestNormalStatusShowsLayoutMode`, `...DropsHintBeforeLayoutTag` and `TestToggleLayoutUpdatesStatusTag` all failed against the tagless line.
  - `TestLayoutModeString` failed to compile.
  - `...DropsLayoutTagWhenNothingFits` passed on arrival. It guards the end of the chain and can't be meaningfully red before the chain exists.
  - Sabotage (the chain cut down to its first element) turns `...DropsHintBeforeLayoutTag` red; restored.
- **smoke.** The tag asserts in `case_status_line_names_the_prefix` (default → `cards · C-b for commands`) and `case_scroll_mode_marks_off_screen_panes` (`--layout scroll` → `scroll · `) both FAIL on a Phase 3 (85763f3) binary, then pass on the branch.
- **Golden.** `startup.txt` gains exactly one word, `cards`. Reviewed, then regenerated with `make golden`. The plan didn't list this step.
- **Environment caveat, not fixed.** `smoke.py` pins neither `WIDEBOI_LAYOUT` nor `XDG_CONFIG_HOME`. A developer with `layout = "scroll"` in their own config would fail the default-tag assert (and the golden). This is the same exposure the suite already had for the card-layout assumptions. Worth pinning in a follow-up: CLAUDE.md says "pin the thing".

## Worth knowing

- `NewClient`'s zero-value mode is still scroll (protocol zero value). `cards` as the default comes from `config.Load`, applied by `runClient` via `SetLayoutMode`. `TestClientDefaultsToScrollLayout` pins the zero value on purpose.
- `SetLayoutMode` on the mode already set recomputes identical placements and doesn't touch motion, which is why `focusTo` can call it every time.

## PR prep (rebased onto #97–#101)

- `main` moved four times during the session. It was rebased each time, the last onto 8cb5c2f (#101). #99's multi-key bindings rebased cleanly, and the toggle row still reads `Key: "c", Action: ActionToggleLayout`.
- **SIGKILLs mid-`make check`** (`make: *** [check] Killed: 9`, a loop shell dying) came from another agent on the machine killing processes too broadly. Les confirmed this. It wasn't the suite.
- **verify-exit stray false positive, filed as #102.** Six concurrent ptycheck runs each treat any orphaned (ppid 1) copy of the binary as their own leak. Since #82, a signalled owner's setsid'd server sits at ppid 1 while it reaps, so a sibling's server gets reported. Every instance checked: the pid was in a sibling's `tracking` list, and that sibling reported all reaped.
  - `make verify-exit` alone: 0/8 on main, 0/8 on the branch.
  - Inside full `make check`: 4 of 13 branch runs vs 0/4 on main. Load-dependent; the branch adds three attach cases to the load.
- **Final gate:**
  - after the #99/#100 rebase, `make check` ×4 all green (smoke 36/36, attach 21/21);
  - after the #101 rebase, `make quick` green and one `make check`, whose only failure was the #102 signature.

## Copilot review on #103 (4 comments, all fixed)

- **Reserved verb still broadcast.** `needBroadcast = true` ran for every verb, so an old client's `C-b c` caused a snapshot plus a forced all-pane resend. Now skipped for `VerbToggleCards`. `TestReservedToggleVerbChangesNothing` also asserts the client transport stays empty; it was red ("sent protocol.MsgLayoutSnapshot") before the fix.
- **Harness inherited the developer's layout (3 comments: smoke, attachcheck, golden).** This was the environment caveat noted in Phase 4 as "not fixed". Fixed at the single spawn point, `ptylib.spawn_in_pty`: it pops `WIDEBOI_LAYOUT` and sets `XDG_CONFIG_HOME` to a never-created per-child temp path. `config.Load` ignores a missing *default* file (ENOENT only), so the built-in default stays under test instead of a pinned copy of it.
  - Red proof: with `WIDEBOI_LAYOUT=scroll`, and separately with an XDG config saying `layout = "scroll"`, the smoke tag case, golden and the attach flag case all FAILED before the pin and all passed after.
  - The CLAUDE.md testing bullet now lists the layout pin.
