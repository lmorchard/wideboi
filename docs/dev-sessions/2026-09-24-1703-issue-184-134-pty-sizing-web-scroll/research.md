# Research: Multi-Client PTY Sizing (#184) and Web UI Vertical Viewport/Scroll (#134)

### 1. Tracking Client Dimensions and Invocation of `recomputeSessionSizeLocked` and `resizePanesLocked`

- **Dimension tracking:**
  - `Server.clientSizes` is a `map[transport.Transport]protocol.MsgResize` (`internal/server/server.go:91`), initialized in `NewServer` (`internal/server/server.go:208`).
  - Entries are added or updated in `handleClientMsg` on receiving `protocol.MsgAttach` (`internal/server/server.go:428`) or `protocol.MsgResize` (`internal/server/server.go:463`), provided `Cols > 0 && Rows > 0`.
  - Entries are removed on disconnect in `removeClientLocked` (`internal/server/server.go:356`).
  - `recomputeSessionSizeLocked` (`internal/server/server.go:701–721`) iterates over `s.clientSizes` and sets `s.cols` and `s.rows` to the minimum `Cols` and `Rows` across all attached clients.

- **Call paths invoking `recomputeSessionSizeLocked`:**
  1. `removeClientLocked`: when a client disconnects (`internal/server/server.go:361`).
  2. `handleClientMsg` -> `case protocol.MsgAttach`: when an attach message contains `Cols > 0 && Rows > 0` (`internal/server/server.go:429`).
  3. `handleClientMsg` -> `case protocol.MsgResize`: when a resize message contains `Cols > 0 && Rows > 0` (`internal/server/server.go:465`).

- **Call paths invoking `resizePanesLocked`:**
  1. `removeClientLocked`: following session size recomputation on client disconnect (`internal/server/server.go:362`).
  2. `handleClientMsg` -> `case protocol.MsgAttach`: unconditionally during attach handling (`internal/server/server.go:454`).
  3. `handleClientMsg` -> `case protocol.MsgResize`: if `s.cols != oldCols || s.rows != oldRows` after recomputation (`internal/server/server.go:466–469`).
  4. `handleClientMsg` -> `case protocol.MsgVerb`:
     - `protocol.VerbNewColumn` (`internal/server/server.go:478`)
     - `protocol.VerbCycleWidth` (`internal/server/server.go:481`)
     - `protocol.VerbGrowWidth` (`internal/server/server.go:484`)
     - `protocol.VerbShrinkWidth` (`internal/server/server.go:487`)
     - `protocol.VerbKillPane` (`internal/server/server.go:498`)
  5. `onPaneExit`: when a pane child process exits and its pane is destroyed (`internal/server/server.go:680`).

---

### 2. Client Size Reporting and Wire Messages (CLI and Web)

- **Wire messages:**
  - Protocol Buffer schema `wideboi.proto`:
    - `ClientMessage.msg.attach` -> `MsgAttach { int32 cols = 1; int32 rows = 2; }` (`internal/protocol/wirepb/wideboi.proto:154–157, 217`).
    - `ClientMessage.msg.resize` -> `MsgResize { int32 cols = 1; int32 rows = 2; }` (`internal/protocol/wirepb/wideboi.proto:190–193, 221`).
  - Go types: `protocol.MsgAttach` and `protocol.MsgResize` (`internal/protocol/messages.go:160, 166`), marshaled via `protocol.MarshalClient` (`internal/protocol/codec.go:22–25, 34–37`).
  - Web TypeScript types: generated `MsgAttach` and `MsgResize` (`web/src/gen/internal/protocol/wirepb/wideboi_pb.ts:505, 660`), marshaled to binary using `toBinary(ClientMessageSchema, ...)` in `WideboiClient.send` (`web/src/client.ts:85–92`).

- **CLI client reporting:**
  - Initial size:
    - Queried from host terminal via `t.GetSize()` (`cmd/wideboi/main.go:659`), defaulting to `80x24` if non-positive or error (`cmd/wideboi/main.go:661`).
    - Instantiated via `client.NewClient(..., width, height, ...)` (`cmd/wideboi/main.go:664`, storing `c.cols, c.rows` at `internal/client/client.go:116–117`).
    - Sent via `cli.Attach(ctx)` (`cmd/wideboi/main.go:670`, and upon reconnection at line 746), which executes `c.transport.SendClient(ctx, protocol.MsgAttach{Cols: c.cols, Rows: c.rows})` (`internal/client/client.go:132–134`).
  - Resize events:
    - Received over the terminal event loop as `uv.WindowSizeEvent` (`cmd/wideboi/main.go:774`).
    - Updates local screen buffer `scr.Resize(ev.Width, ev.Height)` (`cmd/wideboi/main.go:777`).
    - Calls `cli.SendResize(ctx, ev.Width, ev.Height)` (`cmd/wideboi/main.go:780`).
    - `SendResize` updates `c.cols, c.rows`, resets active animations, recomputes local placements (`internal/client/client.go:1171–1181`), and sends `protocol.MsgResize{Cols: cols, Rows: rows}` (`internal/client/client.go:1183`).

