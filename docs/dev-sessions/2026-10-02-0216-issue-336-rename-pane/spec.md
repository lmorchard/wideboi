# User-Defined Pane Titles and Renaming (rename-pane) Spec

**Goal:** Enable users to set, override, and clear custom titles on panes via CLI, in-session prompt, and web prompt, keeping terminal cards, slivers, and dashboards clearly labeled.

**Source:** GitHub Issue #336

## Current state

- Child processes emit terminal titles via OSC 0/1/2, parsed by `x/vt` and `oscScanner` (`internal/server/term/oscfix.go:50-138`) and stored in `vtGrid.title` (`internal/server/term/grid.go:241, 549`).
- `Pane.Title()` (`internal/server/pane.go:413`) directly forwards to `p.grid.Title()`.
- Server gathers titles across all panes in `paneTitlesLocked()` (`internal/server/server.go:801`) and broadcasts them in `MsgLayoutSnapshot.PaneTitles` (`internal/server/server.go:995-1004`, `internal/protocol/messages.go:340-342`).
- The 33ms server tick checks `!sameStringMap(s.paneTitlesLocked(), s.lastTitles)` (`internal/server/server.go:973`) to trigger layout broadcasts when any title changes.
- In-place binary upgrades serialize pane metadata into `UpgradePane` (`internal/server/upgrade.go:48-60`), but only persist the child terminal title in `term.GridSnapshot`.
- Clients (TUI and Web) render pane titles directly from `MsgLayoutSnapshot.PaneTitles`:
  - TUI card headers and sliver spines: `internal/client/render.go:168-207`.
  - TUI status dashboard: `internal/server/dashboard.go:72, 98-105`.
  - Web card headers and sliver spines: `web/src/wideboi-pane.ts:60-77, 363`.
  - Web tabs: `web/src/wideboi-app.ts:1768-1811`.
  - Web mobile selector: `web/src/components/mobile-bar.ts:68-78`.

## Desired end state

1. **CLI Command**:
   - `wideboi rename-pane [flags] [pane-id] [title]`
   - Aliases supported at the CLI parser level or via commands registry.
   - If `[pane-id]` is provided (e.g. `wideboi rename-pane 2 "API Server"`), targets pane 2.
   - If `[pane-id]` is omitted (e.g. `wideboi rename-pane "API Server"`), uses `$WIDEBOI_PANE_ID`. If `$WIDEBOI_PANE_ID` is not set (run outside a wideboi pane), fails with an error: `usage: wideboi rename-pane [flags] <pane-id> <title> (pane-id required outside wideboi pane)`.
   - If `title` is `""` or omitted (e.g. `wideboi rename-pane 2 ""`), clears the custom title override and restores child OSC 0/2 title tracking.
2. **In-Session Command Registry**:
   - Add `rename-pane` (aliases: `title`, `label`) to `internal/commands/registry.go`.
   - Supports:
     - `:rename-pane "API Server"` or `:title "API Server"` (targets caller pane).
     - `:rename-pane 2 "API Server"` or `:title 2 "API Server"` (targets pane 2).
     - `:title ""` or `:title` (clears override on caller pane).
     - `:title 2 ""` (clears override on pane 2).
3. **Protocol Wire Messages**:
   - Add `MsgRenamePaneRequest` and `MsgRenamePaneResponse`:
     ```protobuf
     message MsgRenamePaneRequest {
       int32 pane_id = 1;
       string title = 2;
       bool clear = 3;
     }

     message MsgRenamePaneResponse {
       int32 pane_id = 1;
       string error = 2;
     }
     ```
   - Bump `protocol.Version` from 21 to 22 in `internal/protocol/version.go`.
   - Bump `PROTOCOL_VERSION` in `web/src/version.ts` to 22.
4. **Server State and Pane Model**:
   - `Pane` tracks `customTitle string` and `hasCustomTitle bool` (protected by mutex).
   - `Pane.SetCustomTitle(title string)` sets custom title and marks override active.
   - `Pane.ClearCustomTitle()` removes override.
   - `Pane.Title()` returns custom title if override is active, else `p.grid.Title()`.
   - `Pane.CustomTitle()` returns `(title string, ok bool)` for serialization.
   - Server handles `MsgRenamePaneRequest`: checks pane exists, updates title, triggers layout broadcast, updates dashboard, returns response.
