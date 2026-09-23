# Research: jump-to-column and reorder hotkeys (#80, #74)

## Key bindings

- `internal/keys/keys.go` is the single source of truth: `Bindings` table (status-bar
  order), `Action` kinds (`ActionVerb`, `ActionScroll`, `ActionQuit`, `ActionDetach`,
  `ActionHelp`, `ActionExit`), `ActionName*` constants for config remapping.
- `Binding.CtrlForm()` gives a `ctrl+<letter>` repeat form only for single `a-z` keys
  not in `Reserved` (`i`, `m`, `[`) and without `NoRepeat`. Digits and punctuation get
  no repeat form.
- Letters in use: `h l j k n w o p x a ? d c q` + `esc`. Reserved: `i m [`. `b` is the
  default prefix, so `ctrl+b` repeat would collide. Free: `e f g r s t u v y z`.
- `BuildBindings` (keys.go ~293) validates `[keys]` config: `validActions` map, a
  hardcoded list of names in the unknown-action error, collision check, and a
  `switch` that rebuilds `BarGroup` labels per action name. New actions must be added
  to all three.
- `BarItemsFor` collapses bindings by `BarGroup`; an empty group means help-overlay only.
  Comment at keys.go ~143: the attached bar is 77/79 cells at 80 cols, no room for more
  entries (`TestControlHelpFitsEveryEntryAt80Columns`).

## Routing

- `cmd/wideboi/router.go` `route()` walks bindings; ctrl form -> sticky, plain -> leave
  control mode. `fire()` maps `Action` to a `route` (`routeVerb` carries a `Verb`).
- `cmd/wideboi/main.go:525` dispatches `routeVerb` -> `client.SendVerb`.

## Help overlay

- `internal/client/help.go` `helpLines` prints one line per binding (`%-4s  %s`,
  `b.Key`, `b.Long`) plus 6 lines of footer. No grouping.

## Server

- `protocol.MsgVerb{Verb}` has no argument. `VerbType` values are wire values;
  append only (messages.go:18 comment).
- `protocol.MsgFocusPane{PaneID}` exists (mouse click focus), handled at
  server.go ~316 with a guard for closed panes.
- `server.go` `handleClientMsg` dispatches verbs onto `s.strip` then broadcasts layout.
  Focus changes happen via: FocusLeft/Right, NewColumn (AddColumn focuses new),
  KillPane, SmartJump, MsgFocusPane, and `onPaneExit` (outside handleClientMsg).

## Layout

- `internal/layout/layout.go` `Strip{columns []Column, focusIndex, scrollX, cardFirst}`.
  Focus is an index. `FocusPaneID(id)`, `PaneIDs()` (in strip order), `KillPane`.
  No reorder/swap primitive exists.
- Client keeps its own strip via `SyncColumns(m.Columns, m.FocusPaneID)`
  (client.go:145); column order in `MsgLayoutSnapshot.Columns` is strip order.

## Chrome

- Pane header (client.go:498-523): ` [<paneID>] <glyph> <title> ★`. Pane IDs are
  monotonically assigned (`nextPaneID++`), never reused, so they do not match
  position after kills/inserts. Normal status line: `focus: [pane N ★]`.
- Smoke harness `focus_pane_id` parses the status line to find the focused pane.
