# Session Notes: wideboi Plan 6 (Modal Input)

**Date:** 2026-09-19
**Branch:** `plan-6-modal-input`
**Pull Request:** https://github.com/lmorchard/wideboi/pull/5

## What was accomplished

Plan 6 implemented modal input and prefix handling across 5 tasks:

1. **Task 1 (`feat(compose): add a styled write path`):** Added `compose.WriteStyled(s, x, y, text, style)` so cells can carry styling (like `uv.AttrReverse`), and delegated `WriteString` to it.
2. **Task 2 (`feat(client): give the status bar a control mode`):** Added `controlMode` and `prefixLabel` to `Client`. In control mode, the status bar inverts (SGR 7), displays the 71-cell control verb menu, and hides the host cursor (DECTCEM).
3. **Task 3 (`feat(cmd): replace the alt bindings with a configurable prefix`):** Extracted key routing into `cmd/wideboi/router.go`. Reads `WIDEBOI_PREFIX` (defaulting to `ctrl+b`). Replaced all `alt+*` bindings. Doubled prefix sends literal `0x02` to the pane and exits control mode; `Escape` exits; unknown keys are swallowed. Removed `pgup` global scroll claim (and confirmed `pgdn` was never matched by name in v1).
4. **Task 4 (`test(smoke): assert the prefix, the mode, and the keys we gave back`):** Updated `scripts/smoke.py` and `scripts/ptylib.py` to test the prefix, sticky mode, inverted bar, hidden cursor, doubled prefix, reclaimed control keys (`ctrl+q`, `ctrl+o`), and custom prefix environment variable.
5. **Task 5 (`docs: document the prefix, and why alt was the wrong layer`):** Removed Option-as-Meta workaround section from `README.md`, documented sticky control mode and `WIDEBOI_PREFIX`, recorded the lesson in `docs/LESSONS.md`, and updated `testdata/golden/startup.txt`.

## Status & Verification

- `make check` passed cleanly (`fmt-check`, `lint`, `seam-check`, `test`, `race`, `verify-exit`, `smoke`, `golden`).
- PR opened: https://github.com/lmorchard/wideboi/pull/5