- **Web client reporting:**
  - Size calculation:
    - Calculated in `getGridSize()` (`web/src/wideboi-app.ts:305–309`):
      `cols = Math.floor(this.paneStrip.clientWidth / this.cellWidth)`
      `rows = Math.floor(this.paneStrip.clientHeight / CELL_HEIGHT) + 2`
  - Initial size:
    - On WebSocket connection open `client.onConnect`, waits for `this.updateComplete` and invokes `this.sendAttach()` (`web/src/wideboi-app.ts:439–441`).
    - `sendAttach()` calls `this.getGridSize()` and dispatches `{ case: 'attach', value: { cols: size.cols, rows: size.rows } }` (`web/src/wideboi-app.ts:740–745`).
  - Resize events:
    - Handled via a `ResizeObserver` attached to `this.paneStrip` (`web/src/wideboi-app.ts:324–330, 336, 347`).
    - On dimension change, invokes `sendResizeIfChanged()` (`web/src/wideboi-app.ts:329; also called at 488, 540`).
    - `sendResizeIfChanged()` compares `getGridSize()` to `this.lastSentSize` and sends `{ case: 'resize', value: size }` when changed (`web/src/wideboi-app.ts:311–319`).

---

### 3. Vertical Viewport Height and Vertical Positioning of Pane Content

- **CLI client:**
  - Viewport allocation:
    - Host viewport height is stored in `c.rows` (`internal/client/client.go:60`).
    - Server pane emulator row count is set to `layout.AvailHeight(s.rows)` (`internal/server/server.go:792`), computed as `max(viewportHeight - 2, 1)` (`internal/layout/layout.go:320–322`), reserving 1 row for top headers and 1 row for the bottom status bar.
    - Placement rectangles define vertical boundaries: `Src = (0, 0, w, availHeight)` and `Dst = (x0, 1, x1, 1 + availHeight)` (`internal/layout/layout.go:374, 381`; `internal/layout/card.go:73–74`).
  - Rendering vertical positions:
    - Header bar: drawn at row `Y = 0` (`internal/client/client.go:572–602`).
    - Content: drawn starting at row `p.Dst.Min.Y` (which is `1`) up to `p.Dst.Max.Y` (`1 + availHeight`) via `compose.Blit(dst, mirror.Surface, p.Dst)` (`internal/client/client.go:608–610`).
    - Scroll footer: if `pu.ScrollOffset > 0`, drawn at row `footerY = p.Dst.Max.Y - 1` (`internal/client/client.go:611–633`).
    - Status bar: drawn at row `c.rows - 1` in `drawStatusBarLocked` (`internal/client/client.go:499, 888–927`).
    - Host cursor: clamped vertically to `[focusedPlacement.Dst.Min.Y, focusedPlacement.Dst.Max.Y]` (`internal/client/client.go:527–532`).

