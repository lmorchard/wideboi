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

## Agreed end state

The browser slice above landed in #217. It is the first step, not the final
width model. Each client now needs a display width and horizontal pan per pane,
independent of the server's PTY widths and of other clients. These apply in
cards and scrolling strip. A new pane starts at its PTY width with pan zero.
Changes survive layout snapshots and mode switches. Pan clamps when either
dimension changes. A display width may exceed the PTY width, showing blank
cells beyond the PTY until that client claims size.

Follow PTY width is one client-wide toggle, initially off. Turning it on makes
every display width track its PTY width. Turning it off freezes the current
widths. A manual width change turns follow off and edits the focused pane.

Size ownership from #184 extends to pane widths. A viewer's width edits affect
only its view. Claiming size takes ownership and applies that client's current
width for every live pane, together with its host dimensions, to the PTYs.
While it owns size, later width edits resize the matching PTY immediately.
Another client's claim takes ownership. Disconnecting the owner freezes PTY
sizes until another claim. A pane in follow mode contributes its current PTY
width to a claim.

Background output does not move a manually panned view. Sending a key or paste
reveals the pane cursor with the smallest necessary pan, including its position
after input takes effect. This is analogous to scrollback returning the sender
to live output on input. Mouse coordinates and the host cursor use the cropped
source rectangle.

In terminal control mode, H/L pan the focused pane left/right by 10 cells by
default; a positive `pan_step` config value changes the step. `f` toggles
Follow PTY width. Preserve case in configurable keys. h/l still move focus;
n still creates a pane; c still toggles layout.

Acceptance must attach two clients and establish that local width/pan edits
leave the other client's view and every PTY unchanged, a claim applies its
per-pane widths, owner edits resize immediately, and a second claim transfers
that ability. Check both layouts, source cropping, cursor/mouse coordinates,
and input revealing the cursor.