5. **Persistence Across In-Place Upgrades (#250 / #326)**:
   - Add `CustomTitle string` and `HasCustomTitle bool` to `UpgradePane` in `internal/server/upgrade.go`.
   - `buildUpgradeStateLocked` saves custom title; `RestoreState` restores it onto the reconstructed `Pane`.
6. **Web Client Prompt**:
   - Add `rename-pane`, `title`, `label` to `WideboiApp.handlePromptCommand` in `web/src/wideboi-app.ts`.
   - Dispatches `renamePaneRequest` over WebSocket.

## Design decisions

- **Decision:** Use an RPC pair (`MsgRenamePaneRequest` / `MsgRenamePaneResponse`) over the protocol wire.
  - **Why:** Allows CLI invocation and internal command execution to detect and report non-existent pane IDs synchronously to the user.
  - **Rejected:** Fire-and-forget message (`MsgRenamePane`). Fire-and-forget makes CLI exit 0 silently when an invalid pane ID is passed.
- **Decision:** Target resolution when `[pane-id]` is omitted defaults to `$WIDEBOI_PANE_ID`, and errors if run outside a session.
  - **Why:** Prevents accidental renames of unintended panes when running scripts or commands from external terminal windows.
  - **Rejected:** Silently defaulting to the currently focused pane when run outside wideboi.
- **Decision:** Empty string `""` or 0-argument invocation clears the custom title override.
  - **Why:** Natural, low-friction way to reset a pane back to tracking child OSC 0/2 titles without needing a separate `--clear` flag or command.
  - **Rejected:** Requiring a distinct `clear-title` command or mandatory `--clear` flag.
- **Decision:** Keep underlying `term.Grid` title intact and separate from `Pane` custom title.
  - **Why:** When a child process (e.g. Claude Code, zsh, vim) emits OSC 0/2 while a custom title is active, the child title continues to be recorded in the terminal emulator. Clearing the custom title immediately reveals the up-to-date child title without delay or blanking.
  - **Rejected:** Overwriting `g.title` directly on the emulator, which would permanently discard the child's title.
- **Decision:** Bump `protocol.Version` to 22.
  - **Why:** `docs/LESSONS.md:442-452` mandates bumping `protocol.Version` for new message types on the wire so mismatched versions handshake-refuse cleanly instead of encountering framing errors.
  - **Rejected:** Reusing existing message types or avoiding the version bump.

## Patterns to follow

- Command definition & execution: `internal/commands/registry.go:252-276` (`kill-pane`).
- CLI subcommand registration & dispatch: `cmd/wideboi/main.go:127, 353-354` and `cmd/wideboi/control.go:320-359` (`runClose`).
- Server request handling: `internal/server/handlers.go:250-274` (`handleClosePane`).
- Upgrade state serialization & restoration: `internal/server/upgrade.go:48-60, 140-147, 320-350`.
- Web client command prompt dispatch: `web/src/wideboi-app.ts:1453-1460`.

## What we're NOT doing

- Not adding an interactive floating modal or popup dialog in TUI (internal prompt `:title` and CLI `wideboi rename-pane` fully satisfy the feature).
- Not modifying OSC 0/1/2 parser callbacks in `x/vt` or `oscfix.go`.
- Not persisting custom titles to disk across reboot (that is scoped to #326 session resurrection; we only ensure the upgrade snapshot struct `UpgradePane` carries it).
- Not adding fuzzy pane renaming by pane title regex or glob.

## Readiness checklist

1. **Placeholder scan:** No TBDs or un-answered questions.
2. **Internal consistency:** CLI, prompt, protocol, server model, upgrade persistence, and web client all agree on semantics and data shapes.
3. **Scope bounded:** Explicit "What we're NOT doing" bounds scope.
4. **No load-bearing ambiguity:** Command arguments, pane-id resolution, clearing semantics, and version bumps are explicitly pinned.
