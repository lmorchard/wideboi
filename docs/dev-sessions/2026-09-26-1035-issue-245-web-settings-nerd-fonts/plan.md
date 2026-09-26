# Plan: Settings Pane & Expanded Nerd Fonts

## Phase 1: Font Assets & Font Configuration
Establish the expanded font assets and configuration foundation.

### Files
- `web/public/fonts/*.ttf`, `web/public/fonts/*.otf` — add 10 popular Nerd Fonts (Cascadia Code, Hack, IBM Plex Mono, Meslo LGS, Source Code Pro, Inconsolata, Roboto Mono, DejaVu Sans Mono, Victor Mono, Geist Mono).
- `web/index.html` — define `@font-face` rules for all bundled Nerd Fonts.
- `web/src/fonts.ts` — new file defining `AVAILABLE_FONTS` catalog and font option types.
- `web/src/fonts.test.ts` — tests validating font catalog definitions, font IDs, and fallback.

### Key Changes
```ts
export interface FontOption {
  id: string;
  name: string;
  family: string;
}

export const AVAILABLE_FONTS: FontOption[] = [
  { id: 'monospace', name: 'System Monospace', family: 'monospace' },
  { id: 'JetBrainsMono Nerd Font Mono', name: 'JetBrains Mono Nerd Font', family: 'JetBrainsMono Nerd Font Mono' },
  { id: 'FiraCode Nerd Font Mono', name: 'Fira Code Nerd Font', family: 'FiraCode Nerd Font Mono' },
  { id: 'CaskaydiaCove Nerd Font Mono', name: 'Cascadia Code Nerd Font', family: 'CaskaydiaCove Nerd Font Mono' },
  { id: 'Hack Nerd Font Mono', name: 'Hack Nerd Font', family: 'Hack Nerd Font Mono' },
  { id: 'MesloLGS Nerd Font Mono', name: 'Meslo LGS Nerd Font', family: 'MesloLGS Nerd Font Mono' },
  { id: 'BlexMono Nerd Font Mono', name: 'IBM Plex Mono Nerd Font', family: 'BlexMono Nerd Font Mono' },
  { id: 'SauceCodePro Nerd Font Mono', name: 'Source Code Pro Nerd Font', family: 'SauceCodePro Nerd Font Mono' },
  { id: 'Inconsolata Nerd Font Mono', name: 'Inconsolata Nerd Font', family: 'Inconsolata Nerd Font Mono' },
  { id: 'RobotoMono Nerd Font Mono', name: 'Roboto Mono Nerd Font', family: 'RobotoMono Nerd Font Mono' },
  { id: 'DejaVuSansM Nerd Font Mono', name: 'DejaVu Sans Mono Nerd Font', family: 'DejaVuSansM Nerd Font Mono' },
  { id: 'VictorMono Nerd Font Mono', name: 'Victor Mono Nerd Font', family: 'VictorMono Nerd Font Mono' },
  { id: 'GeistMono Nerd Font Mono', name: 'Geist Mono Nerd Font', family: 'GeistMono Nerd Font Mono' },
];
```

### Verification — automated
- [x] `npm --prefix web test src/fonts.test.ts` passes — **13 passed, 80 tests passed**.
- [x] `npm --prefix web run build` succeeds and includes fonts in `dist/fonts/` — **built in 249ms, 12 fonts in dist/fonts/**.

---

## Phase 2: KeyRouter Shortcut for Settings
Add key action and shortcut routing for opening/toggling the settings overlay.

### Files
- `web/src/key-router.ts` — add `toggle_settings` action to `KeyRouterAction`, handle `,` (comma) in prefix mode.
- `web/src/key-router.test.ts` — unit test verifying prefix followed by `,` produces `{ type: 'toggle_settings' }`.

### Key Changes
```ts
export type KeyRouterAction =
  | ...
  | { type: 'toggle_settings' };

// In KeyRouter.handle():
if (key === ',') {
  this.prefixActive = false;
  return { type: 'toggle_settings' };
}
```

### Verification — automated
- [x] `npm --prefix web test src/key-router.test.ts` passes — **10 passed, 80 tests passed**.

---

## Phase 3: Dedicated Settings Modal & UI Integration
Move font settings out of Help into a dedicated Settings modal, add toolbar buttons, and provide preview.

### Files
- `web/src/wideboi-app.ts`:
  - Remove font settings block from `.help-dialog`.
  - Add `prefix + ,` row to the Help dialog table.
  - Add `@state() private showSettings = false;`
  - Implement `openSettings()`, `closeSettings()`, `toggleSettings()`.
  - Wire `action.type === 'toggle_settings'` in key routing and handle `Escape` when settings is open.
  - Add Settings button (`⚙ Settings`) to the toolbar and mobile bar.
  - Implement `renderSettingsModal()` with font family picker, font size input, live preview card with glyphs, prefix key dropdown, and layout mode selector.
  - Add CSS styles for `.settings-overlay`, `.settings-dialog`, `.settings-preview`, etc.
- `web/src/settings.test.ts` — component tests for settings state management and font persistence.

### Verification — automated
- [x] `npm --prefix web test` passes all tests — **14 passed, 84 tests passed**.
- [x] `npm --prefix web run lint` (tsc) passes with zero errors — **tsc --noEmit passed cleanly**.

---

## Phase 4: Full Suite Verification & Build
Verify the entire codebase including Go backend and embedded assets.

### Files
- All changed files

### Verification — automated
- [x] `make quick` passes (web build + Go tests + web tests) — **passed clean**.
- [x] `make check` passes — **all targets (fmt, lint, test, web-test, web-accept 29/29, race, smoke 39/39, attach-check 26/26, golden, verify-exit) passed clean**.
