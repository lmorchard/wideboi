# Jump-to-column, last-pane toggle, and card reordering Spec

**Goal:** Control-mode hotkeys to focus a column by position, flip back to the
previously focused pane, and move the focused column left/right in layout order.

**Source:** #80 (hotkeys to jump straight to terminals), #74 (hotkey to reorder cards)

## Current state

See `research.md`. Load-bearing facts:

- Bindings live in one table, `internal/keys/keys.go` `Bindings`. The router
  (`cmd/wideboi/router.go`) turns a binding into a `route`, and `main.go:525`
  dispatches that route. Only single `a-z` keys get a `ctrl+<key>` repeat form.
- `MsgVerb` takes no argument. `MsgFocusPane{PaneID}` already exists for mouse clicks.
- `layout.Strip` holds `columns` in order and a `focusIndex`. It has no reorder
  primitive and no focus history.
- The pane header shows ` [<paneID>] …`. Pane IDs are never reused, so they
  drift away from the pane's position.
- The status bar has no room left (77/79 cells at 80 columns), so new bindings
  go in the help overlay only.

## Desired end state

In control mode:

| Key | Action | Repeat (ctrl) |
| --- | --- | --- |
| `1`–`9` | focus the Nth column from the left | no |
| `0` | focus the last (rightmost) column | no |
| `tab` | focus the previously focused pane | no |
| `y` | move the focused column one place left, focus follows | `ctrl+y` |
| `u` | move the focused column one place right, focus follows | `ctrl+u` |

- A digit past the column count, `tab` with no valid previous pane, and a move
  at either end are all no-ops. Like any other binding, they leave control mode.
- Every pane header shows its 1-based position before the pane ID:
  ` 2 [7] ● claude ★`. Positions are shown for every column, including past 9.
- The help overlay gets `tab` on its own line, `y/u` together on one line, and
  one collapsed line for the digits: `0-9   focus a column by position, 0 the last`.
- README key table gains the new rows.

## Design decisions

- **Digits index position, not pane ID.**
  - **Why:** IDs are never reused and grow without bound. Position always stays
    within 1–9 for the first nine columns.
  - **Rejected:** pane ID (gappy, useless past 9). Replacing the ID in the header
    with the position (would touch the status line and what smoke parses).

- **The client translates a digit into `MsgFocusPane`.** A new
  `ActionFocusColumn` action with a `Column int` field (1–9, or `keys.LastColumn` = -1 for the `0` key). The
  router returns `routeFocusColumn{Column}`. `main` calls
  `client.FocusColumn(ctx, n)`, which resolves the pane ID from the client's
  strip (`PaneIDs()`, which is in strip order) and sends `MsgFocusPane`.
  - **Why:** reuses an existing message and its closed-pane guard
    (server.go ~316), so there is no protocol change. A mouse click already does
    exactly this.
  - **Rejected:** 10 new `VerbType`s (wire bloat). An index field on `MsgVerb`
    (every other verb would carry a field it ignores; messages.go's
    `MsgFocusPane` comment makes the same argument).

- **Previous focus is tracked in `layout.Strip`.** Every focus mutation calls a
  `noteFocusFrom(prev)` helper that records the outgoing pane ID when the
  focused pane actually changes: FocusLeft/Right, AddColumn, FocusPaneID.
  `KillPane` clears the record if it names the killed pane, and a kill does not
  itself record. New `Strip.FocusLast()` swaps to the recorded pane.
  `VerbFocusLast` (appended to `VerbType`) calls it.
  - **Why:** layout is the pure core and owns focus. That makes this
    unit-testable without a server, and it covers mouse clicks, smart jumps and
    digit jumps for free, since all of them end in `FocusPaneID`.
  - **Rejected:** tracking in `Server.handleClientMsg` by diffing before/after.
    That misses `onPaneExit`, and it cannot be tested in `layout`.
  - The client's strip records too, via `SyncColumns`. That is harmless: nothing
    on the client reads it.

- **Reordering is a swap with the neighbour: `Strip.MoveLeft/MoveRight`.**
  Widths travel with the column. `VerbMoveLeft`/`VerbMoveRight` are appended.
  The server does not call `resizePanesLocked`.
  - **Why:** the no-shrink premise says a pane's logical width is its column's
    width. A move changes neither, so nothing resizes (compare the
    `VerbToggleCards` comment, server.go ~307).
  - A move does not count as a focus change for last-pane purposes: the
    focused pane is the same.

- **`y`/`u` get a repeat form. The digits and `tab` do not.** Les's choice:
  moving a card several places in one chord (`C-b C-y C-y y`) is worth the
  weaker mnemonic. Jumps and toggles gain nothing from repeating.

- **Configurability:** `focus_last`, `move_left` and `move_right` join
  `validActions` and the unknown-action error list. They have no `BarGroup`,
  so they are help-only and the `BuildBindings` label switch leaves them alone. The digit bindings are fixed. They are
  not in `validActions` and cannot be remapped. The collision check still
  catches a user remapping another action onto a digit.

- **Help-overlay collapsing:** a `HelpGroup` field on `Binding`, mirroring
  `BarGroup`. Bindings that share one print a single overlay line whose text is
  the group string. The key label is the members' keys joined with `/`, which
  stays correct under remapping. A `HelpKey` field overrides the label, and the
  digits use `"0-9"`. Every row keeps its own `Long`: `TestTableIsWellFormed`
  requires one ("a binding may be overlay-only, but never undiscoverable").
  - **Rejected:** skipping bindings with an empty `Long`. That breaks the test's
    stated invariant.
  - **Amendment (plan Phase 4):** the overlay is exactly 24 rows at 80x24 today.
    These four new lines would clip it, so `h/l`, `j/k`, `o/p` and `y/u` are
    grouped as well, and a test pins "fits at 80x24".

## Patterns to follow

- Binding table entries and ActionName constants: `internal/keys/keys.go:24-40,117-160`.
- Verb dispatch: `internal/server/server.go:277-312` (append the new cases).
- Router action switch: `cmd/wideboi/router.go` `fire()`. Main dispatch: `cmd/wideboi/main.go:525`.
- Strip mutators with bounds guards: `internal/layout/layout.go:133-197`.
- Header formatting: `internal/client/client.go:498-523`.
- Test that every binding routes somewhere (LESSONS "a binding nobody typed"):
  extend the existing enumeration tests in `cmd/wideboi/router_test.go` /
  `internal/keys/keys_test.go` rather than hand-picking.
- Smoke cases assert through `focus_pane_id` (`scripts/smoke.py:62`), not on echoed input.

## What we're NOT doing

- No status-bar entries for the new bindings (bar is full).
- No renumbering or reuse of pane IDs, and no changes to the `focus: [pane N]` status line.
- No ctrl repeat for digits or `tab`, and no change to sticky semantics.
- No remapping of digit keys.
- No multi-level focus history (only the single previous pane).
- No mouse drag-to-reorder.
- No fix for the server/client duplicate placement computation (#47).
- No overlay redesign (scrolling, columns) beyond the grouping that keeps it at 24 rows.

## Open questions

None. The overlay height question was answered by counting (see the Phase 4 amendment).
