# Implementation Plan: Consolidate redundant bottom bar controls into settings pane

## Phase 1: Code Modifications in `web/src/wideboi-app.ts`
- Remove the `Layout`, `Prefix`, and `Theme` label and select elements from `.toolbar`.
- Retain `Focus Pane:`, `Pane width:`, `Follow PTY`, `Fit to Window`, `Search`, `⚙ Settings`, `Help (?)`, and the tip.

## Phase 2: Update Browser & Unit Tests
- In `web/tests/settings.spec.js`:
  - Assert that `#theme-select`, `#layout-mode`, and `#prefix-key` do not exist in the toolbar (`expect(page.locator('#theme-select')).toHaveCount(0)`, etc.).
  - Test selecting themes directly via the settings modal.
  - Verify that theme styling (`--wb-bg-app`) applies correctly.

## Phase 3: Verification
- Run `npm test` in `web/`.
- Run `npm run test:browser` in `web/`.
- Run `npm run build` and `make check`.

## Phase 4: PR & Review
- Commit changes with descriptive commit message referencing issue #254.
- Push branch and open Pull Request.