- **Web client:**
  - Viewport allocation:
    - Host container `.terminal-shell` is styled as `height: 100vh` flexbox (`web/src/wideboi-app.ts:25, 32`).
    - Top `.title` row is `height: 16.8px; flex: none;` (`web/src/wideboi-app.ts:39–42`).
    - Bottom `.status` row is `height: 16.8px; flex: none;` (`web/src/wideboi-app.ts:39–42`).
    - Center `.pane-strip` has `flex: 1; min-height: 0; overflow-y: hidden;` (`web/src/wideboi-app.ts:47–53`).
    - Each `wideboi-pane` has `height: 100%; overflow: hidden;` (`web/src/wideboi-pane.ts:14, 53`).
    - Row count sent to server adds `+ 2` to pane strip rows (`web/src/wideboi-app.ts:307`), accounting for the 2 rows subtracted by server `AvailHeight`.
  - Rendering vertical positions:
    - `wideboi-pane` uses an internal `ResizeObserver` to pass element pixel height to `PanePainter.resize(width, height)` (`web/src/wideboi-pane.ts:77–80`).
    - `PanePainter` sizes `<canvas>` backing store height to `Math.round(height * dpr)` (`web/src/pane-painter.ts:74, 82`).
    - In `PanePainter.draw()`:
      - Line rows are rendered with top pixel coordinate `py = y * CELL_HEIGHT` where `CELL_HEIGHT = 16.8` (`web/src/pane-painter.ts:134`; `web/src/pane-state.ts:7`).
      - Row loop terminates when `y * CELL_HEIGHT >= this.height` (`web/src/pane-painter.ts:119`).
      - Cursor is placed at `py = pane.cursorY * CELL_HEIGHT` (`web/src/pane-painter.ts:167–170`).
      - Scroll footer is positioned at `footerY = Math.floor(this.height / CELL_HEIGHT) * CELL_HEIGHT - CELL_HEIGHT` (`web/src/pane-painter.ts:180–182`).

---

### 4. Row Selection Under Overflow and Vertical Scrolling / Panning Mechanisms

- **Row selection when rows exceed viewport:**
  - Emulators on the server are sized to `AvailHeight(s.rows)` (`internal/server/server.go:792`). Overflow lines go into the emulator's scrollback buffer `ScrollbackLen()` (`internal/server/term/grid.go:677, 715`).
  - The server tracks client scroll positions in `s.clientScrollOffsets[tp][paneID]` (`internal/server/server.go:76, 1148, 1180–1183`).
  - Row rendering on the server is performed in `p.UpdateMessageForOffset(offset, ...)` (`internal/server/pane.go:311–350`):
    - When `offset <= 0`: `grid.DrawAt` delegates to `g.em.Draw` for active terminal rows (`internal/server/term/grid.go:707–709`).
    - When `offset > 0`: `grid.DrawAt` computes history row index `sbY = (sbLen - offset) + y` (`internal/server/term/grid.go:722`). Rows are pulled from `g.em.ScrollbackCellAt(x, sbY)` if `sbY < sbLen`, or `g.em.CellAt(x, sbY - sbLen)` if `sbY >= sbLen` (`internal/server/term/grid.go:725–729`).
  - The server transmits the selected window of rows to clients inside `MsgPaneUpdate.Lines` (`internal/server/pane.go:326–350`) or row patches in `MsgPanePatch.ChangedRows` (`internal/server/server.go:1249–1282`).
  - Clients render only the received row buffer:
    - CLI copies received rows into `PaneMirror.Surface` (`internal/client/client.go:288–307`) and blits them to `p.Dst` (`internal/client/client.go:609`).
    - Web renders rows sequentially up to canvas pixel height `y * CELL_HEIGHT < this.height` (`web/src/pane-painter.ts:119`).

- **Vertical scrolling / panning mechanisms:**
  - **Wire message:** `MsgScroll { PaneID int, Delta int }` (`internal/protocol/messages.go:172–175`; `internal/protocol/wirepb/wideboi.proto:200–203`).
  - **Server handler (`internal/server/server.go:539–572`):**
    - Computes `newOffset = cur + m.Delta`, clamped to `[0, p.ScrollbackLen()]` (`internal/server/server.go:550–557`).
    - Updates `s.clientScrollOffsets[tp][paneID]` (`internal/server/server.go:559`).
    - Clears unread flags if scrolled back to 0 (`internal/server/server.go:560–562`).
    - Increments `clientScrollGens[tp][paneID]` to invalidate wire caching and sets `needPaneBroadcast = true` (`internal/server/server.go:563–571`).
    - Implements content pinning during broadcasts: if child writes increase `sbLen` while client is scrolled up, server increments `curOffset` by the difference to maintain viewport anchoring (`internal/server/server.go:1200–1210`).
  - **CLI client scrolling:**
    - Key bindings: configured via `keys.ActionScroll` (default bindings: `j` for `Delta: -10`, `k` for `Delta: 10`) (`internal/keys/keys.go:158–161`). Routed in `cmd/wideboi/router.go:147` to `cli.SendScroll(ctx, act.Scroll)` (`cmd/wideboi/main.go:829`), which sends `protocol.MsgScroll` (`internal/client/client.go:1165`).
    - Mouse wheel: handled in `mouse.go`; `uv.MouseWheelUp` sends `Delta = +3`, `uv.MouseWheelDown` sends `Delta = -3` (`internal/client/mouse.go:35, 215–219`).
    - Status indicators: bottom overlay line renders `[▲ scroll +<offset>/<len>]` and `[▼ new output]` (`internal/client/client.go:611–633`); status bar shows `[scroll +<offset>]` (`internal/client/client.go:913–917`).
  - **Web client scrolling:**
    - Key bindings: in control mode, `KeyJ` sends `{ case: 'scroll', value: { paneId, delta: -10 } }`, `KeyK` sends `delta: 10` (`web/src/wideboi-app.ts:621–625`).
    - Mouse wheel: listener on `.pane-strip` converts `e.deltaY` into line deltas (`delta = Math.trunc(-e.deltaY / 30) || (e.deltaY < 0 ? 3 : -3)`) and dispatches `{ case: 'scroll', value: { paneId, delta } }` (`web/src/wideboi-app.ts:708–723`).
    - Status indicators: canvas renders scroll footer `[▲ scroll +<offset>/<len>]` (`web/src/pane-painter.ts:179–192`); header displays `[scroll +<offset>]` (`web/src/wideboi-app.ts:843`).

