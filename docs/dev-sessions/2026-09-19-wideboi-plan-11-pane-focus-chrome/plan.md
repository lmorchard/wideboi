# Plan 11 Implementation Plan — Focused Pane Visual Indicator & Header Chrome

**Goal:** Implement pane header bars, active column dividers, and status bar focus badges.

## Tasks

### Task 1: Update layout available height & placement Y offsets
- Update `AvailHeight` in `internal/layout/layout.go` to account for 1-row header + 1-row status bar.
- Update `ComputePlacements` and `CardStrategy` `Dst.Min.Y` offsets to start at `Y = 1`.
- Update layout unit/property tests.

### Task 2: Client header bars, active dividers, and status badge
- In `internal/client/client.go`, add pane header drawing (inverted for focused pane, plain for unfocused).
- Update column divider drawing: bold `┃` adjacent to focused pane, `│` for others.
- Update `normalStatusLocked` to include `focus: [pane N ★]`.

### Task 3: Verification & Golden Snapshot
- Update smoke tests (`scripts/smoke.py`).
- Regenerate golden snapshot (`make golden`).
- Pass full `make check`.

### Task 4: PR Creation
- Commit, push `plan-11-pane-focus-chrome`, and open PR.
