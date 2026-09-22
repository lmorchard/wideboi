# Width Control & Configurable Presets Spec

**Goal:** Provide keyboard-driven incremental width adjustment (grow/shrink) for the focused card and support user-configurable width presets for cycle-width.

**Source:** GitHub Issue #45, user discussion on 2026-09-22

## Current state

- **Pane spawn geometry:** A pane spawns at `max((cols-1)/2, 40)` cells wide (`internal/server/server.go:278`).
- **CycleWidth:** `s.strip.CycleWidth()` (`internal/layout/layout.go:115-128`) hardcodes transitions through fixed widths:
  - If width <= 40 -> 60
  - If width <= 60 -> 80
  - Otherwise -> 40
- On a 200-column host terminal, a spawned pane starts at 99 cells wide. Tapping `w` (`VerbCycleWidth`) drops it to 40, and subsequent taps only reach 60 and 80—never returning to 99 or anything wider (Issue #45).
- **Keybindings:** Control mode bindings (`internal/keys/keys.go:116-153`) map single keys to actions. Currently `w` maps to `VerbCycleWidth`.
- **Status bar budget:** The control mode status bar budget at 80 columns is tight (77 cells used out of 79 budget for the 9 default entries when attached; `internal/client/help_test.go:37-51`).

## Desired end state

1. **Incremental Grow & Shrink:**
   - Add new verbs `protocol.VerbGrowWidth` and `protocol.VerbShrinkWidth`.
   - In `internal/layout/layout.go`, add methods on `*Strip`:
     - `GrowWidth(delta int)` (delta default 10) increases the focused column width.
     - `ShrinkWidth(delta int)` (delta default 10) decreases the focused column width, clamped to a floor of `MinColumnWidth` (20 cells).
   - In `internal/keys/keys.go`:
     - Bind `p` (`ActionNameGrowWidth`, verb `protocol.VerbGrowWidth`, with automatic `ctrl+p` repeat form).
     - Bind `o` (`ActionNameShrinkWidth`, verb `protocol.VerbShrinkWidth`, with automatic `ctrl+o` repeat form).
     - Help overlay descriptions: `"o": "shrink this column's width"`, `"p": "grow this column's width"`.
     - Both support configurable key overrides via TOML `[keys]`.
2. **Configurable Width Presets:**
   - In `internal/config/config.go`, support `width_presets` in TOML config (e.g. `width_presets = [40, 60, 80, 100]` or similar).
   - Default presets: `[40, 60, 80]`, but dynamically include or allow custom preset list.
   - `Strip.SetWidthPresets([]int)` sets the presets on the layout strip.
   - `CycleWidth()` steps to the next higher preset in the configured slice, or wraps to the first preset if at/above the maximum. If the current width is not an exact preset (e.g. spawned at 99), it transitions to the next higher preset (or wraps if > max preset).

## Design decisions

- **Decision:** Use `o` (shrink) and `p` (grow) as default keys for incremental width adjustments by 10 columns.
  - **Why:** Letters `o` and `p` are side-by-side, mnemonic (shrink / grow), and critically support standard ASCII control bytes (`ctrl+o` / `ctrl+p`), enabling smooth repeat chords (`Ctrl-B Ctrl-O Ctrl-O ...` / `Ctrl-B Ctrl-P Ctrl-P ...`) without exiting control mode.
  - **Rejected:** `,` and `.` (terminals in standard ASCII mode do not send distinct control bytes for ctrl-punctuation, preventing repeat chords).
- **Decision:** Add explicit `VerbGrowWidth` and `VerbShrinkWidth` to protocol.
  - **Why:** Matches the existing architecture where client sends discrete verb messages and server mutates layout state and calls `resizePanesLocked()`.
- **Decision:** Keep width presets as a list of integers configured via TOML and passed to `layout.Strip`.
  - **Why:** Preserves the fast `w` cycle workflow while making presets adaptable to different user displays and habits.
- **Decision:** Minimum column width clamped at 20 cells.
  - **Why:** Prevents columns from becoming 0 or negative width while allowing flexible shrinking on smaller screens.

## Patterns to follow

- Protocol verb definitions: `internal/protocol/messages.go:12-25`
- Keybindings and configuration: `internal/keys/keys.go` and `internal/config/config.go`
- Strip layout mutation: `internal/layout/layout.go`
- Server verb routing and resizing: `internal/server/server.go:209-242`

## What we're NOT doing

- Decoupling card width from contained terminal emulator / pty width (tracked separately in #70).
- Fraction-based presets (like gwae's 1/3, 1/2) in this slice — keeping integer column counts for consistency with existing layout engine.

## Open questions

- Status bar display: Does `,` and `.` need a BarGroup on the 80-col status bar, or should it live in the help overlay?
  - *Default answer:* Put in help overlay without `BarGroup` (like `ActionNameToggleCards`), or group with `w` if possible without exceeding 79 cols. Overlay ensures 80-col bar budget invariant is never broken.
