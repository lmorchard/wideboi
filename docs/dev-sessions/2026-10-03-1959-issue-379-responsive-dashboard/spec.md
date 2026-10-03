# Spec: Responsive Compact Layout for Status Dashboard in Narrow Columns (Issue #379)

**Goal:** Make `Dashboard.Render` adapt responsively across three column-width modes (Wide, Compact, Drawer) so the dashboard displays clear, legible status information without clipping or horizontal overflow at narrow widths (20–35 cols).

**Source:** https://github.com/lmorchard/wideboi/issues/379

---

## Current State

- `Dashboard.Render` always outputs a fixed 4-column layout (`PANE ID`, `STATUS`, `TITLE`, `CWD`).
- When `cols < 50`, `TITLE` and `CWD` are truncated. At `cols = 28`, only `PANE ID` and `STATUS` are visible (`> [1]*     ▲ working   `), leaving no room for titles.
- The summary banner and instruction footer are clipped at narrow widths.

---

## Desired End State

1. **Wide Mode (`cols >= 60`):**
   - Header: `  %-9s %-12s %-24s %s`, `"PANE ID"`, `"STATUS"`, `"TITLE"`, `"CWD"`.
   - Banner: `  [ wideboi dashboard ]  %s` with full words (`1 needs input  •  2 working  •  1 done  •  3 idle`).
   - Rows: `%s%-9s %-12s %-24s %s` (`marker`, `idStr`, `statusStr`, `title`, `cwd`).
   - Footer: `  [j/k/↑/↓] Select   [Enter/Click] Jump   [C-b x] Close`.

2. **Compact Mode (`38 <= cols < 60`):**
   - Drops `CWD` column.
   - Header: `  %-9s %-12s %s`, `"PANE ID"`, `"STATUS"`, `"TITLE"`.
   - Banner: `  [ wideboi ]  %s`.
   - Rows: `%s%-9s %-12s %s` (`marker`, `idStr`, `statusStr`, `title`).
   - Footer: `  [j/k] Select  [Enter] Jump  [C-b x] Close`.

3. **Drawer Mode (`cols < 38`):**
   - Streamlined for narrow pinned sidebar columns (20–35 cols).
   - Header: `  %-6s %-3s %s`, `"PANE"`, `"ST"`, `"TITLE"`.
   - Banner: `  [wb] %s` with compact glyph badges (`1! 2▲ 1✔ 3●`).
   - Rows: `%s%-6s %-3s %s` (`marker`, `idStr`, `glyph`, `title`), e.g.:
     `> [1]*   ▲   make test`
   - Footer:
     `  [j/k] Move  [Enter] Jump` (or `  [j/k] [Enter]` if `cols < 26`).

4. **Consistency:**
   - 2-line header structure (banner + column header) is preserved across all modes, ensuring mouse click mapping (`ev.Y - 2`) remains stable and uniform.
   - All lines pass through rune-aware `padOrTruncate` before styling escape codes are applied.

---

## Verification Plan

- Unit tests in `internal/server/dashboard_test.go`:
  - `TestDashboardWideMode`: Verifies 4 columns with CWD at `cols = 80`.
  - `TestDashboardCompactMode`: Verifies 3 columns without CWD at `cols = 50`.
  - `TestDashboardDrawerMode`: Verifies glyphs, compact banner, and title visibility at `cols = 28`.
  - `TestDashboardUltraNarrowMode`: Verifies clean rendering and no panic at `cols = 16`.
- `make quick` and all tests pass cleanly.
