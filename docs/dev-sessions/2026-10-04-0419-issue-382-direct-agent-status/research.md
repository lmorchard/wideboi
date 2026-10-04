# Research: Direct Agent Status Reporting (Issue #382)

## 1. Context & Motivation

Wideboi observes pane activity via:
- Semantic prompt sequences (OSC 133)
- Progress notifications (OSC 9;4)
- PTY write heuristics
- Screen tail blocker scans (#376)
- Inactivity timeouts (#383)

While these heuristics work automatically without configuration, real CLI agents (Claude Code, OpenCode, Codex) frequently run in environments where OSC sequences are not emitted. Screen-tail scanning is best-effort.

In [workmux](https://github.com/raine/workmux), agent status tracking is achieved with 100% determinism via direct lifecycle hooks (`workmux set-window-status [working|waiting|done|clear]`).

## 2. Architecture & Design

1. **Wire Protocol:**
   - Add `MsgSetPaneStatusRequest`:
     - `PaneID int`
     - `Status PaneStatus`
     - `Clear bool`
   - Add `MsgSetPaneStatusResponse`:
     - `Error string`
   - Bump `protocol.Version` to 27.
2. **Server Pane Status Override (`internal/server/pane.go`):**
   - Each `Pane` maintains an explicit status override:
     - `SetExplicitStatus(st protocol.PaneStatus)`
     - `ClearExplicitStatus()`
   - In `Pane.Status()`: explicit status takes precedence over PTY output heuristics and OSC heuristics, while process exit status still governs reaped processes.
3. **CLI Command (`wideboi set-pane-status`):**
   - Callable from inside a child pane (uses `$WIDEBOI_PANE_ID` automatically) or with an explicit pane ID:
     ```bash
     wideboi set-pane-status working
     wideboi set-pane-status input
     wideboi set-pane-status done
     wideboi set-pane-status idle
     wideboi set-pane-status clear
     ```
   - Also available as internal command `:set-pane-status`.
4. **Agent Integration Documentation:**
   - Document Claude Code hooks (`UserPromptSubmit`, `Notification`, `Stop`) in `docs/skills/wideboi-control/SKILL.md` and `docs/AGENT-INTEGRATIONS.md`.
   - Document OpenCode plugin example for event-driven status reporting.
