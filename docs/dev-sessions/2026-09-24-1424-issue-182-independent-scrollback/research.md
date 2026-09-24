# Codebase Research: Issue #182 (Independent Scrollback per Client)

## 1. Scrollback Offset Flow and Storage

### Client Input Paths
- **Terminal Keypress**:
  - `cmd/wideboi/router.go:147-148`: `router.fire` maps `keys.ActionScroll` to `route{Kind: routeScroll, Scroll: b.Scroll}`.
  - `cmd/wideboi/main.go:806-807`: Event loop receives `routeScroll` and calls `cli.SendScroll(ctx, act.Scroll)`.
  - `internal/client/client.go:1116-1124`: `Client.SendScroll` reads `c.focusPaneID` and sends `protocol.MsgScroll{PaneID: focusedID, Delta: delta}` over the transport.
- **Terminal Mouse Wheel**:
  - `internal/client/mouse.go:203-220`: On `uv.MouseWheelEvent`, hit tests pointer with `c.contentHitLocked(pt)`. If pane not in `mouseTracking` mode, maps `uv.MouseWheelUp` to `Delta: 3` and `uv.MouseWheelDown` to `Delta: -3`.
  - `internal/client/mouse.go:224-226`: Dispatches `protocol.MsgScroll` to `c.transport.SendClient(ctx, msg)`.
- **Web Mouse Wheel & Keys**:
  - `web/src/wideboi-app.ts:452-467`: Wheel event listener hit tests cell coordinates (`pixelsToCells` -> `getPaneHit`). If `hit.paneID > 0`, maps `e.deltaY > 0 ? -3 : 3` and calls `this.client.send({ case: 'scroll', value: { paneId: hit.paneID, delta } })`.
  - `web/src/wideboi-app.ts:370-374`: In prefix mode (`Ctrl-b`), key `j` sends `delta: -10` and `k` sends `delta: 10`.

### Wire Protocol
- `internal/protocol/messages.go:205-208`: `MsgScroll` struct defines `PaneID int` and `Delta int`.
- `internal/protocol/codec.go:32-33, 64-65`: Codec translates `MsgScroll` to/from protobuf `wirepb.ClientMessage_Scroll` (`internal/protocol/wirepb/wideboi.proto:186-189, 208`).

### Server Handler & Offset Storage / Mutation
- `internal/server/server.go:433-437`: In `Server.handleClientMsg`, `case protocol.MsgScroll` looks up pane `p` in `s.panes` and calls:
  ```go
  p.SetScrollOffset(p.ScrollOffset() + m.Delta)
  ```
- `internal/server/pane.go:344-345`: `p.ScrollOffset()` and `p.SetScrollOffset(offset)` delegate directly to `p.grid.ScrollOffset()` and `p.grid.SetScrollOffset(offset)`.
- `internal/server/term/grid.go:157`: Offset is stored in `vtGrid.scrollOffset` as an `atomic.Int32`.
- `internal/server/term/grid.go:537-548`: `(g *vtGrid).SetScrollOffset(offset int)` clamps `offset` between `0` and `g.em.ScrollbackLen()`. It calls `g.scrollOffset.Swap(int32(offset))` and advances `g.generation.Add(1)` whenever the offset changes value.

### Render and Broadcast Paths
- `internal/server/server.go:306-322`: Frame ticker in `Server.Run` triggers `s.broadcastPaneUpdates(ctx, false)` every 33ms.
- `internal/server/server.go:859-870`: Generation change (`gen != last`) flags the pane as dirty for clients.
- `internal/server/server.go:878`: Calls `pane.UpdateMessage()`.
- `internal/server/pane.go:298-300`: `Pane.UpdateMessage` allocates a `uv.ScreenBuffer` and calls `p.Draw(buf, image.Rect(0, 0, cols, rows))`.
- `internal/server/term/grid.go:556-581`: `(g *vtGrid).Draw` locks `g.writeResizeMu`, loads `offset := int(g.scrollOffset.Load())` and `sbLen := g.em.ScrollbackLen()`. For each line `y`:
  - `sbY := (sbLen - offset) + y`.
  - When `sbY < sbLen && sbY >= 0`, extracts history cells via `g.em.ScrollbackCellAt(x, sbY)`.
  - Otherwise, extracts live screen cells via `g.em.CellAt(x, sbY-sbLen)`.
- `internal/server/server.go:905-914`: Update is packaged as a `MsgPanePatch` or full `MsgPaneUpdate` and sent to each target transport.

---

## 2. Per-Client State Tracking and Lifecycle in `internal/server`

### Current State
- `transports []transport.Transport`: Active client connections (`server.go:36`).
- `paneGens map[transport.Transport]map[int]uint64`: Last sent grid generation per client per pane (`server.go:57`).
- `paneFrames map[transport.Transport]map[int]protocol.MsgPaneUpdate`: Last accepted full snapshot per client per pane, used as patch baseline (`server.go:60`).
- `clientSizes map[transport.Transport]protocol.MsgResize`: Viewport size per client (`server.go:65`).
- `pendingPaneCreated map[transport.Transport][]int` (`server.go:46`).
- `pendingCreationSnapshot map[transport.Transport]bool` (`server.go:47`).
- `owner transport.Transport` (`server.go:80`).

### Client Lifecycle
- **Attach**: Adds to `transports`. Client sends `MsgAttach`. Missing `paneGens` entries cause initial full pane updates.
- **Detach**: Client sends `MsgDetach`. Handled cleanly in `dropClient(ctx, tp)`.
- **Reconnect**: New `Transport` connected; gets full layout snapshot and full pane updates.
- **Cleanup**: `removeTransportLocked(tp)` cleans maps for `tp` under `s.mu`.

---

## 3. Pane Update Dispatch and Patching

- Today, `pane.UpdateMessage()` generates a SINGLE `protocol.MsgPaneUpdate` for all clients because the pane's scroll offset is global on `vtGrid`.
- `broadcastPaneUpdates` generates patches against each client's specific baseline `s.paneFrames[tp][id]`.
- If scroll offset becomes per-client, each client viewing a pane at a different scroll offset will need an update rendered at its own scroll offset!
- If client A and client B have different scroll offsets for pane P, `pane.UpdateMessage()` can no longer be a single call for all clients, OR `UpdateMessage` needs to take the scroll offset (or `UpdateMessageForOffset(offset)`), and generations/caching need to account for per-client offsets.
