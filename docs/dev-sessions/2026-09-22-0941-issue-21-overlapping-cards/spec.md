# Issue #21: Cards: make them genuinely overlap (z-order), not just sit side by side

`CardStrategy` emits **non-overlapping** rects. Slivers are laid out contiguously with the focused card between them, and `Z` is 0 for slivers / 1 for the focused pane — but nothing actually occludes anything, so the z-fan is currently decoration.

"Panes that slip under each other" is the name, not yet the behaviour.

The insight that made cards cheap in the first place still holds: **cards are clipping plus z-order, not resizing.** An occluded card keeps its full logical width, so its child never learns it is partly covered — no `SIGWINCH`, no reflow, no redraw. `Placement` already carries `Dst`, `Src` and `Z`, and the compositor, animator and (future) mouse hit-testing all consume `[]Placement`.

One thing already in place: `internal/client/motion.go` sorts interpolated placements back-to-front by `Z` before drawing, because transient overlap during a transition is already real. `composeFrameLocked` itself still paints in slice order, so genuine overlap would want that honouring `Z` too.

## Additional tasks

1. "seems like unfocused terminals are frozen, but we want to see activity in them even if just in a sliver."
2. "I think we need to reintroduce side borders between cards, they're hard to distinguish now."
