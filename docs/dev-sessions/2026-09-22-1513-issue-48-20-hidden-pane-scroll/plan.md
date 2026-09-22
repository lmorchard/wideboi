# Plan

1. **#48 (client).** Invert `TestScrollModeNeverShowsAMarker` into a test that
   scroll mode *does* show `+N` for fully off-screen panes. Watch it fail, then
   remove the card-only gate in `drawHiddenMarkersLocked`. Update the comments
   that describe the gate.
2. **#20 (layout).** Add layout tests: (a) focus moving inside the window
   leaves the visible set unchanged; (b) the window slides once focus reaches the
   margin; (c) a rapid property: focus is always placed, the placed cards are
   contiguous, and neighbours are placed when the window has room for them.
   Watch (a) fail against the re-centring code. Then add `Strip.cardFirst`
   and replace `visibleSides` with a windowed version.
3. Update comments in `card.go` and `client.go` that point at #20 as
   unbuilt. `make check`, run 4x (the property test and the animation paths are
   timing-free, but the suite is not).
