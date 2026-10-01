# Spec: Issue 337 Preserve Soft-Wrapping When Selecting and Copying Text

## Motivation & Background
When copying text spanning multiple visual lines (such as long URLs, wrapped shell commands, or compiler diagnostics), terminal selections currently treat every visual row break as a hard newline (`\n`).
- In the Web client (`web/src/pane-state.ts`), `selectionText` unconditionally joins rows with `\n`.
- In the TUI client (`internal/client/mouse.go`), `selectionText` only approximates wrapping by checking cell occupancy at the rightmost edge of the destination rectangle, which fails across columns, margins, or clippings.

## Requirements

1. **Wire Protocol (v20)**:
   - Add `bool wrapped = 2;` to `LineData`.
   - Add `bool wrapped = 3;` to `PaneRow`.
   - Add `WrappedLines []bool` to `MsgPaneUpdate` and propagate through `MsgPanePatch`.
   - Bump protocol `Version` and `PROTOCOL_VERSION` to 20.
   - Record schema hash in `version_guard_test.go`.

2. **Server Extraction (`internal/server/pane.go`)**:
   - In `UpdateMessageForOffset`, compute `wrappedLines []bool` directly from the rendered `uv.ScreenBuffer`:
     A row `y` wraps into `y+1` if its rightmost cell (`cols-1`) contains non-empty, non-space content and row `y+1` contains content.
   - This runs cleanly at snapshot construction time without locks, callbacks, or memory leaks on `term.Grid`.

3. **Patch Building and Application (`internal/protocol/pane_patch.go`)**:
   - In `BuildPanePatch`, account for `wrapped` changes when comparing lines and detecting shifts.
   - In `ApplyPanePatch`, preserve `wrapped` state across row shifts and row updates.

4. **TUI Client Copying (`internal/client/mouse.go`)**:
   - In `selectionText`, check `pu.WrappedLines[localY]` for the selected pane.
   - If row `localY` is wrapped, omit the newline between row `localY` and row `localY+1`.

5. **Web Client Copying (`web/src/pane-state.ts`)**:
   - In `PaneStore.applyPatch`, preserve `row.wrapped` on created `LineData`.
   - In `selectionText`, check `pane.lines[lineY]?.wrapped`. If true, omit `\n` between consecutive wrapped rows.

6. **Automated Testing**:
   - Unit tests in `internal/client/mouse_test.go` verifying wrapped rows omit newlines.
   - Unit tests in `web/src/pane-rendering.test.ts` verifying `selectionText` with soft-wrapped lines.
   - Browser acceptance tests in `web/tests/links-and-clipboard.spec.ts` verifying multi-line drag-copy of soft-wrapped rows.
