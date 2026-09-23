# Notes: #80 + #74 jump and reorder hotkeys

## What shipped (4 phases, one commit each)

1. `y`/`u` reorder the focused column (ctrl repeat). `Strip.MoveLeft/MoveRight`, `VerbMoveLeft/Right`; no resize.
2. `tab` flips to the previous pane. `Strip.lastFocusPaneID`, recorded by `noteFocusFrom` in FocusLeft/Right/AddColumn/FocusPaneID; KillPane forgets a dead record. `VerbFocusLast`.
3. `1`-`9`/`0` focus by position: `ActionFocusColumn`, `routeFocusColumn`, `Client.FocusColumn` → existing `MsgFocusPane` (no protocol change). Headers read ` 2 [7] …`. `Binding.HelpGroup`/`HelpKey`.
4. Help overlay grouping (h/l, j/k, o/p, y/u) so it fits 80x24; `TestHelpOverlayFitsAt80x24`. README + config.example.

## Decisions made during execution

- **`u` collided with the documented remap example** `scroll_up = "u"` (README, smoke config case, keys/client/config tests). All moved to `e`. **Compat break for users**: any config binding `y`, `u`, `tab` or a digit now fails at startup with a duplicate-key error. Worth a line in the PR.
- `TestHelpLinesNameEveryBinding` was rewritten for groups: a grouped binding needs its exact group line, and its key must appear in the label (or `HelpKey` covers it). Same invariant ("no binding undiscoverable"), different shape.
- `TestUnknownKeysExitControlModeWithAnyModifier` used `5` as an unbound key; now `g`.
- Digits are appended after `tab` in the table via `slices.Concat`, so overlay order reads naturally; the bar is unaffected (no BarGroup).

## Plan errors

- Phase 4 row count: predicted 26, actual 28 (forgot the border). Still lands on exactly 24 after grouping. **Zero slack**: the next overlay-only binding needs another group or a redesign. `TestHelpOverlayFitsAt80x24` will say so.
- Phase 3 smoke originally could not tell one move from two; fixed at plan time (position 2 check).

## Proven-red evidence

- Layout/server unit tests red vs no-op stubs; kill-forgets test red when the clear is deleted.
- Smoke: FocusLast sabotaged → last-pane case FAIL; MoveLeft sabotaged → reorder case FAIL; FocusColumn sabotaged → both FAIL. Each restored + rebuilt in the same command.

## Copilot review (PR #94)

- **Skipped (Les's call):** stale-snapshot race in `FocusColumn`. `C-b u` then `C-b 1` arriving before the reorder snapshot resolves position 1 against the old order. The window is one local round trip, and a human needs two prefix chords in it; mouse clicks already have the identical race against stale placements. Fixing it would mean a server-resolved `MsgFocusColumn`. Revisit together with clicks if it ever shows up.
- **Fixed:** "Column'th" comment wording.

## Not done / follow-ups

- Board transitions skipped: `gh` token lacks `project` scope (`gh auth refresh -s project`).
- Manual checks (below in plan.md) await Les.
