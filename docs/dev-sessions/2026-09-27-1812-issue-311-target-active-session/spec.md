# Native In-Client Command Palette, Prompt, and Session Environment Spec

**Goal:** Replace out-of-process ephemeral panes for command palette and prompt with native in-client UI in both web and TUI clients, and inject active session environment variables (`WIDEBOI_SOCK` and `WIDEBOI_SESSION`) into all spawned panes.

**Source:** https://github.com/lmorchard/wideboi/issues/311

## Current state

- Ephemeral terminal panes running `wideboi prompt` and `wideboi palette` were used to avoid duplicating UI, but incurred significant friction:
  - Spawning an entire PTY process, running shell lookups, negotiating IPC against unix sockets, and disturbing the terminal strip layout.
  - In `web/src/wideboi-app.ts:793-807`, `openPrompt` and `openPalette` sent `wideboi prompt` / `wideboi palette` commands without `--socket` or binary path, which crashed on non-default sessions or when `wideboi` was not in `$PATH`.
  - On web/mobile, terminal-based prompt and palette felt clumsy compared to native HTML dialogs.
  - In the console client (`cmd/wideboi/main.go:1148-1168`), complex choreography was required (`--caller-pane`, `--socket`, `--detach-file`).
- Furthermore, `ptyx.Spawn` (`internal/server/ptyx/pane.go:120`) did not inject `WIDEBOI_SOCK` or `WIDEBOI_SESSION` into child environments, leaving user shells and subcommands unaware of the active session.
- `internal/config/config.go:applySessionLayer` rejected having both `WIDEBOI_SESSION` and `WIDEBOI_SOCK` set in the environment, even when they pointed to the same session.

## Desired end state

1. **Active Session Environment Injection:**
   - In `internal/config/config.go:applySessionLayer`, allow `session != ""` and `socket != ""` if `socket == SessionSocketPath(session)`. Divergent values continue to return an error.
   - When the server spawns panes (`internal/server/ptyx/pane.go` / `internal/server/server.go`), child processes receive `WIDEBOI_SOCK` set to the active server socket and `WIDEBOI_SESSION` set to the session name (if running a named session). Existing stale session variables from outer environments are stripped.
   - Any `wideboi` command run inside a shell pane (e.g. `wideboi split`, `wideboi status`, `wideboi new`) automatically connects to the host session.

2. **Web Client Native Command Palette & Prompt (`web/src/`):**
   - The `<wideboi-command-menu>` component becomes an interactive Command Palette:
     - Includes a filter search `<input>` auto-focused on open.
     - Live filters commands by label and description.
     - Supports keyboard navigation (Up/Down arrow keys, Enter to execute, Escape to cancel).
     - Supports entering prompt mode (e.g., typing `: <command>` or selecting the "Command Prompt" item).
     - Executes actions directly using native client messages (`MsgVerb`, `MsgSplitRequest`, `MsgSetPaneWidth`, `MsgClosePaneRequest`) without spawning ephemeral panes.
   - Deprecated ephemeral pane spawns (`openPrompt()`, `openPalette()`) are removed.

3. **Console TUI Native Prompt & Palette (`internal/client/`, `cmd/wideboi/`):**
   - **Command Prompt (`:`):**
     - Handled in-process via status bar input (similar to search mode).
     - Prefix `:` displays prompt `: <input>_`. Enter executes the command via `commands.DefaultRegistry.Execute(...)` directly in-process; Esc cancels.
   - **Command Palette (`Space`):**
     - Handled in-process via an overlay modal box (similar to `drawHelpOverlay`).
     - Displays a search query line `> <filter>_` with live filtered command matches from `commands.DefaultRegistry.All()`.
     - Up/Down keys select the active item; Enter executes via `commands.DefaultRegistry.Execute(...)`; Esc cancels.
   - Ephemeral pane CLI subcommands `wideboi prompt` and `wideboi palette` and detach file flags are retired.

## Design decisions

- **Decision:** In-client native UI for both web and console clients rather than external processes.
  - **Why:** Solves #311 at the root. Zero PTY/process overhead, no `$PATH` resolution issues, no window rebalancing/scrolling disruption, and superior UX on both mobile/web and desktop TUI.
  - **Rejected:** Patching external process `--socket` and binary resolution, which keeps all the IPC and layout friction.
- **Decision:** Use status bar for TUI Prompt (`:`) and centered overlay box for TUI Palette (`Space`).
  - **Why:** Matches existing terminal conventions (Vim-style `:` prompt in status line, Sublime/VS Code-style floating palette).
- **Decision:** Support matching `WIDEBOI_SOCK` and `WIDEBOI_SESSION` in `applySessionLayer`.
  - **Why:** Child shells and scripts can inspect `$WIDEBOI_SESSION` or use `$WIDEBOI_SOCK` without conflicting.

## Patterns to follow

- TUI Overlay rendering: `internal/client/help.go:drawHelpOverlay` and `compose.WriteStyled`.
- TUI Status bar input: `internal/client/search.go` and `internal/client/statusbar.go`.
- Command execution: `internal/commands/registry.go:DefaultRegistry.Execute`.
- Web Dialog & keyboard trap: `web/src/components/command-menu.ts`.

## What we're NOT doing

- We are not modifying the protobuf wire protocol (`wideboi.proto`) or bumping `protocol.Version`.
- We are not removing other CLI subcommands (`wideboi split`, `wideboi send`, `wideboi capture`, etc.).
- We are not removing the `commands.DefaultRegistry`—it remains the central command execution logic for both the CLI and in-process TUI.

## Open questions

None.
