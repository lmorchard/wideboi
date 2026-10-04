# Spec: Direct Agent Status Reporting (Issue #382)

**Goal:** Provide a direct, deterministic CLI and socket API (`wideboi set-pane-status`) for agents and hooks to explicitly set or clear a pane's status (`working`, `input`, `done`, `failed`, `idle`, `clear`), providing 100% deterministic tracking.

**Source:** https://github.com/lmorchard/wideboi/issues/382

---

## Current State

- Wideboi relies on OSC sequences (133/9;4), PTY write heuristics, screen tail pattern scanning (#376), and inactivity timeouts (#383).
- There is currently no CLI subcommand or RPC message for an external agent process or hook to explicitly declare its status to Wideboi.

---

## Desired End State

1. **Protocol Message:**
   - `MsgSetPaneStatusRequest`:
     ```protobuf
     message MsgSetPaneStatusRequest {
       int32 pane_id = 1;
       PaneStatus status = 2;
       bool clear = 3;
     }
     message MsgSetPaneStatusResponse {
       string error = 1;
     }
     ```
   - Wire `protocol.Version` bumped to 27.

2. **Server Handling:**
   - In `internal/server/pane.go`, `Pane` gains `SetExplicitStatus(st protocol.PaneStatus)` and `ClearExplicitStatus()`.
   - `Pane.Status()` returns the explicit status if set; otherwise falls back to `grid.Status()`. If the pane's process exited in `--keep` mode, exit status governs.
   - When explicit status changes, server updates the dashboard and broadcasts layout.

3. **CLI Command:**
   - Syntax:
     ```bash
     wideboi set-pane-status [status] [pane-id]
     ```
   - Status keywords:
     - `working` &rarr; `StatusWorking`
     - `input` / `needs_input` &rarr; `StatusNeedsInput`
     - `done` &rarr; `StatusDone`
     - `failed` &rarr; `StatusFailed`
     - `idle` &rarr; `StatusIdle`
     - `clear` &rarr; clears explicit override, returning to heuristic/OSC tracking
   - When run inside a pane (`$WIDEBOI_PANE_ID` set), `pane-id` can be omitted.
   - Target flags (`-L <session>`, `-s <socket>`) supported.
   - Exits 0 on success; prints clean error on stderr and exits 1 on failure.

4. **Internal Command:**
   - `:set-pane-status [status] [pane-id]` registered in `commands.DefaultRegistry`.

5. **Hook Documentation:**
   - Add Claude Code and OpenCode integration guide to `docs/skills/wideboi-control/SKILL.md`.

---

## Verification Plan

- Unit tests in `internal/protocol/`:
  - `TestSetPaneStatusRequestCodec`: Verify marshal/unmarshal round-trip.
  - `TestWireSchemaMatchesProtocolVersion`: Verify v27 schema hash.
- Unit tests in `internal/server/`:
  - `TestSetPaneStatusExplicitOverride`: Set status via RPC, assert `pane.Status()` reports explicit status, clear status, assert it resumes grid status.
- Integration tests in `cmd/wideboi/control_test.go`:
  - `TestSetPaneStatusCommand`: Test `wideboi set-pane-status` with explicit ID, environment `$WIDEBOI_PANE_ID`, all status keywords, and error handling.
- Run `make quick` and ensure clean passes.
