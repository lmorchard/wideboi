# Spec: Consolidate redundant bottom bar controls into settings pane

## Background & Problem
When the settings modal was introduced in #245 / #249 and theme customization was added in #244 / #248, configuration settings controls (`Color Theme`, `Prefix Key`, and `Layout Mode`) were added to the Settings dialog while remaining in the bottom `.toolbar`.
These controls are redundant:
- `Layout Mode`: `#layout-mode` in toolbar, `#settings-layout-mode` in settings dialog
- `Prefix Key`: `#prefix-key` in toolbar, `#settings-prefix-key` in settings dialog
- `Color Theme`: `#theme-select` in toolbar, `#settings-theme` in settings dialog

Having these redundant controls in the bottom toolbar clutters the UI and wastes horizontal space, especially when the dedicated Settings modal already provides full control over them.

## Requirements
1. Remove `#layout-mode`, `#prefix-key`, and `#theme-select` dropdowns and their labels from the bottom `.toolbar` in `wideboi-app.ts`.
2. Retain operational toolbar controls:
   - `Focus Pane:` select
   - `Pane width:` input and `Follow PTY` checkbox
   - `Fit to Window` button
   - `Search` button
   - `⚙ Settings` button
   - `Help (?)` button
   - Tip text
3. Ensure the Settings modal retains full functionality for Color Theme, Prefix Key, and Layout Mode (as well as Font Family and Size).
4. Update `web/tests/settings.spec.js` to verify that `#theme-select`, `#layout-mode`, and `#prefix-key` are removed from the toolbar, and that theme selection works through the Settings dialog.
5. Verify that all tests pass (`npm test`, `npm run test:browser`, `make check`).
