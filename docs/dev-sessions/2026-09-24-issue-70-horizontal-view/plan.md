# Issue #70: per-client horizontal view plan

## Contract

Each client owns a display width and horizontal pan for each pane. These view
settings apply in cards and scrolling strip and do not change another client's
view. New panes start at the PTY width and pan zero. A client-wide Follow PTY
width toggle tracks all server widths; turning it off freezes the visible
widths. A manual width adjustment turns it off. A display width may exceed the
current PTY width so the client can stage a wider size claim.

The first attached client owns session size. A viewer's width edits stay local.
Claiming size transfers ownership and atomically applies the claimant's
per-pane widths and host dimensions. Subsequent owner width edits resize that
pane's PTY immediately. A later claim transfers ownership; an owner disconnect
leaves PTYs at their last sizes. A pane in Follow PTY mode claims its current
PTY width.

Background output leaves manual horizontal pan in place. Input to a pane
reveals its cursor with the smallest necessary pan, including the resulting
cursor position. This matches scrollback's return-to-live-on-input rule.

Terminal control mode uses H/L to pan left/right, default 10 cells per press,
clamped at the edges. `pan_step` is a positive config value. `f` toggles Follow
PTY width. Existing h/l focus, n new pane, and c layout keys remain.

## Implementation

1. Extend the versioned wire with widths on a size claim and an exact pane
   width request. The server accepts live width requests only from its size
   owner, validates widths, and broadcasts the resulting PTY geometry.
2. Keep PTY widths and local display widths separately in the terminal client.
   Compute both layout strategies with display widths, then offset each
   placement's source rectangle by its pane's pan. Make the compositor honor
   that rectangle, including wide glyphs and blank area beyond a narrower PTY.
3. Add the terminal bindings, positive `pan_step` config, cursor reveal on
   input, and follow toggle. Preserve case in custom key bindings.
4. Replace the browser's global card-width selector with a focused-pane width
   control and client-wide follow toggle. Use local widths in both layouts;
   preserve each pane's horizontal scroll position. Send exact width requests
   and include local widths in claims.
5. Test protocol round trips, two-client ownership and isolation, source crop
   and mouse/cursor translation, bindings/config, and browser interactions.
   Run `make check`, then open a PR.
