# Development Notes: Issue #182 (Independent Scrollback Position per Client)

## Summary

Implemented independent, per-client scrollback positions and view rendering for wideboi sessions. Multiple clients attached to the same pane can now navigate history independently without interfering with each other's view or live terminal output.

## Key Decisions & Architecture

1. **Server-tracked per-client view slices:**
   - Full terminal history remains authoritative with the pane in `vtGrid.em`.
   - The server tracks scroll offsets per client per pane (`clientScrollOffsets[tp][paneID]`).
   - `vtGrid.DrawAt(dst, area, offset)` renders at a caller-specified offset without mutating global grid state.
   - `Pane.UpdateMessageForOffset(offset, unread)` constructs the `MsgPaneUpdate` for that offset, suppressing cursor visibility when scrolled into history (`offset > 0`).

2. **Stay in history on new output + content pinning:**
   - When new output arrives on a pane while a client is scrolled up (`lastOffset > 0`), the client remains in history.
   - If scrollback length increases while scrolled up, `offset` is increased by the delta (`curOffset += sbLen - lastSbLen`), keeping the exact visible lines pinned in view without drift.
   - The server sets `unread_output = true` for that client on that pane.

3. **Typing resets to live:**
   - When a client sends keystrokes or input (`MsgInput`) to a pane currently scrolled up, the client's scroll offset for that pane snaps back to 0 (live output) and unread output is cleared.

4. **Visual indicators:**
   - **Pane bottom footer:** In both terminal client (`internal/client`) and web client (`web/src/renderer.ts`), when `offset > 0`, a 1-row footer is displayed at the bottom of the pane (`[▲ scroll +{offset}/{len}]`, or `[▲ scroll +{offset}/{len}  ▼ new output]` when unread output exists).
   - **Status line indicator:** The client status bar displays `[scroll +{offset}]` or `[scroll +{offset} ⤓]` next to the focused pane when scrolled.

5. **Wire protocol versioning:**
   - `MsgPaneUpdate` and `MsgPanePatch` extended with `scroll_offset`, `scrollback_len`, and `unread_output`.
   - Protocol version bumped from 2 to 3 (`wideboi.v3`).
   - TypeScript and Go protobuf bindings regenerated and verified via `make proto-check`.

## Verification Evidence

All test suites and CI gates passed:
- `make check`:
  - `fmt-check`: Clean
  - `lint`: `go vet` clean
  - `seam-check`: OK (no client/server seam crossings)
  - `test`: All unit tests passed across all packages
  - `web-test`: 7 files, 25 tests passed
  - `web-accept`: Playwright browser acceptance test passed
  - `race`: `go test -race -count=1 ./...` passed with zero races
  - `verify-exit`: Process exit, PTY teardown, and signal disposal verified
  - `smoke`: 37/37 passed
  - `attach-check`: 25/25 passed
  - `golden`: Wire output matches golden snapshot

## Copilot Review & Addressed Items

Copilot review comments identified three high-value areas for refinement, all addressed:
1. **Output Generation vs. Render Generation:** `p.Generation()` increments on resize, which would previously trigger `unread_output` when a window was resized while scrolled. Added `OutputGen()` to `term.Grid` (bumped exclusively by child terminal `Write`) so layout resizes never produce false unread notifications. Added `TestResizeDoesNotTriggerUnreadOutputWhenScrolled`.
2. **Delivery Bookkeeping for Scrollback Length:** Moved `paneSbLens` and `paneOutputGens` updates to run only on accepted deliveries (`r.accepted`), ensuring delivery retries preserve the correct delta for content pinning.
3. **Offset-Zero Fast Path in Emulator Draw:** Restored direct delegation to `g.em.Draw` when `offset <= 0` in `DrawAt`, avoiding `writeResizeMu` lock contention and per-cell history walks during normal live rendering.
4. **Focused Pane Scroll Indicator:** In `web/src/renderer.ts`, placed the focused pane's scroll status at the beginning of the status bar text to prevent it being clipped off by subsequent column listings.

## Commits on Branch `issue-182-independent-scrollback`

1. `9a81cf2`: Phase 1: Protocol and Codec Updates (Wire v3)
2. `00e3d00`: Phase 2: term.Grid.DrawAt and Pane.UpdateMessageForOffset
3. `7f6090d`: Phase 3: Per-Client Scroll Tracking and Broadcast in internal/server
4. `3b11523`: Phase 4: Visual Indicators in Terminal and Web Clients
5. `b8d5859`: Phase 5: End-to-End Integration and Gate Verification
