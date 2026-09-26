# Session Notes: Command palette & internal CLI via ephemeral server-managed panes

Session started in worktree `.worktrees/issue-251-command-palette`.
Baseline tests passed.
Tracking GitHub Issue #251.

## What was built
- **Internal Command Registry (`internal/commands`)**:
  - `commands.Registry` with command lookup by name and alias.
  - POSIX-style argument parsing supporting single quotes, double quotes, and escapes.
  - Built-in commands: `new-column`, `split`, `run`, `kill-pane`, `cycle-width`, `grow-width`, `shrink-width`, `set-width`, `move-left`, `move-right`, `toggle-cards`, `toggle-status`, `focus-left`, `focus-right`, `focus-last`, `detach`, `quit`, `help`.
  - Exported `WriteClientFrame` and `ReadServerFrame` in `internal/transport/frame.go` for synchronous one-shot client-server RPCs without goroutine pump leaks.
- **Built-in Interactive Subcommands (`cmd/wideboi/prompt.go` & `cmd/wideboi/palette.go`)**:
  - `wideboi prompt`: Terminal raw mode input with `: ` prompt, Backspace/Ctrl+U/Ctrl+W, Tab-completion of command names, and Enter/Esc handling.
  - `wideboi palette`: Terminal raw mode fuzzy picker listing commands, filtering live as query is typed, Up/Down navigation, Enter execution, and Esc cancellation.
  - Both accept `--caller-pane`, `--session`, `--socket` flags and communicate via `WIDEBOI_SOCK`.
- **Triggers in TUI and Web Clients**:
  - `internal/keys/keys.go`: Added `ActionPrompt` (`:`) and `ActionPalette` (`space`).
  - Help overlay constraint preserved: Collapsed width manipulation (`w`, `o`, `p`) into `helpWidth = "cycle / shrink / grow column width"`, allowing `:/space` to be added while keeping the help overlay exactly at 24 rows (`TestHelpOverlayFitsAt80x24`).
  - `cmd/wideboi/router.go` & `cmd/wideboi/main.go`: Dispatches ephemeral pane spawn via `cli.SendSplit` with `--caller-pane` and `AfterPaneID`.
  - `web/src/key-router.ts` & `web/src/wideboi-app.ts`: Dispatches split request for `wideboi prompt` and `wideboi palette` when `prefix :` or `prefix space` is pressed.
- **Verification**:
  - Added smoke tests in `scripts/smoke.py` covering prompt and palette invocation, cancellation, and clean focus restoration.
  - All unit, web, browser (playwright), race, exit, and smoke test suites passed (`make check`).
