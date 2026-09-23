# Notes

## Phase 1 (keys + router)

- Red step done in two moves: first the API shape only (`[]string` signature,
  `CtrlForms`, first-key stub), so the new tests failed on behaviour rather than
  on compile. Failures: `Aliases=["left"]` where `[g]` was wanted; `left` still
  fired FocusLeft; `g`/`ctrl+g` were unknown; `[]` panicked the stub (index 0).
- `TestBuildBindingsMoveGroupSkipsUnbound` was never seen red on its own. Under
  the stub it was masked by the panic, and the label code needed no change:
  `Sprintf` of an empty key already drops the letter. Kept as a regression guard.
- Existing duplicate-key tests pass unchanged. Every collision message still
  begins "duplicate key".
- Router table test subtests are now named after the ctrl form (`ctrl+h`), not
  `h/ctrl`, since a binding can have several.

## Phase 2 (config)

- Red: the three new Load tests failed on go-toml's own "cannot decode TOML
  array into string", and the example test failed on focus_left/right with
  empty Aliases. That is the replace-semantics compat break working exactly
  as specified.
- The DeepEqual guard prints per-binding diffs rather than one giant struct
  dump, so the next drift points straight at the row.

## Phase 3 (smoke + README)

- Smoke case red with `focus_right = "l"` ("alias g for focus_right left focus
  on pane 1"), green with `["l", "g"]`.
- `make check` 4/4 green, no flake this time.
- Manual Phase 2 check: Les confirmed `C-b g` / `C-b ctrl+g ctrl+g` work.

## For the PR description: compat breaks

- A config that sets `focus_left`/`focus_right` to a single key (including a
  copy of the old `config.example.toml`) loses the arrow keys. Fix: list them.
- Remapping a verb onto `left`/`right` used to succeed silently (the arrow was
  dropped from focus_left/right); it is now an error naming focus_left/right.
- Collision error wording changed; every variant still starts "duplicate key".

## PR #99 Copilot review

- Fixed (medium): unbinding half of a help pair (`focus_right = []`) left the
  survivor on the shared "left / right" line. BuildBindings now clears
  HelpGroup/HelpKey on the survivors so they show their own Long. Line count is
  unchanged, so the 80x24 overlay budget holds. Test was red on the missing own
  line first; my first assertion ("left / right" anywhere) was too broad, since
  y/u's line legitimately says it, and was narrowed to the focus pair's text.
- Fixed (low ×2): README and config.example.toml promised a ctrl repeat on every
  a-z key; toggle_cards is NoRepeat. Exception now documented.
- Process slip: first squash was soft-reset onto an origin/main that had gained
  #98 without rebasing, so the pushed commit deleted LICENSE. Caught in the
  diffstat before the PR opened; redone from the pre-squash commit.
