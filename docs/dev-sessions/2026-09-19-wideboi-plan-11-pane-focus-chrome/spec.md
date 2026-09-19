# wideboi Plan 11 — Focused Pane Visual Indicator & Header Chrome

**Date:** 2026-09-19
**Status:** In Progress

## Goal

Provide prominent, unmistakable visual feedback for the focused terminal pane across three integrated cues:
1. **Pane Header Bar:** A 1-row header above each column showing pane ID and status glyph. The focused pane header is inverted (`uv.AttrReverse`), while unfocused headers stay unstyled/dim.
2. **Active Divider Styling:** The vertical column divider bounding the focused pane uses bold `┃` dividers (or styled dividers), while unfocused dividers use standard `│`.
3. **Status Bar Focus Badge:** The bottom status bar highlights the focused pane (`focus: [pane N ★]`).

## Architecture & Design

### 1. Layout Adjustment (`internal/layout/layout.go`)
- Header row consumes 1 row at `Y = 0`.
- `AvailHeight(viewportHeight)` returns `max(viewportHeight - 2, 1)` (1 row header + 1 row bottom status bar).
- `ComputePlacements` offsets placement `Dst.Min.Y` to `1` so PTY content begins below the header bar.

### 2. Client Rendering (`internal/client/client.go`)
- `drawPaneHeaders`: Draws 1-row header at `Y = 0` above each column (`0..Dst.Max.X`).
  - Focused pane: `WriteStyled` with `uv.Style{Attrs: uv.AttrReverse}`, text e.g. ` 1: /bin/sh [✓] `.
  - Unfocused pane: `WriteString` unstyled text e.g. ` 2: /bin/sh `.
- `drawDividers`: Bounding divider for focused pane uses `┃`; other column dividers use `│`.
- Status bar normal hint format includes `focus: [pane N ★]`.

### 3. Testing & Golden Snapshot
- Unit tests in `internal/client/client_test.go` and `internal/layout/layout_test.go`.
- `make check`, `make smoke`, `make golden`.
