# Issue #125: browser-owned scrolling strip

## Scope

Give each web pane a persistent custom element and canvas. Let the browser
clip and scroll a fixed-width strip. Keep terminal cell drawing, pane mirrors,
input routing, and the existing PTY sizing protocol intact.

## Decisions

- `PaneStore` reconstructs full pane state from updates and patches before Lit
  renders. This preserves updates that arrive immediately after a snapshot.
- `<wideboi-pane>` owns one canvas and its frame scheduling. The parent owns
  column order, focus, and the native horizontal scroll viewport.
- A pane's CSS width comes from the shared logical column width in cells;
  flex cannot shrink it. The strip's height reserves one title row and one
  status row, matching the existing client geometry.
- Pointer events route through the pane element and convert directly to its
  local cell coordinates. Vertical wheel input remains terminal scrollback;
  horizontal wheel input remains browser strip scrolling.
- Reordering uses a short element transform animation; focus uses native
  scroll-to-view. Both honor reduced-motion preferences.

## Verification

The browser test first failed because the old renderer had no pane elements.
It now checks fixed widths, focus visibility, independent canvases, local
mouse coordinates, click-to-focus, resize stability, and element identity
after reorder. Follow-up review tests also cover actual canvas focus after
pointer selection, visible row count against the reported grid, and focus and
input routing immediately after a pane closes. Unit tests cover patch
reconstruction, selection, idle frame scheduling, and redundant canvas resize.

## Later work

Card overlap, mobile pan/zoom, and independent terminal dimensions remain
separate design work under #70, #132, and #184. The current web client still
reports its viewport size on attach and resize.
