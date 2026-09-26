# Web Client: Dedicated Settings Pane and Expanded Nerd Fonts

## Goal
1. Move web client settings (font family, font size, etc.) out of the help overlay into a dedicated Settings modal / pane.
2. Provide an extensible, clean UI for web client settings, accessible via the toolbar and keyboard shortcuts.
3. Add a curated collection of popular programming Nerd Fonts downloaded from Nerd Fonts releases (https://www.nerdfonts.com/font-downloads) so users have a rich selection of terminal typefaces with complete icon/glyph support.

## Current State
- Commit `d3d304ebd27eea4303b79ed2a06edc0d0cd070c7` added initial support for two Nerd Fonts (`JetBrainsMono` and `FiraCode`).
- The font settings controls (font family select, font size input) were placed directly inside the Help overlay (`showHelp`), cluttering the keybinding cheat sheet.
- There is no dedicated Settings button or modal dialog in the web UI.
- Only two fonts are available beyond system monospace.

## Desired End State
1. **Dedicated Settings Pane / Modal**:
   - A dedicated Settings overlay modal (`showSettings`), separate from Help.
   - Settings button (e.g. gear icon `⚙`) in the toolbar and mobile menu to open Settings.
   - Keyboard shortcut to toggle Settings (e.g. prefix + `,` or similar standard convention, plus Esc to close).
   - Clean, well-styled settings UI displaying:
     - Font family dropdown (with preview/labels).
     - Font size control (number input / buttons).
     - Clean grouping ready for future settings (e.g. themes, layout options).
     - Responsive layout matching wideboi's dark UI aesthetic.
2. **Expanded Nerd Fonts Collection**:
   - Include a wide, curated selection of popular monospaced Nerd Fonts from https://www.nerdfonts.com/font-downloads:
     - JetBrains Mono Nerd Font
     - Fira Code Nerd Font
     - Cascadia Code (CaskaydiaCove) Nerd Font
     - Hack Nerd Font
     - Meslo LG Nerd Font
     - Source Code Pro (SauceCodePro) Nerd Font
     - IBM Plex Mono (BlexMono) Nerd Font
     - Inconsolata Nerd Font
     - Roboto Mono Nerd Font
     - DejaVu Sans Mono Nerd Font
     - Victor Mono Nerd Font
   - Properly configure `@font-face` rules in `web/index.html` or dedicated CSS.
   - Support smooth font loading and canvas re-measurement upon selection.
3. **Persistence & Terminal Resizing**:
   - Persist settings in `localStorage`.
   - Update `termSettings`, trigger font loading, recalculate `cellWidth` and `cellHeight`, and recalculate grid / send resize to server when appropriate.
4. **Verification & Tests**:
   - Web unit and component tests for settings modal opening/closing and font changes.
   - Ensure `make check` and build pass.
