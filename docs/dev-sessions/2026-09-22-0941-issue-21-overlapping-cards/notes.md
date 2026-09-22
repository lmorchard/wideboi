# Notes

- Real overlapping cards implemented by allowing `CardStrategy` to extend `Dst` and `Src` to their full column widths, rather than clipping them to the narrow sliver gap.
- Since multiple panes now occupy the same physical coordinates, they are drawn back-to-front by stable-sorting placements by `Z` index in `client.go` `composeFrameLocked`. This mirrors the logic `motion.go` already had for transitions.
- Dropped the `PlacementSliver` concept entirely, since occluded cards now simply draw their real terminal output (`PlacementFull`), meaning unfocused cards visibly update rather than looking "frozen".
- Since titles were originally only displayed by the sliver drawing logic, `composeFrameLocked` has been updated to include `paneTitles` in the standard top-row header.
- Re-introduced side borders for cards (`│` and `┃`). In `LayoutCards`, each pane gets a border drawn on its visible edge (left edge for overlapping cards, right edge additionally for the focused card) to maintain contrast between the overlapping panes.
- Adapted unit tests to assert `Z=1` instead of `PlacementSliver`, and checked for borders and overlapping content.