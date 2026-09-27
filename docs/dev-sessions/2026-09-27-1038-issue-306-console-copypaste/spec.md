# Console TUI Copy/Paste Fix Spec

**Goal:** Ensure reliable copy and paste under the console TUI client (`cmd/wideboi`) by supporting bracketed paste, handling `uv.PasteEvent`, forwarding bracketed paste to children, and providing local clipboard fallbacks alongside OSC 52.

**Source:** GitHub Issue #306 (user request 2026-09-27)

## Current state

- **Host bracketed paste not enabled (`cmd/wideboi/main.go:1066-1076`)**: Screen setup enters alt-screen and enables mouse tracking, but never calls `scr.EnableBracketedPaste()`.
- **`uv.PasteEvent` ignored (`cmd/wideboi/main.go:1088-1181`)**: The event loop switches on `uv.WindowSizeEvent`, `uv.KeyPressEvent`, and `uv.MouseEvent`. Any `uv.PasteEvent` emitted by ultraviolet upon receiving `\x1b[200~...\x1b[201~` is silently discarded.
- **`cli.SendInput` unused (`internal/client/client.go:638-649`)**: `SendInput(ctx, data)` packages bytes into `protocol.MsgInput{PaneID, Data}`, but is never called in `cmd/wideboi`.
- **Child bracketed paste not forwarded (`internal/server/handlers.go:510-514`, `internal/server/term/grid.go:622-624`)**: While `vtGrid` tracks `ansi.ModeBracketedPaste` for snapshot restore, `m.Data` is passed raw to the child without bracketed paste delimiters even when the child requested it.
- **Clipboard write is OSC 52 only (`cmd/wideboi/main.go:1258-1261`)**: `writeClipboard` only writes OSC 52 sequences. macOS Terminal.app and default iTerm2 ignore OSC 52, leaving the system clipboard untouched on drag-release.

## Desired end state

1. **Host bracketed paste**:
   - `cmd/wideboi` enables bracketed paste on startup via `scr.EnableBracketedPaste()`.
   - The event loop in `main.go` handles `uv.PasteEvent`, clears any active selection, and calls `cli.SendInput(ctx, []byte(ev.Content))`.
2. **Child bracketed paste preservation**:
   - When `protocol.MsgInput` carries `Data`, if the target pane's terminal emulator has `BracketedPaste` enabled, the server wraps the data with `\x1b[200~` and `\x1b[201~` when writing to the child pty.
   - If bracketed paste is disabled in the child, data is written raw as before.
3. **Local clipboard support alongside OSC 52**:
   - `writeClipboard` continues to emit OSC 52 for remote/SSH and OSC 52-compatible terminals.
   - When not in an SSH session (`SSH_CLIENT` and `SSH_TTY` are empty), `writeClipboard` also writes to the local OS clipboard asynchronously with a brief timeout:
     - On macOS: via `pbcopy`.
     - On Linux: via `wl-copy` or `xclip -selection clipboard` if found in `PATH`.

## Design decisions

- **Decision:** Enable bracketed paste on the host screen and handle `uv.PasteEvent`.
  - **Why:** Modern terminals wrap pasted text in `\x1b[200~` ... `\x1b[201~`. Ultraviolet already decodes this into `uv.PasteEvent`. Handling it routes pastes directly to `cli.SendInput` as a single chunk instead of hundreds of individual keypresses.
  - **Rejected:** Leaving bracketed paste off and streaming key events, which causes prefix collisions with the router and overflows `p.input` buffer (cap 256).

- **Decision:** Server wraps `MsgInput.Data` in `\x1b[200~` and `\x1b[201~` if the child pane has `BracketedPaste` enabled.
  - **Why:** Interactive shells and editors expect pasted chunks inside bracketed paste markers so they do not execute newlines immediately.
  - **Rejected:** Sending raw data always; this breaks multiline pastes into shells that enabled bracketed paste.

- **Decision:** Write to local clipboard (`pbcopy`/`xclip`/`wl-copy`) in parallel with OSC 52 when not in an SSH session.
  - **Why:** Terminal.app has no OSC 52 support, and iTerm2 has it disabled by default. Local developer sessions expect drag-to-copy to populate their system pasteboard immediately.
  - **Rejected:** OSC 52 only (fails on Terminal.app/default iTerm2); or local tools only (fails over SSH).

- **Decision:** Mouse release remains the sole trigger for copying; no new copy keybindings.
  - **Why:** Keeps the modal key router lean and avoids breaking existing key maps.

## Patterns to follow

- **`internal/client/client.go:638-649`**: `cli.SendInput` pattern for forwarding raw bytes to focused pane.
- **`cmd/wideboi/main.go:1088-1181`**: Event loop structure in `main.go`.
- **`internal/server/term/grid.go:622-624`**: Mode tracking in `vtGrid`. Expose `BracketedPaste() bool` on `Grid` interface (or accessor on `vtGrid`).
- **`internal/server/pane.go:248-260`**: `p.SendBytes` queueing onto `p.input`.

## What we're NOT doing

- Not adding new keyboard copy/paste bindings (e.g. `prefix + y` or `prefix + ]`).
- Not reading from system clipboard or implementing OSC 52 clipboard reading.
- Not altering web client copy/paste behavior (already handled in #296).
- Not altering mouse tracking modes or disabling SGR mouse reporting.

## Open questions

- None. (Scope and decisions fully resolved during brainstorm).
