# Multi-Client PTY Sizing Policy (#184) and Web UI Vertical Viewport (#134) Spec

**Goal:** Provide predictable PTY sizing across multiple clients by establishing a stable sizing policy (first client sets size, subsequent clients attach as viewers, explicit size claim) and allowing smaller viewports in Web and CLI to navigate vertical content and view active output at the bottom.

**Source:** GitHub issue #184 and issue #134

## Current state

- **Sizing calculation:** The server computes session rows and columns as the minimum across all connected clients in `recomputeSessionSizeLocked` (`internal/server/server.go:701-721`).
- **Resizing triggers:** Every client attach (`MsgAttach`) and window resize (`MsgResize`) triggers `recomputeSessionSizeLocked` and `resizePanesLocked` (`internal/server/server.go:429, 465`). A small browser window, mobile client, or secondary terminal immediately shrinks PTYs for all clients.
- **Web UI viewport rendering:** The web client computes rows as `paneStrip.clientHeight / CELL_HEIGHT + 2` (`web/src/wideboi-app.ts:305-309`). In `pane-painter.ts:119`, it draws rows starting from row 0 and stops when `y * CELL_HEIGHT >= this.height`. If the hosted pane is taller than the canvas, rows at the bottom (where prompts, cursor, and running agents output) are clipped and unreachable.
- **Web UI scrolling:** Mouse wheel and scroll keys exclusively dispatch `MsgScroll` (`internal/protocol/messages.go:172`), which adjusts server scrollback history above row 0 (`internal/server/server.go:539-572`). There is no mechanism to pan or view the bottom rows of the active terminal grid.
- **CLI viewport rendering:** In `internal/layout/layout.go:385-387`, `srcY` is hardcoded to 0 (`dst.Min.Y - 1`), always displaying the top rows if a pane is taller than the available viewport height.

## Desired end state

1. **Multi-client size policy (Server):**
   - The first client to attach establishes the session geometry (`s.rows`, initial column widths/heights) and becomes the active `sizeOwner`.
   - While `sizeOwner` is attached, ordinary `MsgResize` events from that owner update the session geometry.
   - Subsequent clients attach as **viewers**. Their `MsgAttach` and `MsgResize` update their recorded dimensions in `s.clientSizes[tp]`, but do **not** alter `s.rows` or trigger `resizePanesLocked()`.
   - If the active `sizeOwner` disconnects, the session dimensions remain locked at their current values. The session does not shrink or change geometry on disconnect.
   - Any client can explicitly claim size ownership by sending `MsgVerb{Verb: VerbClaimSize}`. Upon receipt, the server sets `sizeOwner = tp`, adopts that client's reported viewport dimensions, updates `s.cols` and `s.rows`, calls `resizePanesLocked()`, broadcasts the new layout snapshot, and resends pane updates.
   - Protocol version is bumped to 7 to reflect the new `VerbClaimSize` enum value (14).

2. **Web UI vertical navigation (#134):**
   - When a pane's rows exceed the element's visible row capacity (`Math.floor(this.height / CELL_HEIGHT)`), `wideboi-pane` and `PanePainter` support a vertical viewport offset `scrollYOffset`.
   - **Bottom-anchored by default:** When viewing active terminal output at the bottom, the view stays anchored to the bottom (`scrollYOffset = max(0, pane.rows - visibleRows)`), ensuring cursor and agent output are always in view.
   - **Seamless vertical scrolling:**
     - Mouse wheel down scrolls down within the active pane toward the bottom.
     - Mouse wheel up scrolls up through the active pane's rows toward row 0.
     - Once at row 0 (`scrollYOffset == 0`), continued wheel up dispatches `MsgScroll{Delta: 3}` into the server scrollback history.
     - Wheel down while in scrollback decreases server scrollback offset. Once scrollback reaches 0, wheel down increases `scrollYOffset` back toward the bottom.
   - **Mouse and cursor coordinates:** `cellAt(clientX, clientY)` accounts for `scrollYOffset` so click-to-focus, selection drags, and mouse tracking map to the correct row index.
   - **Visual indicator:** When scrolled away from the bottom within the active screen, a visual indicator (in the pane footer or status line) indicates the offset.
   - **Toolbar Claim Button:** A "Claim size" / "Fit to window" button in the toolbar allows one-click claiming of session size.

3. **CLI viewer vertical bottom-anchoring:**
   - In `internal/layout/layout.go` and `internal/layout/card.go`, when `c.Height > availHeight`, `srcY` is calculated as `max(0, c.Height - availHeight)` so a secondary CLI client viewing a taller session displays the active bottom region.
   - In CLI control mode, a keybinding (e.g. `S` or `c`) triggers `VerbClaimSize`.

## Design decisions

- **Decision:** First client sets size, subsequent clients attach as viewers, explicit claim action via `VerbClaimSize`.
  - **Why:** Matches user expectations that starting a session sets its geometry, opening a secondary monitor/browser window shouldn't degrade or reflow the active session, and explicit user action allows transferring control when intended.
  - **Rejected:** Minimum-of-all (current behavior, breaks on small clients); CLI-only ownership (disallows web-only or web-primary workflows).

- **Decision:** Bottom-anchored viewport with unified scroll transition (active pane scroll -> scrollback history).
  - **Why:** Terminal users interact with the prompt and bottom output lines. Forcing users to scroll down on every command would make viewing taller panes unusable. Seamless transition prevents needing two separate scroll gestures for active screen vs scrollback.
  - **Rejected:** Native CSS `overflow-y: auto` (competes with custom canvas rendering and confuses mouse events / scrollback).

- **Decision:** Protocol version bumped to 7.
  - **Why:** Per `docs/LESSONS.md`, wire changes require bumping `protocol.Version` so mismatched clients/servers fail cleanly with version mismatch rather than decoding corrupt frames.

## Patterns to follow

- Server verb dispatch: `internal/server/server.go:474-500`
- Wire messages and protobuf codec: `internal/protocol/messages.go:14-30`, `internal/protocol/wirepb/wideboi.proto:14-30`
- Layout placements and `AvailHeight`: `internal/layout/layout.go:320, 350-402`, `internal/layout/card.go:60-75`
- Web painter rendering and mouse handling: `web/src/pane-painter.ts:112-178`, `web/src/wideboi-app.ts:708-723`
- Status bar rendering: `internal/client/client.go:888-927`, `web/src/wideboi-app.ts:830-850`

## What we're NOT doing

- **No decoupling of horizontal card width from PTY width (Issue #70):** Card width remains equal to child column width in the strip. Horizontal strip navigation remains unchanged.
- **No per-pane virtual heights:** All hosted panes in a session share the uniform session height.
- **No canvas zooming / scaling in web client:** Font size and cell dimensions remain fixed.

## Open questions

None.
