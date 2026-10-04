# Spec: Native wait-output and wait-status Commands (Issue #377)

**Goal:** Add native blocking server-side wait primitives `wait-output` and `wait-status` to eliminate bash polling loops in headless agent orchestration.

**Source:** https://github.com/lmorchard/wideboi/issues/377

---

## Requirements

### 1. Protocol Messages
- `MsgWaitOutputRequest`:
  - `PaneID int`
  - `Match string` (substring search)
  - `Regex string` (regex search)
  - `Lines int` (tail lines to inspect; default 50)
- `MsgWaitOutputResponse`:
  - `PaneID int`
  - `MatchedLine string`
  - `Error string`
- `MsgWaitStatusRequest`:
  - `PaneID int`
  - `Until []PaneStatus` (target statuses)
- `MsgWaitStatusResponse`:
  - `PaneID int`
  - `Status PaneStatus`
  - `Error string`
- Wire `protocol.Version` bumped to 28.

### 2. Server Behavior
- **Immediate match check:**
  - `wait-output`: if the pane already has a line matching `match` or `regex` within `lines`, returns immediately.
  - `wait-status`: if the pane is already in one of the requested statuses, returns immediately.
- **Async waiting:**
  - If not matched, registers waiter under server mutex.
  - On PTY output write (`p.onOutput`), checks output waiters on that pane.
  - On status transitions (`SetExplicitStatus`, `ClearExplicitStatus`, exit reap, layout ticks), checks status waiters on that pane.
  - On pane close, notifies waiters with error.
  - On client disconnect, cleans up registered waiters.

### 3. CLI Commands
- `wideboi wait-output [flags] <pane-id> [pattern]`
  - Flags: `--match <str>`, `--regex <pat>`, `--lines <N>`, `--timeout <duration>`, `-L <session>`, `-s <socket>`.
  - Can omit `<pane-id>` when running inside a pane (`$WIDEBOI_PANE_ID`).
  - Positional `[pattern]` acts as `--match` if neither `--match` nor `--regex` is given.
  - On match: prints matched line to stdout, exits 0.
  - On timeout: exits 124.
  - On error: prints error to stderr, exits 1.
- `wideboi wait-status [flags] <pane-id> [status]`
  - Flags: `--until <status1,status2...>`, `--timeout <duration>`, `-L <session>`, `-s <socket>`.
  - Can omit `<pane-id>` when running inside a pane.
  - Positional `[status]` acts as `--until`.
  - On match: prints matched status name to stdout, exits 0.
  - On timeout: exits 124.
  - On error: prints error to stderr, exits 1.

### 4. Interactive Commands
- Register `:wait-output` and `:wait-status` in `internal/commands/registry.go`.

### 5. Documentation
- Update `docs/skills/wideboi-control/SKILL.md` to showcase `wait-output` and `wait-status`.
