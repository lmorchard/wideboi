# wideboi Plan 4 — Input Routing, $mod Keybindings, Agent Status, Scrollback, and Full Verbs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete the remaining v1 multiplexer features: `$mod` keybindings, OSC 133 agent status tracking with glyphs (`»`, `!`, `✓`, `✗`), `SmartJump` verb, scrollback navigation, and comprehensive wire acceptance tests.

**Architecture:**
1. **`$mod` Keybindings**: Map `alt` / `Option` keys (`alt+h`, `alt+l`, `alt+n`, `alt+w`, `alt+x`, `alt+j`, `alt+q`) to protocol verbs, bypassing child PTY input.
2. **OSC 133 & Status Glyphs (`internal/server/term`, `internal/protocol`)**: Register OSC 133 callbacks on `term.Grid` to track prompt and command lifecycle (`StatusWorking` `»`, `StatusNeedsInput` `!`, `StatusDone` `✓`, `StatusFailed` `✗`), plus idle output heuristics. Update status line and `compose.WriteString` for wide/multibyte glyph rendering.
3. **Smart Jump Verb (`VerbSmartJump`)**: Jump focus directly to panes needing attention (`!` or `✗`).
4. **Scrollback Navigation**: Support `PageUp` / `PageDown` or `$mod+u` / `$mod+d` to scroll focused pane through 10k-line emulator history.
5. **Acceptance Checks & Golden Snapshot**: Expand `scripts/smoke.py` and `make golden`.

---

### Task 1: `$mod` Keybinding Matrix (`cmd/wideboi/main.go`, `internal/protocol`)

- [ ] **Step 1: Expand `protocol.VerbType` and key matching**
  Add `VerbSmartJump` and `VerbKillPane` to `protocol.VerbType`.
  Map `$mod` (`alt`/`Option`) combinations in `cmd/wideboi/main.go`:
  - `alt+h` / `alt+left`: `VerbFocusLeft`
  - `alt+l` / `alt+right` / `ctrl+o`: `VerbFocusRight`
  - `alt+n`: `VerbNewColumn`
  - `alt+w`: `VerbCycleWidth`
  - `alt+x`: `VerbKillPane`
  - `alt+j`: `VerbSmartJump`
  - `alt+q` / `ctrl+q`: Quit multiplexer

- [ ] **Step 2: Commit Task 1**

---

### Task 2: OSC 133 Agent Status Tracking & Status Glyphs (`internal/server/term`, `internal/server`, `internal/client`)

- [ ] **Step 1: Add OSC 133 handler registration to `term.Grid`**
  Track prompt/command lifecycle (`OSC 133;A`, `OSC 133;B`, `OSC 133;C`, `OSC 133;D;<code>`).
  Add `Status() PaneStatus` to `term.Grid` and `server.Pane`.

- [ ] **Step 2: Update `internal/protocol` and `internal/server`**
  Broadcast `PaneStatus` per pane in `MsgLayoutSnapshot`.

- [ ] **Step 3: Update `internal/client/compose` and status bar rendering**
  Ensure `compose.WriteString` correctly handles UTF-8 multibyte glyphs (`»`, `!`, `✓`, `✗`).
  Render status glyphs beside pane IDs on the status bar.

- [ ] **Step 4: Commit Task 2**

---

### Task 3: Smart Jump Verb (`VerbSmartJump`) & Scrollback Navigation

- [ ] **Step 1: Implement `VerbSmartJump` in `internal/layout` and `internal/server`**
  Jumps focus to the first pane with `StatusNeedsInput` (`!`) or `StatusFailed` (`✗`).

- [ ] **Step 2: Implement scrollback offset navigation**
  Add scroll offset tracking per pane (`ScrollOffset int`).
  Support `PageUp`/`PageDown` and `$mod+u`/`$mod+d` to adjust scroll offset.
  Render scrolled rows using `Grid.ScrollbackCellAt`.

- [ ] **Step 3: Commit Task 3**

---

### Task 4: Acceptance Checks & Golden Wire Snapshot (`scripts/smoke.py`, `scripts/golden.py`, `Makefile`)

- [ ] **Step 1: Expand `scripts/smoke.py` acceptance cases**
  Add wire-level checks for `$mod` keybindings (`alt+n`, `alt+h`, `alt+l`, `alt+w`, `alt+q`), OSC 133 status emission, and `SmartJump`.

- [ ] **Step 2: Regenerate golden wire snapshot (`make golden`)**

- [ ] **Step 3: Run full gate `make check`**

- [ ] **Step 4: Commit Task 4**
