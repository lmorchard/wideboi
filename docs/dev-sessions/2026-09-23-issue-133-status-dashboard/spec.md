# Status Dashboard Spec

**Goal:** Add a `wideboi status` CLI command that outputs a snapshot of all active panes in the current session, showing their titles, sizes, and operational statuses (e.g., idle vs working) for external dashboarding or scripting.

**Source:** https://github.com/lmorchard/wideboi/issues/133

## Current state

- The multiplexer server tracks sessions and pane state natively (`internal/server/server.go:24`).
- It captures terminal metadata like titles via OSC 0/1/2 (`internal/server/term/grid.go:247`) and command status (agent responses, working vs idle) via OSC 133 (`internal/server/term/grid.go:255`).
- On connection, a client receives a `protocol.MsgLayoutSnapshot` containing the complete tree layout (`Columns[]`), focus, and metadata `PaneStatuses` / `PaneTitles` for all panes (`internal/protocol/messages.go`).
- Other simple CLI commands like `wideboi ls` (list sessions) exist, demonstrating a pattern of one-shot CLI commands (`cmd/wideboi/sessions.go`, `cmd/wideboi/main.go`).

## Desired end state

- A new `wideboi status` command (with `status` as a CLI subcommand).
- Behavior: connects to the server (default or specified by `-L`/`-s`), requests the layout snapshot, formats it, prints it to standard output, and exits 0.
- If the server cannot be reached, it prints an error and exits 1.
- Output formats:
  - Default: A human-readable text table listing panes, their dimensions, focus state, status, and title.
  - `--json`: Structured JSON containing the exact layout snapshot data for `jq` and external tooling.
- Connection: it behaves like `wideboi attach` but closes the connection immediately after receiving the first `MsgLayoutSnapshot`.

## Design decisions

- **Decision:** Implement as a one-shot CLI client rather than a server-managed persistent pane.
  - **Why:** Fits immediately into standard shell pipelines and external dashboards (e.g., `watch wideboi status`). Modifying the multiplexer tree to support "special" non-PTY panes is a much larger architectural shift that breaks the 1:1 pane-to-PTY invariant.
  - **Rejected:** Native server-side TUI pane (deferred to future work if needed, prioritizing the simpler composable primitive).
- **Decision:** Rely exclusively on `MsgLayoutSnapshot`.
  - **Why:** The server already broadcasts this message immediately to every new transport connection (in `Server.Attach`). It contains all necessary columns, titles, and statuses. No new server protocol message is needed.
- **Decision:** Human-readable format as a simple tab-aligned table.
  - **Why:** Standard CLI output style. Easy to read without pulling in heavy TUI libraries.
- **Decision:** `--json` outputs exactly the data contained in `protocol.MsgLayoutSnapshot` (optionally augmented with `Focus` boolean per pane).
  - **Why:** Minimal transformation necessary; provides all data directly for scripters.

## Patterns to follow

- **CLI Flag parsing:** Add `--json` flag handling in `parseCLI` in `cmd/wideboi/main.go`.
- **Command routing:** Add `status` to the subcommand switch in `main.go`, routing to `runStatus(cfg, jsonOut)`.
- **Connection logic:** Model the connection off `runKillSession` (`cmd/wideboi/main.go:210`). Create `transport.NewClientSocketConn`, run pumps, wait for the first message on `ServerSendChan()`.
- **Formatting:** Use Go's `text/tabwriter` for aligning the table (standard library, lightweight).

## What we're NOT doing

- We are NOT implementing a continuous interactive TUI client (`watch` mode) in this session.
- We are NOT modifying the server's layout tree to include non-PTY panes.
- We are NOT changing what data is tracked via OSC 133 or Titles.
- We are NOT adding persistent state logging to disk; this is a live snapshot only.

## Open questions

*(None remaining, decisions are locked in).*
