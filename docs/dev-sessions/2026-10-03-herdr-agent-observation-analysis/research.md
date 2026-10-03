# Research & Comparative Analysis: Observing Agent Activity and Status (Wideboi vs. Herdr)

**Date:** 2026-10-03  
**Subject:** Comparative study of Herdr (`~/devel/herdr`) and Wideboi (`~/devel/wideboi`)  
**Related Issues:** #373, #374, #375, #376, #377, #379, #380  

---

## 1. Overview & Context

Both **Wideboi** and **Herdr** are modern terminal multiplexers designed for long-running, detached developer workflows with multiple concurrent terminal panes. Both specifically target AI coding agent sessions (Claude Code, OpenCode, Codex, Aider, etc.), providing persistent background servers, client detachment, and programmatic control over UNIX domain sockets.

However, each project approached the problem from different starting points:
- **Wideboi** started as an infinite horizontal scrolling / card strip multiplexer with high-performance VT emulation (Go, Ultraviolet / x/vt fork, Protobuf wire protocol) and thin terminal/web clients. Status observation centered around standards: OSC 133 semantic prompts, OSC 9;4 progress notifications, OSC 7 CWD, and an in-memory server-managed dashboard grid pane (`<prefix> s`).
- **Herdr** started with an explicit "agent runtime" philosophy (Rust, custom libghostty-vt, Ratatui). It treats agents as first-class primitives inside workspaces and panes, using a combination of process tree inspection, declarative pattern matching manifests on screen tails, state debouncing, and inter-agent coordination APIs.

This document captures the architectural findings from analyzing Herdr and outlines actionable lessons for Wideboi.

---

## 2. Agent Activity Observation: Beyond OSC & PTY Silence

### Wideboi's Current Observation Model
Wideboi (`internal/server/term/grid.go`, `internal/server/pane.go`) tracks status through:
1. **OSC 133 (FinalTerm Semantic Prompts):**
   - `133;A` / `133;B` &rarr; `StatusNeedsInput`
   - `133;C` &rarr; `StatusWorking`
   - `133;D;0` &rarr; `StatusDone`
   - `133;D;<nonzero>` &rarr; `StatusFailed`
2. **OSC 9;4 (ConEmu / Windows Terminal Progress Protocol):**
   - `9;4;1` / `9;4;3` &rarr; `StatusWorking`
   - `9;4;0` &rarr; `StatusDone`
   - `9;4;2` &rarr; `StatusFailed`
   - `9;4;4` &rarr; `StatusNeedsInput`
3. **PTY Write Activity Fallback:**
   - Any write to the PTY sets `StatusWorking`.
   - When writes cease for `DefaultIdleTimeout` (3 seconds), status falls back to `StatusIdle`.
4. **Process Exit:**
   - On process exit in `--keep` panes: exit code 0 &rarr; `StatusDone`, non-zero &rarr; `StatusFailed`.

### The Problem in Practice
Most popular coding agents (**Claude Code**, **OpenCode**, **Codex**, **Aider**) do **not** emit OSC 133 or OSC 9;4 sequences natively.
- When an agent pauses to ask for tool permission, file edit approval, or clarifying answers (`"Permission required"`, `"Do you want to proceed? [y/n]"`, `"esc to cancel / enter to confirm"`), it simply stops writing bytes to the PTY while awaiting input.
- Wideboi's write heuristic observes 3 seconds of write silence and silently transitions the pane to `StatusIdle`.
- **Consequence:** `StatusNeedsInput` is almost never triggered for the primary coding agents running inside Wideboi. Desktop notifications for blocked agents never fire, and the status bar/dashboard fails to surface stalled agents.

### Herdr's Approach
Herdr (`herdr/src/detect/`) uses a multi-layered detection strategy:
1. **Foreground Process Inspection:**
   - Inspects the foreground process group of the pane's PTY to determine if an agent executable (`claude`, `opencode`, `codex`, `pi`, etc.) is running.
2. **Declarative Screen-Tail & Title Manifests:**
   - Manifests (`distribution/agent-detection/*.toml`) specify prioritized pattern-matching rules against:
     - Specific regions: `bottom_non_empty_lines(12)`, `after_last_horizontal_rule`, `whole_recent`.
     - Prompts: `"△ Permission required"`, `"esc to cancel"`, `"enter to confirm"`, `"Do you want to proceed?"` &rarr; classified as `Blocked` (`StatusNeedsInput`).
     - Spinners & interrupt hints: Braille characters (`\u2800-\u28FF`) or half-circle arcs (`\u25D0-\u25D3`) in OSC terminal titles, progress bars, or `"esc to interrupt"` &rarr; classified as `Working`.
3. **State Debouncing (`PendingIdleConfirmation`):**
   - Agents often pause for 100–500ms between tool execution flushes or model thinking chunks. Herdr holds `Working` for up to 700ms (or 3 check intervals) before falling back to `Idle`, unless an unambiguous idle prompt is visible. This completely prevents status flickering.
