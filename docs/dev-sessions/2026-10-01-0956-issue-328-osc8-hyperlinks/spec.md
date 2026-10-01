# Issue 328: Support OSC 8 explicit terminal hyperlinks in TUI and Web clients

## Motivation & Background

Modern CLI utilities (e.g. `ls --hyperlink`, `git log`, `delta`, GCC/Clang/Rustc diagnostics, ripgrep) emit explicit **OSC 8 hyperlinks**:
```text
\e]8;;https://example.com\e\Click Here\e]8;;\e\
```

In standard terminals, this renders visible anchor text while hiding the URL behind a clickable link.

In Zellij, `panes/hyperlink_tracker.rs` tracks OSC 8 escape sequences, associates them with character cells, and re-emits them to the outer terminal with accurate coordinate boundaries during compositing.

### Current State in Wideboi

- Web UI: #296 added regex-based autolinking for plaintext URLs (`http://...`), but explicit OSC 8 hyperlinks are not handled.
- TUI Client: OSC 8 sequences are either discarded or not re-emitted when compositing cells in `internal/client/compose/surface.go`.
- Underlying terminal emulator (`charmbracelet/x/vt`) maintains `Cursor.Link` on the pen, but this attribute is not propagated through the wire protocol.

### Requirements & Scope

1. **Wire Protocol**:
   - Add link metadata to the protocol (an indexed link URI table in `MsgPaneUpdate` and `MsgPanePatch`, with cells referencing a link ID to avoid payload bloat).
2. **TUI Client**:
   - In `internal/client/client.go` / `compose` / `render`, propagate cell hyperlink metadata to Ultraviolet mirror cells (`uvCell.Link = uv.NewLink(...)`).
   - Ultraviolet diffing automatically emits OSC 8 open/close escape sequences to the host terminal, bounded by pane coordinate boundaries so links do not spill into adjacent cards or slivers.
3. **Web Client**:
   - Detect explicit OSC 8 links on hover in `<wideboi-pane>` and open via `window.open` on click.
   - Enforce safe URI schemes (`http:`, `https:`, `mailto:`, `ssh:`, `git:`, `gemini:`), rejecting `javascript:`, `data:`, etc.
