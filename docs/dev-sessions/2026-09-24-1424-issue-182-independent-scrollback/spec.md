# Make Pane Scrollback Position Independent per Client Spec

**Goal:** Allow multiple clients attached to the same session to navigate a pane's scrollback history independently without disturbing each other's view or live terminal output.

**Source:** GitHub Issue #182

## Current state

- `MsgScroll` sets `p.SetScrollOffset` on the shared `vtGrid` (`internal/server/server.go:433-437`, `internal/server/term/grid.go:537-548`).
- `Pane.UpdateMessage` draws from `g.scrollOffset.Load()`, generating a single global `MsgPaneUpdate` snapshot (`internal/server/pane.go:286-338`, `internal/server/term/grid.go:556-581`).
- `broadcastPaneUpdates` sends this single snapshot (or a diff patch against `s.paneFrames[tp][id]`) to all clients (`internal/server/server.go:878-914`).
- As a result, when client A scrolls up, client B viewing the same pane is also dragged into scrollback history.
- When new output arrives, `vtGrid` advances `generation` and updates the live bottom row, but keeps `scrollOffset` constant relative to the bottom, causing the view to drift.
- Clients do not receive scroll offset or scrollback bounds in `MsgPaneUpdate` or `MsgPanePatch`.

## Desired end state

1. **Per-client scroll offset tracking:**
   - The server maintains scroll offset (`offset >= 0`) independently per client per pane (`s.clientScrollOffsets[tp][paneID]`).
   - Scrolling client A (`MsgScroll` from A) updates only A's offset. Client B's view remains unchanged (live at offset 0).
   - Reconnecting or attaching a client starts with scroll offset 0 (live view) on all panes.
   - Closing a pane cleans up per-client scroll offset state; disconnecting a client cleans up that client's scroll offsets.

2. **Per-client render and broadcast:**
   - `vtGrid.DrawAt(dst, area, offset)` draws the grid at a caller-specified scroll offset without mutating any global state.
   - `Pane.UpdateMessageForOffset(offset)` constructs a `MsgPaneUpdate` for a given scroll offset. When `offset > 0`, `CursorVisible` is set to `false` (cursor hidden in scrollback).
   - `broadcastPaneUpdates` renders pane views according to each client's scroll offset. If multiple clients share the same offset (most commonly offset 0), the rendered frame is reused.
   - Per-client monotonic generations ensure patch delta compression (`BuildPanePatch` and `ApplyPanePatch`) continues to work per client.

3. **Behavior on new output:**
   - When a client is scrolled up (`offset > 0`) and new terminal output arrives on that pane, the client stays in history.
   - The server flags `unread_output = true` for that client on that pane.
   - The pane update for that client reflects `unread_output = true`.
   - When the client scrolls back to live output (`offset == 0`), `unread_output` is cleared.

4. **Behavior on typing / input:**
   - When a client types into a pane (`MsgInput`), that client's scroll offset for that pane snaps back to 0 (live output) so the user immediately sees what they type.

5. **Visual indicators:**
   - **Per-pane bottom status bar:** When `offset > 0`, a 1-row footer is displayed at the bottom of the pane (e.g. `[▲ scroll +12/150  ▼ new output]` when unread output is present, or `[▲ scroll +12/150]` when no new output). When `offset == 0`, no footer is displayed.
   - **Client bottom status line:** In the terminal client's bottom status line (`internal/client/client.go`), if the focused pane is scrolled (`offset > 0`), an indicator is displayed (e.g. `[scroll +12 ⤓]` or `[scroll +12]`).
   - **Web client:** The web client renderer (`web/src/renderer.ts`) also displays the scroll position and new-output indicator when `offset > 0`.

6. **Wire protocol:**
   - `MsgPaneUpdate` and `MsgPanePatch` carry `int32 scroll_offset`, `int32 scrollback_len`, and `bool unread_output`.
   - `protocol.Version` bumped from 2 to 3.
   - WebSocket subprotocol updated to `wideboi.v3`.

## Design decisions

- **Decision:** Track scroll offset per client in `Server`, not on `vtGrid`.
  - **Why:** Terminal history belongs to the emulator, but viewport position is presentation state specific to each viewing client (identical to how viewport size `clientSizes` is per-client).
  - **Rejected:** Keeping scroll offset on `vtGrid`. A single int on `vtGrid` cannot represent different viewport positions for concurrent clients.

- **Decision:** Retain scroll position in history when new output arrives, but flag `unread_output`.
  - **Why:** Snapping to bottom when an active background process (like an AI agent or test runner) outputs text makes scrollback unreadable. Setting `unread_output = true` alerts the user without disrupting reading.
  - **Rejected:** Snapping to bottom on any output.

- **Decision:** Snap to bottom (`offset = 0`) when the client sends keystrokes (`MsgInput`).
  - **Why:** Standard terminal behavior: typing in a pane implies interactive engagement with the current command prompt.
  - **Rejected:** Leaving the pane scrolled up while typing into the prompt unseen.

- **Decision:** Render the per-pane scrollbar/footer on the client from metadata in `MsgPaneUpdate`/`MsgPanePatch`.
  - **Why:** Keeps the terminal emulator output clean and enables `BuildPanePatch` row-shifting diffs to operate on raw terminal content. Clients (terminal, web) can style and lay out indicators cleanly.
  - **Rejected:** Server baking footer text directly into terminal screen buffer cells.

- **Decision:** Bump `protocol.Version` to 3.
  - **Why:** `MsgPaneUpdate` and `MsgPanePatch` gain new fields, and patch reconstruction rules require both sides to understand the updated schema. Per `docs/LESSONS.md`, wire changes require bumping `protocol.Version` and the WebSocket subprotocol.
  - **Rejected:** Silent proto field additions without handshake version bump.

## Patterns to follow

- **Per-client state tracking:** Follow `s.paneGens`, `s.paneFrames`, and `s.clientSizes` in `internal/server/server.go:57-65`, and transport cleanup in `s.removeTransportLocked` (`internal/server/server.go:279-294`).
- **Protobuf wire types and versioning:** Follow `internal/protocol/wirepb/wideboi.proto`, `internal/protocol/codec.go`, and `internal/protocol/version.go` (refer to #174 version handshake in `cmd/wideboi/handshake.go`).
- **Patch generation & row shifting:** Follow `internal/protocol/pane_patch.go` (`BuildPanePatch` and `ApplyPanePatch`).
- **Client rendering & status bar:** Follow `internal/client/client.go:850-895` (`statusLineLocked`) and card/pane drawing in `internal/client/client.go:550-645`.

## What we're NOT doing

- We are NOT modifying the underlying scrollback buffer data structure or allocation in `x/vt` / `ultraviolet`.
- We are NOT implementing full scrollback search (Ctrl-R / `/`) in this issue.
- We are NOT persisting scrollback history to disk across server restarts.
- We are NOT changing layout calculations or focus management.

## Open questions

*(None — all design questions resolved during brainstorm)*
