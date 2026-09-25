# Issue #70: terminal width and card width

## Current seam

The server's `layout.Strip` keeps each column's width and uses that width when
resizing its PTY. `MsgLayoutSnapshot.Columns[].Width` publishes it. The web
client previously used the same value for both card placement and canvas size.
This made a narrower card unable to show the right side of a wider terminal.

This slice leaves the server width authoritative. In the browser, the card
width selector chooses a local display width of terminal, 40, 60, or 80 cells,
capped at the current terminal column width. Card placement and the pane
element use that display width. The canvas uses `MsgPaneUpdate.Cols`, and its
own scroll viewport can pan across the full grid. Pointer coordinates are
translated from the moved canvas rectangle. The choice survives layout
snapshots, sends no resize or width verb, and is reset by a page reload.

Horizontal gestures inside a narrowed card move its inner viewport. The
scrolling layout still lets native horizontal gestures move the outer strip.
Vertical wheel behavior continues to distinguish live-grid panning from
terminal scrollback. The control is visible in card layout only; scrolling
layout continues to show the full server width of each pane.

## Work remaining on #70

- The terminal client still uses `layout.Column.Width` for both PTY width and
  local placement. A client-side display width must be modeled separately
  before it can pan a wider PTY grid horizontally. Keep the existing server
  width as the PTY width; do not let a local display width reach
  `resizePanesLocked`.
- Decide whether the card-width choice should be per pane and how it should
  follow focus or cursor movement. This slice uses one simple browser-local
  width setting and native horizontal scrolling.
- Add a two-client acceptance check once the server's #184 sizing policy
  lands: changing either client's card width and pan position must leave
  both PTYs and the other client's view unchanged.

The #184 work explicitly preserves the existing PTY column widths, so this
browser slice can merge independently. The remaining terminal-client layout
work should build on #184's final session-size contract.
