# Issue #207: overlapping web cards

## Scope

Add a browser-local card presentation for the persistent pane elements from
#199. Make cards the default, retain the horizontal strip as a choice, and
keep the existing terminal sizing protocol.

## Decisions

- The card layout computes a sticky, contiguous window around the focused
  pane. The focused element keeps its logical width; available space is shared
  among visible neighbors with a four-cell minimum sliver.
- Every pane element stays mounted. CSS absolute positioning and stacking
  expose slivers; a vertical spine and inset edge identify each card without
  changing canvas dimensions.
- The existing FLIP element animation handles focus, reorder, and mode changes.
  Reduced motion skips it. Returning to the strip reveals focus after the
  animation, when its final rectangle is available. During a focus slide, the
  old focused card keeps the top stacking level until the movement ends.
- The card layout uses the strip's existing viewport dimensions. Switching
  modes changes neither the reported cell grid nor PTY size. A stable scrollbar
  gutter preserves the available cell height, and card mode holds the strip's
  horizontal scroll at zero even if a smooth strip reveal was in progress.

## Verification

Unit tests cover crowded and narrow card geometry. Browser tests cover overlap,
stacking (including a paused right-to-left focus slide), pane identity, sliver
click-to-focus, pane-local mouse coordinates, crowded focus, mode changes
without resize messages, and reduced motion. The
geometry tests were observed failing when the minimum sliver width was
deliberately changed to an incorrect value, then restored.
