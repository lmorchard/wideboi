## Phase 1: Protocol and Core Layout Updates
1. Move `PaneStatus` into `protocol`.
2. Update `MsgLayoutSnapshot` to remove `FocusPaneID` and use `protocol.PaneStatus` for statuses.
3. Update `MsgVerb` to include `PaneID int`.
4. Remove `MsgFocusPane`.
5. Update `layout.Strip` methods to take `paneID int` (or `afterPaneID int`) and fix up `layout` tests.

## Phase 2: Server Updates
1. Stop responding to `MsgFocusPane`.
2. Update `MsgVerb` handling: apply mutating verbs (cycle width, grow, shrink, move left/right, kill) to the specified pane.
3. Drop server handling of `VerbFocusLeft/Right/Last` and `VerbSmartJump`.
4. Update server tests to match these changes (removing focus tracking from server asserts).
5. Update `MsgLayoutSnapshot` building to use `protocol.PaneStatus` directly.

## Phase 3: Client Updates
1. Implement `HandleVerb` locally in the client for focus changes (`VerbFocusLeft/Right/Last` and `VerbSmartJump`).
2. Update `SendVerb` to include the current `focusPaneID`.
3. Stop sending `MsgFocusPane` on mouse click; instead mutate local focus and trigger redraw.
4. Render raw `PaneStatus` to glyphs on the client side in the status bar.
5. Update client tests.

## Phase 4: Integration and Verification
1. Run `make check` and fix any remaining wiring issues.
2. Manually verify multi-client focus works as expected.
