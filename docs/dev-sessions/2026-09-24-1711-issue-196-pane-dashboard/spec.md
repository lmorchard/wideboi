# In-App Pane Status Dashboard Spec

**Goal:** Provide a live, server-managed status dashboard pane that lists all panes and their statuses, can sit alongside terminals persistently, and enables keyboard and mouse navigation to any pane.

**Source:** https://github.com/lmorchard/wideboi/issues/196

## Current state

- Wideboi supports one-shot pane status reporting via `wideboi status [--json]` (`cmd/wideboi/status.go:24-100`).
- The server tracks pane dimensions, statuses, titles, CWDs (via OSC 7), and user variables (via OSC 1337) and broadcasts them through `MsgLayoutSnapshot` and `MsgPaneMetadata` (`internal/server/server.go:909-1050`).
- Every pane is currently backed by a PTY process (`ptyx.Spawn`) running a shell (`internal/server/pane.go:83-97`, `internal/server/server.go:628-666`).
- Pane focus is client-local presentation (`internal/client/client.go:1060-1087`, `internal/client/client_test.go:38-74`).
- Overlays exist client-side only for the static help popup (`internal/client/help.go:12-140`).

## Desired end state

1. **Dashboard Pane Lifecycle:**
   - Pressing `<prefix> s` invokes `VerbToggleStatus`.
   - If no dashboard pane exists, one is spawned immediately to the right of the focused pane in the strip (matching how `VerbNewColumn` works).
   - If a dashboard pane already exists, `<prefix> s` focuses that dashboard pane.
   - The dashboard pane is a server-managed non-PTY pane using an in-memory `term.Grid` (VT emulator).
   - The dashboard pane can be closed via standard `<prefix> x` (`VerbKillPane`).
   - If the dashboard pane is the only remaining pane in the session (e.g. all terminal panes close or exit), the session terminates cleanly.
   - The dashboard pane omits itself from the list of panes it renders.

2. **Dashboard Visual Presentation:**
   - The dashboard formats a live table of all active terminal panes:
     - Selection indicator cursor (`>` and inverse/highlight background on the selected row).
     - Column index / Pane ID.
     - Status glyph and text (`● idle`, `▲ working`, `! attention`, `✔ done`, `✖ failed`).
     - Focused pane marker (indicating which pane was previously/currently focused by the client).
     - Title (truncated to fit available width).
     - Working directory (CWD) from OSC 7.
   - Updates to any pane's status, title, or CWD immediately re-render the dashboard grid, triggering standard delta patch broadcasting to attached clients.
   - Enables mouse tracking (`\x1b[?1000h\x1b[?1006h`) so mouse clicks are forwarded.

3. **Interactive Navigation:**
   - When the dashboard pane is focused:
     - `j` or Down arrow: Moves row selection down (clamped to the number of rows).
     - `k` or Up arrow: Moves row selection up (clamped to 0).
     - `Enter`: Jumps to the selected pane.
     - Mouse click on a row: Selects the row and jumps to that pane.
   - Navigation jumping sends a `MsgFocusPane{PaneID: targetID}` message to the initiating client, which focuses that pane locally via `c.strip.FocusPaneID(targetID)`.
   - The dashboard pane remains open after jumping so it can remain visible as a live overview.

4. **Documentation & Keybindings:**
   - Documented in help overlay (`internal/keys/keys.go`, `internal/client/help.go`).
   - Documented in README and manpage.

## Design decisions

- **Decision:** Implement the dashboard as a server-managed non-PTY pane rather than a client-side modal overlay.
  - **Why:** Allows the dashboard to be left always visible alongside terminals in wide scrolling or card layouts, and automatically renders across both terminal and web clients over the existing wire protocol.
  - **Rejected:** Client-side modal overlay dialog. While simpler, an overlay cannot be pinned side-by-side with running terminals.
- **Decision:** Spawn to the right of the currently focused pane.
  - **Why:** Matches user expectation and existing `new_column` (`VerbNewColumn`) behavior.
  - **Rejected:** Always pinning at column 0 or end of strip.
- **Decision:** Add server->client `MsgFocusPane{PaneID: int}` for navigation jumps.
  - **Why:** Client focus is client-local presentation. The server cannot unilaterally set all clients' focus; instead, when an attached client presses `Enter` or clicks on the dashboard, the server instructs that specific client to switch focus.
  - **Rejected:** Global server-managed focus (would break multi-client independent focus).
- **Decision:** Omit the dashboard pane from its own listing.
  - **Why:** A dashboard pane monitoring itself adds noise and no utility.
- **Decision:** End session when only the dashboard pane remains.
  - **Why:** Prevents orphaned sessions with no active terminal work when shells exit.

## Patterns to follow

- Pane creation and strip placement: `internal/server/server.go:628-666` (`spawnPaneWithSpecLocked`).
- Pane lifecycle and reap: `internal/server/server.go:668-685` (`onPaneExit`).
- Wire protocol extensions and protobuf schemas: `internal/protocol/wirepb/wideboi.proto`, `internal/protocol/messages.go`, and version bump in `internal/protocol/version.go` per `docs/LESSONS.md`.
- Status bar and help key bindings: `internal/keys/keys.go:153-203`.

## What we're NOT doing

- We are not adding interactive process manipulation (e.g. killing or restarting child panes from inside the dashboard via keyboard shortcuts); navigation/focus jumping is the sole action.
- We are not building bespoke web UI client dashboard components in this issue; the server-managed VT grid renders automatically in web clients.
- We are not adding arbitrary query/filter search fields inside the dashboard in this initial slice.

## Open questions

- None. All 4 design questions (placement, persistence, self-omission, lifecycle when alone) were resolved during brainstorm.
