# Notes: Distinguish bracketed paste from typed text and macro inputs in MsgInput

- Branch: `issue-312-distinguish-bracketed-paste`
- Worktree: `.worktrees/issue-312-distinguish-bracketed-paste`
- Issue: #312

## Decisions & Outcomes

1. **Protocol (`wirepb.MsgInput` & `protocol.MsgInput`)**:
   - Added `bool paste = 4;` to `MsgInput` in protobuf and `Paste bool` to `protocol.MsgInput` in Go.
   - Preserves single unified input event queue, FIFO ordering, scrollback reset, dashboard routing, and upgrade drain logic without introducing duplicate message types.
   - In protobuf, unset booleans default to `false`.
   - Bumped `protocol.Version` from 17 to 18, matching `VERSION_PROTOCOL = "wideboi.v18"` and recording the new descriptor sha256 (`911852df...`) in `wireSchemaHashes`.

2. **Server Handling (`internal/server/handlers.go`)**:
   - `handleInputLocked` wraps `m.Data` with bracketed paste markers (`\x1b[200~` and `\x1b[201~`) only when `m.Paste && p.grid != nil && p.grid.BracketedPaste()`.
   - Raw inputs with `m.Paste == false` (macros, IME, mobile draft/direct, test keystrokes) are forwarded directly to `p.SendBytes` without markers even when the child terminal has bracketed paste enabled.

3. **Go Client & Console TUI**:
   - `Client.SendInput(ctx, data)` sends `MsgInput` with `Paste: false`.
   - `Client.SendPaste(ctx, data)` sends `MsgInput` with `Paste: true`.
   - `handlePaste` in `cmd/wideboi/main.go` routes `uv.PasteEvent` via `cli.SendPaste`.

4. **Web Client**:
   - Added `sendPasteInput(sender, paneID, text)` sending `paste: true`.
   - `sendTextInput(sender, paneID, text)` sends `paste: false`.
   - Clipboard paste handlers (Shift+V and `document.addEventListener('paste')`) call `sendPasteInput`.
   - Macro text steps (`executeMacro`), IME composition (`compositionend`), mobile direct input, and mobile draft call `sendTextInput`.

## Verification Evidence

- `make quick`: passed cleanly (fmt, vet, seam-check, Go test suite, vitest).
- `make proto-check`: passed cleanly with zero drift.
- `make check`: passed all gates:
  - 152 vitest unit tests
  - 50 Playwright browser tests
  - Go `-race` test suite across all packages
  - 41 smoke tests (`scripts/smoke.py`)
  - 29 attachcheck tests (`scripts/attachcheck.py`)
  - Golden snapshot check
- Four fresh test runs of `TestHandleInputBracketedPaste` passed consecutively.