4. **Alternate-Screen / Transcript Awareness:**
   - If an agent opens an interactive transcript viewer (e.g. Claude Code's `Ctrl+O` transcript), Herdr sets `skip_state_update = true` so the viewer does not clobber the real underlying agent state.

---

## 3. State Modeling: The "Seen" vs. "Unseen" Completion Model

### Wideboi's Current State
In Wideboi, `StatusDone` is transient for interactive REPLs: once an agent turn completes and stdout goes quiet, the pane falls back to `StatusIdle` after 3 seconds.

### The UX Failure Mode
In a multi-pane environment (e.g. 5 columns across a wide monitor), an engineer kicks off tasks in 3 background panes and switches to another task or steps away.
- When they return, all 5 panes show `● idle`.
- There is no indication of which tasks completed while they were away, which were already idle, or which failed. The engineer is forced to manually inspect every column.

### Herdr's Distinction
Herdr cleanly separates **engine process state** (`Idle`, `Working`, `Blocked`, `Unknown`) from **human attention state** (`seen: bool`):
- When an unfocused pane transitions `Working` &rarr; `Idle`, it enters the `Done` status (`✓`).
- It remains marked as `Done` until the user focuses or views that pane.
- Once focused, `seen` is set to `true`, and the status transitions to `Idle` (`●`).
- *Effect:* The status overview behaves like an actionable **notification inbox** of completed work.

---

## 4. Status Dashboard & Fleet Overview UX

### Wideboi's Current Dashboard
Wideboi implements an in-memory VT table pane (`internal/server/dashboard.go`, `<prefix> s`) that displays active terminal panes (`PANE ID`, `STATUS`, `TITLE`, `CWD`), supporting keyboard (`j`/`k`/`Enter`) and mouse navigation.

Panes are rendered in numerical/creation order.

### Lessons from Herdr's Overview & Sidebar
1. **Urgency-Based Priority Sorting:**
   - Herdr orders agents by priority:
     $$\text{Blocked (Needs Input)} \;\longrightarrow\; \text{Done (Unseen)} \;\longrightarrow\; \text{Working} \;\longrightarrow\; \text{Idle} \;\longrightarrow\; \text{Unknown}$$
   - Any agent that needs user permission or has failed immediately floats to the top of the dashboard.
2. **Fleet Summary Banner:**
   - Herdr provides aggregate status counts. A header line in Wideboi's dashboard:
     ```text
     [ wideboi dashboard ]  1 needs input  •  2 working  •  1 done  •  3 idle
     ```
     allows the developer to evaluate their entire workspace at a glance.
3. **Git Worktree & Branch Context:**
   - Herdr discovers Git branch and ahead/behind commits (`main ↑1↓2`) from the pane's CWD.
   - For agent worktrees, displaying the branch name (e.g. `feat/auth`) in the status table is significantly more informative than a truncated filesystem path.

---

## 5. Headless Agent Coordination & APIs

### Wideboi's Current Headless Model
Wideboi exposes CLI subcommands via `wideboi-control` (`docs/skills/wideboi-control/SKILL.md`): `split`, `send`, `dump-pane` (`capture`), `pipe-pane`, `wait`, and `close`.

However, `wideboi wait` only waits for a process to *exit*. To coordinate long-running interactive agent REPLs, scripts and agents must poll using bash loops:
```bash
wait_for() {
  for _ in $(seq 100); do
    wideboi capture "$1" -n 5 | grep -qF -- "$2" && return 0
    sleep 0.1
  done
  return 1
}
```

### Herdr's First-Class Coordination Primitives
Herdr provides native primitives over its CLI/socket:
- `herdr pane wait-output <pane-id> --match <string> [--regex <pat>] [--timeout <dur>]`: Blocks directly on server terminal updates without client polling.
- `herdr agent wait <target> --until <status>`: Blocks until a target agent enters `blocked`, `done`, or `idle`.
- **Prompt Safety Guard:** Refuses to inject prompts into an agent that is currently blocked at a confirmation prompt, preventing input corruption.
- **Joined Soft-Wraps (`recent-unwrapped`):** Unwraps lines that were wrapped by fixed terminal column bounds, producing clean text for LLM consumption.

---

## 6. Action Items & Filed Issues

The findings from this analysis have been organized into the following GitHub issues:

1. **Issue #374:** Prioritize attention-needed panes and show fleet summary in status dashboard
2. **Issue #375:** Distinguish unseen completions from idle panes in status bar and dashboard
3. **Issue #376:** Detect agent permission prompts and blockers to set StatusNeedsInput
4. **Issue #377:** Add native wait-output and wait-status commands for headless agent coordination
5. **Issue #379:** Responsive compact layout for status dashboard in narrow columns / session drawer
6. **Issue #380:** Support pre-loading the status dashboard as a configured startup column

Together with **#373 (Pinned columns)**, issues #379 and #380 provide a complete path toward an always-visible, compact status drawer on the left side of Wideboi sessions.
