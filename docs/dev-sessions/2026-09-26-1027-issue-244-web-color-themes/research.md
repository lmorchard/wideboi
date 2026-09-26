# Codebase Research: Web Client Color Theming

## Findings

1. **Terminal Colors Decoding & Palette** (`web/src/colors.ts:4-36`):
   - Currently defines `ANSI_COLORS` array: 16 basic colors, 216 RGB cube colors, and 24 grayscale shades.
   - `decodeColor(color: ColorData | undefined, isBg: boolean): string` returns `#1e1e1e` for default background and `#d4d4d4` for default foreground.
   - For `ColorKind.BASIC` or `INDEXED`, it returns `ANSI_COLORS[color.index]`.

2. **Canvas Painting & Background Optimization** (`web/src/pane-painter.ts:132-212`):
   - Line 132: `ctx.clearRect(0, 0, logicalWidth, logicalHeight);`
   - Lines 147-156:
     ```ts
     const normalBg = decodeColor(cell.style?.bg, true);
     const normalFg = decodeColor(cell.style?.fg, false);
     const bg = reverse ? normalFg : normalBg;
     const fg = reverse ? normalBg : normalFg;
     if (bg !== '#1e1e1e') {
       ctx.fillStyle = bg;
       ctx.fillRect(px, py, this.cellWidth * (cell.width || 1), termSettings.cellHeight);
     }
     ```
     `bg !== '#1e1e1e'` avoids filling rect when the background matches the pane background.
   - Lines 187-194:
     Cursor fill is hardcoded to `#d4d4d4` with inverted character in `#1e1e1e`.
   - Lines 177:
     Selection fill is hardcoded to `'rgba(100, 160, 220, 0.45)'`.
   - Line 199:
     Scrollback banner footer uses `#333333` and `#ffffff`.

3. **Pane Styling** (`web/src/wideboi-pane.ts:10-84`):
   - Uses hardcoded styles:
     - `background: #1e1e1e;`
     - `border-right: var(--divider-width) solid #555;`
     - Focused border: `#007fd4` and box-shadow `inset 0 2px #007fd4;`
     - Card mode border: `#b8b8b8`, focused `#0e9aff`.
     - Card label: `color: #ddd; background: #303030; border-right: 1px solid #b8b8b8;`

4. **Web App Layout & Controls** (`web/src/wideboi-app.ts:23-740` & `2026-2053`):
   - `:host` has `background: #1e1e1e;`
   - `.terminal-shell` has `color: #ccc;`
   - `.toolbar` has `background: #252526; border-top: 1px solid #3c3c3c;`
   - Buttons, selects, and inputs use `#3c3c3c`, `#1e1e1e`, `#555`, `#eee`, etc.
   - Toolbar rendered at `2026-2053` with selects for focus pane, layout, pane width, prefix key, and action buttons.
   - Mobile bar at `2054-2072` and mobile dock at `2073-2200`.

5. **Settings Persistence Pattern** (`web/src/wideboi-app.ts:759, 1861` and `web/src/pane-state.ts:8-23`):
   - `localStorage.getItem('wideboi.prefix') || 'ctrl+b'`
   - `localStorage.setItem('wideboi.prefix', val)` with `try / catch` guard.
