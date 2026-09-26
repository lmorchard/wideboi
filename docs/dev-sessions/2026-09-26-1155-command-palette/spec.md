# Command Palette & Internal CLI via Ephemeral Panes Spec

**Goal:** Provide an internal command palette and interactive command prompt in wideboi using ephemeral server-managed PTY panes as UI primitives ("Pane-as-UI"), enabling discoverable action execution, fuzzy search, and persistent output without building bespoke frontend widgets.

**Source:** https://github.com/lmorchard/wideboi/issues/251

## Current state

- **Control Mode & Shortcuts:** Keystrokes are captured by `cmd/wideboi/router.go:151-191` after the prefix key (default `ctrl+b`). Mappings in `internal/keys/keys.go:24-50` map single keys to `ActionVerb`, `ActionScroll`, `ActionSearch`, `ActionHelp`, etc.
- **Search & Help Modes:** Search (`/`) and Help (`?`) are handled as client-local modes (`cmd/wideboi/router.go:102-149`, `internal/client/search.go:17-55`). Search replaces the status line (`internal/client/client.go:1298-1301`).
- **Pane Lifecycle:** `cmd/wideboi/control.go:170-238` and `internal/server/server.go:500-518` handle `MsgSplitRequest{Command, Cwd, AfterPaneID, Keep}`. Panes run in PTYs via `internal/server/ptyx/pane.go:57-102`.
- **Output & Exit:** When a kept pane (`Keep: true`) exits, its exit code is recorded, but the pane stays in the layout strip until explicitly closed (`internal/server/server.go:1027-1050`). Unkept panes automatically clean up on EOF (`internal/server/server.go:1012-1015, 1097-1136`).
- **IPC & Environment:** Child processes in panes inherit the server environment including `WIDEBOI_SOCK` (`internal/server/ptyx/pane.go:64`, `internal/config/config.go:368`), enabling them to execute CLI commands directly against the running server (`cmd/wideboi/control.go:27-71`).

## Desired end state

1. **Internal Commands Package (`internal/commands`):**
   - A registry of canonical commands (`focus`, `new-column`, `split`, `cycle-width`, `kill-pane`, `toggle-cards`, `theme`, `detach`, `quit`, etc.) with metadata (name, description, aliases, category, handler).
   - Capable of executing commands by dispatching either client actions or server RPCs.

2. **Built-in Ephemeral CLI Tools:**
   - `wideboi palette`: An interactive terminal UI running directly in a pane that presents a fuzzy-searchable list of available commands and actions. Filtering updates live; pressing `Enter` fires the selected command via `WIDEBOI_SOCK` and exits with code 0; pressing `Esc` or `Ctrl+c` exits without action.
   - `wideboi prompt`: An interactive terminal prompt running in a pane (`: `) that accepts command lines with arguments and flags (e.g. `:rename my-pane`, `:new-column --cwd /tmp`). Supports basic line editing, Tab completion, and history.

3. **Client Triggers & Ephemeral Pane Lifecycle:**
   - `ctrl+b Space` triggers the fuzzy command palette.
   - `ctrl+b :` triggers the interactive command prompt.
   - When triggered, client requests the server to spawn an ephemeral pane running `wideboi palette` or `wideboi prompt` immediately adjacent to the current pane (`AfterPaneID = c.focusPaneID`).
   - The ephemeral pane is focused automatically upon creation.
   - When the palette or prompt process exits, the server cleans up the pane (unkept pane behavior) and automatically restores focus to the calling pane.

4. **Command Output in Kept Scratch Panes:**
   - Commands executed from the prompt or palette that produce multi-line output (e.g. `:status`, `:help [cmd]`, or arbitrary shell commands) can run in a kept pane (`Keep: true`), leaving output available for review with scrollback and search.

## Design decisions

- **Decision:** Use ephemeral server-managed PTY panes ("Pane-as-UI") rather than client-side modal/popup widgets.
  - **Why:** Reuses wideboi's core terminal rendering, ANSI styling, font rendering, mouse/keyboard routing, and PTY pipeline. Both the TUI terminal client and Web browser client get the feature immediately without duplicate UI code.
  - **Rejected:** Building bespoke overlay widgets in Go Ultraviolet for TUI and Lit/TypeScript for Web.

- **Decision:** Self-contained built-in subcommands (`wideboi palette` and `wideboi prompt`).
  - **Why:** Zero runtime dependencies (no requirement for `fzf` or `gum` on the host system), predictable key handling and rendering across platforms, and direct integration with internal command registry.
  - **Rejected:** Relying on external shell scripts or third-party tools by default.

- **Decision:** Position the ephemeral pane as a temporary adjacent column/card (`AfterPaneID`).
  - **Why:** Naturally integrates into the existing strip and card fan layouts without introducing floating window / popup compositor complexity into the server.
  - **Rejected:** Adding a new overlay window system to the layout engine.

- **Decision:** Process communication via existing `WIDEBOI_SOCK`.
  - **Why:** The server socket is already inherited by child processes in panes and provides authenticated, version-checked RPC to the server.
  - **Rejected:** Creating special-purpose pipes or IPC channels just for the palette.

## Patterns to follow

- **CLI subcommand pattern:** Follow `cmd/wideboi/main.go:81-90` and `cmd/wideboi/control.go:170-238` for adding CLI subcommands.
- **Client key routing:** Follow `cmd/wideboi/router.go:167-200` and `internal/keys/keys.go:57-80` for binding actions to prefix keys.
- **PTY spawning & layout insertion:** Follow `internal/server/server.go:507-518, 967-1019` for spawning child panes with specific `AfterPaneID`.
- **Focus restoration:** Follow `internal/layout/layout.go:275-291` and `internal/client/client.go:397-404` for managing caller focus transitions.

## What we're NOT doing

- Not adding floating / overlay window compositing to the terminal emulator layout engine (palette remains an adjacent column/card).
- Not building bespoke HTML/DOM modals in the web client (the web client interacts with the palette pane through the existing canvas PTY stream).
- Not supporting arbitrary external plugin runtimes; palette is built into `wideboi`.
- Not supporting user-defined custom scripting languages inside the prompt (commands map to Go action dispatchers).

## Open questions

*(None. All design choices resolved with default answers.)*
