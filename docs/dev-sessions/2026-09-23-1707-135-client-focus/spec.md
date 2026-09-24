# Make pane focus independent per client

Currently, pane focus is tracked on the server and broadcast via `MsgLayoutSnapshot`. When multiple clients connect, changing focus on one client changes it on all others.

This issue tracks moving pane focus entirely to the client side.

### Proposed changes

1. **Protocol (`internal/protocol/messages.go`)**
   * Remove `FocusPaneID` from `MsgLayoutSnapshot`.
   * Remove `MsgFocusPane` entirely (clients focus locally on click).
   * Add `PaneID int` to `MsgVerb` so clients can explicitly target mutating verbs (like kill, move, resize).
   * Move `PaneStatus` into `protocol` so clients can receive raw statuses rather than rendered glyphs in the snapshot.

2. **Layout (`internal/layout/layout.go`)**
   * Modify mutating `Strip` methods (`CycleWidth`, `MoveLeft`, `MoveRight`, `GrowWidth`, `ShrinkWidth`) to accept a `paneID int` instead of acting implicitly on the internal `focusIndex`.
   * Update `AddColumn` to accept an `afterPaneID int` to insert new panes correctly relative to the requesting client's focus.

3. **Server (`internal/server/server.go`)**
   * Drop server-side handling of `VerbFocusLeft`, `VerbFocusRight`, `VerbFocusLast`, and `VerbSmartJump`.
   * Read `m.PaneID` from incoming `MsgVerb`s and apply mutations to the canonical strip.

4. **Client (`internal/client`)**
   * Handle focus verbs and `VerbSmartJump` entirely locally, modifying the local `Strip` and triggering a redraw without a network round-trip.
   * Send the current local focus as `PaneID` for any mutating `MsgVerb`.
   * Update mouse handling to focus locally instead of sending a message.