---

### 5. Interaction of `MsgAttach` and `MsgResize` with Session Sizing, Layout Snapshots, and Pane Updates

- **`MsgAttach`:**
  1. **Transport tracking:** marks the transport attached in `s.attachedTransports[tp] = true` (`internal/server/server.go:422`) and records attachment timestamp (`internal/server/traffic.go:18; internal/server/server.go:417`).
  2. **Session sizing:** if `m.Cols > 0 && m.Rows > 0`, records size in `s.clientSizes[tp]` and invokes `s.recomputeSessionSizeLocked()` (`internal/server/server.go:428–429`).
  3. **Pane bootstrapping:** if `len(s.panes) == 0`, spawns default or startup panes (`internal/server/server.go:431–453`).
  4. **Pane resizing:** unconditionally calls `s.resizePanesLocked()` (`internal/server/server.go:454`), synchronizing emulator sizes to `layout.AvailHeight(s.rows)`.
  5. **Layout snapshot:** sets `needBroadcast = true` and `sendMetadata = true` (`internal/server/server.go:455–456`). In `handleClientMsg`, `s.broadcastLayout(ctx)` is called (`internal/server/server.go:599`), which sends `protocol.MsgLayoutSnapshot` (columns, statuses, titles) to all attached transports (`internal/server/server.go:1037–1086`).
  6. **Pane updates:** `broadcastLayout` immediately calls `s.broadcastPaneUpdates(ctx, columnsChanged)` (`internal/server/server.go:1099`). For the attaching client, `s.paneGens[tp]` is empty (`!hasWireGen` at `internal/server/server.go:1222`), forcing full `protocol.MsgPaneUpdate` transmissions for every active pane to that client (`internal/server/server.go:1230–1243`).
  7. **Metadata:** calls `s.sendPaneMetadataTo(ctx, tp)` (`internal/server/server.go:605`).

- **`MsgResize`:**
  1. **Session sizing:** if `m.Cols > 0 && m.Rows > 0`, stores new dimensions in `s.clientSizes[tp]` (`internal/server/server.go:463`).
  2. **Recomputation:** stores `oldCols, oldRows := s.cols, s.rows` and calls `s.recomputeSessionSizeLocked()` (`internal/server/server.go:464–465`).
  3. **Conditional resize branch:**
     - If `s.cols != oldCols || s.rows != oldRows`:
       - Invokes `s.resizePanesLocked()` (`internal/server/server.go:467`).
       - Sets `needBroadcast = true` (`internal/server/server.go:468`).
       - `broadcastLayout(ctx)` fires (`internal/server/server.go:599`).
       - In `broadcastLayout`, `columnsChanged := !sameColumnSetAndSizes(cols, s.lastColumns)` evaluates whether column dimensions changed (`internal/server/server.go:1036`).
       - Invokes `s.broadcastPaneUpdates(ctx, columnsChanged)` (`internal/server/server.go:1099`). When `columnsChanged` is true, `force = true`, which clears differential baseline checks (`!force` at line 1228) and sends full `MsgPaneUpdate` updates to all clients (`internal/server/server.go:1104, 1222`).
     - If `s.cols == oldCols && s.rows == oldRows`:
       - `s.resizePanesLocked()` is **not** called (`internal/server/server.go:466–469`).
       - `needBroadcast` remains `false`; no layout snapshot and no pane updates are dispatched.
