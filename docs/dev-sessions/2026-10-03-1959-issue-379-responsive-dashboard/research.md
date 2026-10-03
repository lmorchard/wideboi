# Research: Responsive Compact Layout for Status Dashboard in Narrow Columns (Issue #379)

## 1. Context & Motivation

The status dashboard (`internal/server/dashboard.go`) displays an overview of active panes.
Currently, it uses a fixed 4-column layout (`PANE ID`, `STATUS`, `TITLE`, `CWD`), consuming ~50+ characters before CWD is visible.
When a column is narrow (e.g. 20–35 columns):
- Row titles and CWD are cut off.
- Table headers and footer overflow and clip awkwardly.
- Issue #373 proposes **pinned columns** on the left. To make a pinned status dashboard effective as an always-visible **session drawer**, the dashboard must adapt responsively to narrow column widths without losing vital context (status glyph, pane ID, title).

## 2. Layout Breakpoints & Density Modes

1. **Wide mode (`cols >= 60`)**:
   - Full 4-column view: `PANE ID` (9), `STATUS` (12), `TITLE` (24+), `CWD`.
   - Full summary banner: `[ wideboi dashboard ]  1 needs input  •  2 working  •  1 done  •  3 idle`.
   - Full instructions footer.

2. **Compact mode (`38 <= cols < 60`)**:
   - 3-column view: `PANE ID` (9), `STATUS` (12), `TITLE` (fills remaining width). Omit `CWD`.
   - Compact banner: `[ wideboi ]  %s`.
   - Compact instructions footer.

3. **Drawer mode (`cols < 38`, e.g. 20–35 cols)**:
   - Ultra-compact single-line layout:
     `> [1]*   ▲   make test`
     - Status glyph: `!` (needs input), `▲` (working), `✔` (done), `✖` (failed), `●` (idle).
     - Pane ID with focus marker: `[1]*`.
     - Title fills remaining width.
   - Drawer banner with compact status badges:
     `[wb] 1! 2▲ 1✔ 3●`.
   - Compact column headers: `  PANE   ST  TITLE`.
   - Concise footer: `  [j/k] Move  [Enter] Jump`.

## 3. Preservation of Invariants

- Vertical viewport clipping (`maxVisible := rows - 4`) and scroll offset handling remain intact.
- Mouse click row mapping (`idx := d.scrollOffset + (ev.Y - 2)`) remains identical because there are always 2 header rows (banner + column header).
- Sorting by urgency priority and ID-based selection tracking are unaffected.
