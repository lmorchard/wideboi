# Notes: Issue 258 - Palette/prompt `detach` and `set-width`

## Summary of Decisions & Findings

1. **`set-width` command**:
   - In `internal/server/handlers.go`, `handleSetPaneWidthLocked` previously enforced `tp == s.sizeOwner`.
   - Palette/prompt subcommands run in separate processes that connect to the server via an ephemeral socket connection `tp`, which is never `s.sizeOwner`.
   - Updated `handleSetPaneWidthLocked` to check `(tp == s.sizeOwner || !s.isAttachedLocked(tp))` so that unattached command connections can set pane width while keeping attached viewer clients restricted from overriding host geometry.
   - In `internal/commands/registry.go`, `set-width` now validates that `inv.CallerPaneID > 0` and checks width bounds against `layout.MinColumnWidth` and `layout.MaxColumnWidth`.

2. **Palette argument preservation**:
   - `cmd/wideboi/palette.go` previously only passed `matches[selected].Name` when Enter was pressed.
   - Added `buildExecLine(cmdName, rawQuery)` in `cmd/wideboi/palette.go` so that if the user typed trailing arguments in the search box (e.g. `width 60` or `set-width 60`), the argument string is forwarded to `commands.DefaultRegistry.Execute`.
   - Added query fallback in `filterCommands` to match the first word as a command or alias when trailing arguments are present.

3. **`detach` command via client trigger file**:
   - An attached client auto-reconnects on unexpected socket EOF unless the client initiates the detach (`hungUp.Store(true)` and `guard.Stop()`).
   - Added `DetachFile string` to `commands.Invocation`.
   - Added `--detach-file` flag to `wideboi prompt` and `wideboi palette`.
   - In `internal/commands/registry.go`, `detach` writes to `inv.DetachFile` if specified, or falls back to `SendClientMsg(ctx, inv, protocol.MsgDetach{})`.
   - In `cmd/wideboi/main.go`, when spawning `prompt` or `palette`, a unique temporary trigger file is passed via `--detach-file`.
   - On pane exit / server message / frame tick, `main.go` checks if `detachTriggerFile` was written; if so, it executes clean client detach (`performDetach()`), restoring terminal raw mode, printing the detach notice, and exiting 0 while leaving the server alive.

4. **Split focus discovery**:
   - In `internal/server/handlers.go:handleSplitRequestLocked`, `eff.createdPaneID` and `eff.focusTargetID` were not previously being set when `MsgSplitRequest` spawned a pane.
   - Setting `eff.createdPaneID = p.ID()` and `eff.focusTargetID = p.ID()` ensures the server sends `MsgFocusPane{PaneID: p.ID()}` to the client that requested the split, which focuses newly created ephemeral prompt/palette panes automatically.

5. **Copilot Review Fixes**:
   - Detach trigger directory safety: placed detach trigger file in a private `0700` temporary directory (`os.MkdirTemp("", "wb-detach-*")`) and opened with `os.OpenFile(..., os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)` to guard against symlink attacks.
   - Whitespace preservation in palette execution: `buildExecLine` now trims the command prefix and preserves exact argument whitespace rather than collapsing with `strings.Fields`.
   - Palette integration testing: added `TestPaletteExecutesSetWidth` in `cmd/wideboi/palette_test.go` to verify end-to-end execution through a live server socket.
   - Web client scope: terminal client detachment via `--detach-file` coordinates terminal raw mode and process teardown; browser clients operate over WebSockets where tabs are closed rather than detached.
