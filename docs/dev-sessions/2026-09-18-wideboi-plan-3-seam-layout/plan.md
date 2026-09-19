# wideboi Plan 3 — Client/Server Seam and Layout Core Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Cut the client/server transport seam, implement the pure `internal/layout` core for scrolling columns, and wire the multiplexer state behind a transport boundary.

**Architecture:**
1. **The Seam (`internal/protocol` & `internal/transport`)**: Protocol structs carrying codec-neutral messages (`Attach`, `Verb`, `Input`, `Resize`, `LayoutSnapshot`, `PaneLines`, `PaneClosed`). `InProc` transport channel adapter connecting client and server.
2. **Server-side Pane & Lifecycle (`internal/server`)**: PTY spawning, VT grid emulation, and process-tree teardown moved entirely to the server side. Closes the parked descendant-snapshot leak when a pane root exits early.
3. **Layout Core (`internal/layout`)**: Pure, stdlib-only calculation of scrolling columns, strip geometries, preset pane widths, and `[]Placement` output (src crop rect, dst screen rect, Z-depth).
4. **Wire Acceptance & Golden Updates**: Expand `scripts/smoke.py` to cover focus movement, new column creation, and scrolling.

**Tech Stack:** Go 1.27.1 · `charmbracelet/x/vt` (pinned) · `charmbracelet/ultraviolet` · Python 3 stdlib for wire checks.

**Specs:**
- `docs/dev-sessions/2026-09-18-wideboi-plan-2-layout-seam/spec.md`
- `docs/dev-sessions/2026-09-18-wideboi-v1-foundations/spec.md`

---

### Task 1: Protocol & InProc Transport Seam (`internal/protocol` & `internal/transport`)

Dismantle the temporary Plan 1 `internal/client/pane.go` struct that held PTY, VT emulator, and surface in one place.

**Files:**
- Create: `internal/protocol/messages.go`
- Create: `internal/transport/inproc.go`
- Create: `internal/server/pane.go`
- Modify/Delete: `internal/client/pane.go`
- Modify: `scripts/seam-check.sh`

- [ ] **Step 1: Create `internal/protocol/messages.go`**
Declare codec-neutral protocol message types:
  - `VerbType` (`FocusLeft`, `FocusRight`, `NewColumn`, `CycleWidth`, `KillPane`)
  - `MsgAttach{Cols, Rows int}`
  - `MsgVerb{Verb VerbType}`
  - `MsgInput{PaneID int, Data []byte}`
  - `MsgResize{Cols, Rows int}`
  - `MsgLayoutSnapshot{Placements []PlacementData, FocusPaneID int}`
  - `MsgPaneLines{PaneID int, Lines []LineData}`
  - `MsgPaneClosed{PaneID int, ExitCode int}`

- [ ] **Step 2: Create `internal/transport/inproc.go`**
In-process channel transport implementing Client/Server messaging.

- [ ] **Step 3: Move PTY + Grid state to `internal/server/pane.go`**
Server owns PTY spawning (`ptyx`), VT grid emulation (`term.Grid`), and lifecycle. Client owns off-screen drawing surface (`compose.Surface`).

- [ ] **Step 4: Update `scripts/seam-check.sh`**
Retire the client->server `ptyx` and `term` import allowlist entries.

- [ ] **Step 5: Commit Task 1**

---

### Task 2: Pure Layout Core (`internal/layout`)

**Files:**
- Create: `internal/layout/layout.go`
- Create: `internal/layout/layout_test.go`

- [ ] **Step 1: Write failing layout unit tests in `internal/layout/layout_test.go`**
Test `Strip`, `Column`, `Placement`, viewport cropping, focus navigation, preset width cycling, and new column creation.

- [ ] **Step 2: Implement `internal/layout/layout.go`**
Pure, stdlib-only calculation of logical strip coordinates, visible screen placements (`image.Rectangle` for Src and Dst), and column scrolling.

- [ ] **Step 3: Run `go test ./internal/layout/...` and verify green**

- [ ] **Step 4: Commit Task 2**

---

### Task 3: Server State & Pane Lifecycle (`internal/server`)

**Files:**
- Create/Modify: `internal/server/server.go`, `internal/server/server_test.go`

- [ ] **Step 1: Implement `Server` struct in `internal/server`**
Owns `layout.Strip`, map of server-side `Pane`s, PTY reader pumps, and process tree teardown.
Maintain a descendant process snapshot per pane while active, closing the parked leak when a pane shell exits before `Server.Close`.

- [ ] **Step 2: Write unit tests for `Server` and pane teardown**

- [ ] **Step 3: Commit Task 3**

---

### Task 4: Multiplexer & Main Loop Refactoring (`cmd/wideboi/main.go`, `internal/client`)

**Files:**
- Modify: `internal/client/client.go`
- Modify: `cmd/wideboi/main.go`

- [ ] **Step 1: Implement `Client` struct in `internal/client`**
Consumes `MsgLayoutSnapshot` and `MsgPaneLines`, maintains local surface mirrors, composites via `compose.Blit`, and handles host terminal input.

- [ ] **Step 2: Refactor `cmd/wideboi/main.go`**
Wire `Server` and `Client` over `InProc` transport.
Handle keybindings for focus switching (`ctrl+o`), new column creation, width cycling, and quit (`ctrl+q`).
Wire host-window resize (`uv.WindowSizeEvent`) to layout update.

- [ ] **Step 3: Run `make check` and verify binary operates correctly**

- [ ] **Step 4: Commit Task 4**

---

### Task 5: Acceptance Checks & Golden Snapshot (`scripts/smoke.py`, `scripts/golden.py`, `Makefile`)

**Files:**
- Modify: `scripts/smoke.py`
- Modify: `scripts/golden.py`
- Modify: `testdata/golden/startup.txt`

- [ ] **Step 1: Expand `scripts/smoke.py` with acceptance cases**
Add cases for column creation, scrolling past viewport edges, and window resizes asserted on the wire.

- [ ] **Step 2: Update golden wire snapshot with `make golden`**

- [ ] **Step 3: Run full gate `make check`**
Verify `fmt-check lint seam-check test verify-exit smoke` all pass green with no untracked files.

- [ ] **Step 4: Commit Task 5**
