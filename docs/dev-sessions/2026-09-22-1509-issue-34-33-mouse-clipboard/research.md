# Research: mouse (#34) and OSC 52 clipboard (#33)

## Current state

- **No mouse handling anywhere.** No `SetMouseMode` call; `cmd/wideboi/main.go`
  event loops (`runAttach` ~L300, `run` ~L420) switch only on
  `uv.WindowSizeEvent` and `uv.KeyPressEvent`.
- **No clipboard code.** No OSC 52, no `pbcopy`.
- **Z order is already honoured by the compositor.** `composeFrameLocked`
  (`internal/client/client.go:~465`) copies placements and stable-sorts by
  `Z` ascending before painting. The issue text ("paints in slice order") is
  stale. Hit-testing wants the reverse: highest Z first.
- Placements live in `Client.placements` (settled) and
  `currentPlacementsLocked()` (interpolated while `c.motion != nil`).
- Chrome rows: row 0 is the header row for every placement; panes occupy
  `Dst.Min.Y = 1` … `rows-2`; row `rows-1` is the status bar.
- Slivers (`PlacementSliver`) are drawn as chrome (glyph/title + spine), not content.
- Composed output: `Draw` renders into `c.stagingScreen` then copies to
  `c.lastRenderedScreen` (an `offscreenHostScreen`, `compose.Surface`).
  In-process mode draws panes via `srv.DrawPane`; attach mode via mirrors.
  `lastRenderedScreen` holds what is on screen in both modes.

## Focus by pane ID

- `layout.Strip.FocusPaneID(id)` exists (`internal/layout/layout.go:233`),
  used by smart jump (`internal/server/server.go:~240`).
- No client→server message focuses a specific pane. Verbs are relative.
  Client messages: `MsgAttach`, `MsgVerb`, `MsgInput`, `MsgResize`, `MsgScroll`
  (`internal/protocol/messages.go`); gob registration at
  `internal/transport/socket.go:75-78`. `protocol.TestWireTypesCarryNoInterfaces`
  walks message types.
- `MsgScroll{PaneID, Delta}` already targets a pane by ID.

## Pinned ultraviolet (v0.0.0-20260910203606)

- `TerminalScreen.SetMouseMode(uv.MouseModeDrag)` (DEC 1002: press, release,
  drag) and `SetMouseEncoding(uv.MouseEncodingSGR)`; both buffered, flushed on `Flush`.
- `TerminalScreen.Reset()` (called by `Terminal.Stop`, `terminal.go:355`)
  disables mouse mode/encoding if set. Restore re-enables. So teardown is covered.
- Events: `MouseClickEvent`, `MouseReleaseEvent`, `MouseMotionEvent`,
  `MouseWheelEvent`, each `.Mouse()` → `uv.Mouse{X, Y, Button, Mod}`, zero-based cells.
- `TerminalScreen.WriteString` appends raw bytes to the same buffer, flushed on `Flush`.
- `uv` deliberately drops OSC from cell content (`styled.go:~118`) because
  cells are repainted, so OSC 52 must be written out-of-band, not via a cell.

## Pinned x/ansi

- `ansi.SetSystemClipboard(s)` → `ESC ] 52 ; c ; <base64> BEL/ST`.

## Pinned x/vt

- `SafeEmulator.SendMouse(uv.MouseEvent)` exists and encodes per the child's
  requested mouse mode (X10/SGR only). Forwarding to children is feasible
  but not needed now.

## Test surfaces

- `internal/client/*_test.go` drive `Client` directly with `HandleServerMsg`
  and an `offscreenHostScreen`.
- `cmd/wideboi/router_test.go` tests key routing without a terminal.
- `scripts/smoke.py` asserts on wire bytes under a pty; `scripts/attachcheck.py`
  covers the socket transport.
