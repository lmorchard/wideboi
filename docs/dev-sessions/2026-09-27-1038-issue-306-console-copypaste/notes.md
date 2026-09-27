# Notes: Console TUI Copy/Paste Fix (#306)

Session branch: `issue-306-console-copypaste`
Worktree: `.worktrees/issue-306-console-copypaste`
Issue: https://github.com/lmorchard/wideboi/issues/306

## Summary of Changes

1. **Host Terminal Bracketed Paste**:
   - `scr.EnableBracketedPaste()` enabled on startup in `cmd/wideboi/main.go`.
   - `scr.DisableBracketedPaste()` called on teardown before `scr.ExitAltScreen()`.
   - Handled `uv.PasteEvent` in the event loop: clears selection and routes to `cli.SearchEdit` (if search input is active) or `cli.SendInput` to forward raw bytes to the focused pane.
2. **Server Bracketed Paste Forwarding**:
   - Added `BracketedPaste() bool` to `term.Grid` interface and implemented on `vtGrid`.
   - In `internal/server/handlers.go`, when `protocol.MsgInput` contains `Data`, if `p.grid.BracketedPaste()` is true, wrap the bytes with `\x1b[200~` and `\x1b[201~`.
   - Routed `MsgInput.Data` through `p.SendBytes` so it queues into `p.input` in strict order with keys and mouse clicks, and avoids blocking `s.mu`.
3. **Local Clipboard Parallel Write**:
   - In `cmd/wideboi/main.go`, `writeClipboard` continues writing OSC 52, but also runs `writeLocalClipboard(text)`.
   - If not in an SSH session (`SSH_CLIENT`, `SSH_TTY`, `SSH_CONNECTION` are empty), writes to the local OS clipboard asynchronously in a background goroutine with a 500ms timeout (`pbcopy` on Darwin, `wl-copy`/`xclip`/`xsel` on Linux).
   - Mouse release remains the copy trigger.

## Verification

- `go test -count=1 ./cmd/wideboi` passed.
- `go test -count=1 ./internal/server/...` passed.
- `make quick` passed.
- `make check` passed 4 consecutive times with 0 failures across all 9 check gates.
