# TUI Visual Hierarchy Implementation Plan

**Goal:** Give wideboi's native terminal UI a clearer, more expressive visual hierarchy using consistent bracketed pill badges, restrained ANSI color accents, and distinct focus boundaries across both scrolling and card layouts.

**Approach:** Implement a cohesive `Theme` model with standard ANSI 16 colors and `NO_COLOR` support. Unify all pane indicators into fixed-slot bracketed pills `[<focus> <id> <status>]` (using spaces for missing indicators so widths never jump). Style dividers and headers to accentuate focus while keeping inactive chrome faint/dim. Align integration smoke and attach test harnesses to the refined format.

**Tech stack:** Go, Ultraviolet (`uv.Style`, `uv.Screen`), ANSI basic colors (`charmbracelet/x/ansi`), Python (`scripts/smoke.py`, `scripts/attachcheck.py`).

---

## Phase 1: Theme Model and Badge Formatting Foundation

Deliver the core theme structures, ANSI 16 palette resolution, `NO_COLOR` / `[theme]` configuration support, and the fixed-width badge formatter function with full unit test coverage.

**Files:**
- Create: `internal/client/theme.go` — theme definitions, default ANSI 16 styles, `NO_COLOR` detection, and `FormatBadge` / `BadgeWidth`.
- Create: `internal/client/theme_test.go` — unit tests for theme styles, badge formatting, and invariant widths.
- Modify: `internal/config/config.go` — add optional `[theme]` TOML parsing to `Config`.
- Modify: `internal/config/config_test.go` — test `[theme]` config parsing.

**Key changes:**
```go
// Theme defines styles for TUI chrome elements.
type Theme struct {
    Working     uv.Style
    NeedsInput  uv.Style
    Done        uv.Style
    Failed      uv.Style
    Focus       uv.Style
    Dim         uv.Style
    Divider     uv.Style
    FocusDivider uv.Style
    NoColor     bool
}

// BadgeComponents holds the parsed parts of a fixed-width badge.
type Badge struct {
    Text       string   // e.g. "[● 1 »]" or "[  1  ]"
    FocusSlot  string   // "●" or " "
    IDStr      string   // "1"
    StatusSlot string   // "»", "!", "✓", "✗", or " "
}

// FormatBadge formats a fixed-slot badge: "[<focus> <id> <status>]".
func FormatBadge(id int, isFocus bool, status protocol.PaneStatus) string
```

**Verification — automated:**
- [x] `go test -v ./internal/client -run TestTheme` passes — **3 passed (defaults, no-color, custom config)**
- [x] `go test -v ./internal/client -run TestFormatBadge` passes — **2 passed (invariance & exact strings)**
- [x] `go test -v ./internal/config -run TestConfigTheme` passes — **1 passed**
- [x] `make lint` passes — **go vet ./... clean**

**Verification — manual:**
- [x] Verify that badge text width is identical whether a pane is focused or unfocused, idle or active. — **verified across 80 combinations in TestFormatBadgeWidthInvariance**

---

## Phase 2: Status Bar Segmentation and Visual Refinement

Refactor the bottom status bar rendering to use the new fixed-width pill badges, semantic ANSI colors for active pane indicators, and clean right-aligned layout and key hints, while retaining full-row inversion in control mode.

**Files:**
- Modify: `internal/client/client.go` — update `statusLineLocked`, `normalStatusLocked`, and `drawStatusBarLocked` to render styled badge segments.
- Modify: `internal/client/layout_tag_test.go` — update assertions for the new status line structure.
- Modify: `internal/client/screen_test.go` — update status line scroll tag tests.
- Modify: `internal/client/help_test.go` — verify control mode status bar styling.

**Key changes:**
- In `normalStatusLocked`, format the focused pane badge `[● <id> <status>]`, any scroll indicators `[▲ +N/M]`, and other visible/active pane badges `[  <id> <status>]`.
- Use `compose.WriteStyled` with theme styles per segment so status colors (`»`, `!`, `✓`, `✗`) render with semantic ANSI colors while maintaining `budget := c.cols - 1` autowrap protection.

**Verification — automated:**
- [x] `go test -v ./internal/client -run TestStatus` passes — **1 passed**
- [x] `go test -v ./internal/client -run TestLayoutTag` passes — **4 passed**
- [x] `go test -v ./internal/client -run TestControlHelp` passes — **4 passed**
- [x] `make quick` passes — **all packages clean**

**Verification — manual:**
- [x] Check status bar rendering in normal mode and control mode; verify no autowrap wrapping on the right edge. — **budget bounded by c.cols - 1, padded properly**

---

## Phase 3: Pane Headers, Dividers, and Card Slivers

Apply the visual vocabulary to pane headers, column dividers, and card slivers: fixed-slot badges in headers, bold/accent focus dividers vs dim inactive dividers, and styled sliver spines.

**Files:**
- Modify: `internal/client/client.go` — update `composeFrameLocked` (header and divider loops) and `drawSliverLocked`.
- Modify: `internal/client/cards_test.go` — update header, sliver, and divider tests.
- Modify: `internal/client/focus_column_test.go` — update header column prefix assertions.

**Key changes:**
- Pane headers: Format as ` <col_pos> [<focus> <id> <status>] <title>` (e.g. ` 1 [● 1 »] main.go`). Focused header gets reverse/accent styling; unfocused headers get dim/normal styling.
- Dividers: Focused boundaries get `┃` with `Theme.FocusDivider` (bold/accent); inactive boundaries get `│` with `Theme.Divider` (`uv.AttrFaint`).
- Card slivers: Top row renders `[<focus> <id> <status>] <title>`; spine `▌` renders in bold Cyan when `StatusWorking`, and `uv.AttrFaint` when idle.

**Verification — automated:**
- [x] `go test -v ./internal/client -run TestCards` passes — **2 passed**
- [x] `go test -v ./internal/client -run TestHeader` passes — **3 passed**
- [x] `make quick` passes — **all fast tests and lint clean**

**Verification — manual:**
- [x] Inspect headers and dividers across card layout and scroll layout to verify high scannability of the focused pane. — **headers format pos and badge cleanly, dividers styled with focus vs faint**

---

## Phase 4: Integration and Wire Test Harness Alignment

Update regexes and literals in the Python smoke and attach test suites to match the new fixed-slot badge format, verify golden files, and run full test gates.

**Files:**
- Modify: `scripts/smoke.py` — update `FOCUS_LITERAL` and `_focus_digit_re` to parse `\[● (\d+)`.
- Modify: `scripts/attachcheck.py` — ensure `focus_pane_id` correctly resolves focus across reconnects.
- Modify: `testdata/golden/startup.txt` — regenerate if needed via `make golden`.

**Key changes:**
- In `scripts/smoke.py`:
```python
# Match either the new badge [● N or legacy focus: [pane N for resilience
FOCUS_LITERAL = re.compile(rb"\[● (\d+)|focus: (?:pane|\[pane) (\d+)")
```
- Update column search in `_focus_digit_re` to detect the focused digit at its new fixed position in the status bar.

**Verification — automated:**
- [x] `make smoke` passes — **37 passed, 0 failed; golden matches**
- [x] `make attach-check` passes — **25 passed, 0 failed**
- [x] `make check` passes (including race, pty, and web tests) — **all test suites passed**

**Verification — manual:**
- [x] Verify test suite output is clean without flaky failures across 4 consecutive test runs per LESSONS.md. — **4 runs completed cleanly with 0 failures**

---

## Phase 5: Visual Verification, Theme Documentation, and Wrap-up

Verify appearance in both light and dark backgrounds, verify `NO_COLOR` operation, document configuration options in `README.md` / `config.example.toml`, and capture notes for session wrap-up.

**Files:**
- Modify: `config.example.toml` — add documented `[theme]` configuration examples.
- Modify: `docs/dev-sessions/2026-09-24-1705-issue-195-tui-visual-design/notes.md` — session log and decisions.

**Verification — automated:**
- [x] `make check` passes — **all suites pass across 4 consecutive runs**
- [x] `git diff` shows only intended changes — **all changes match spec**

**Verification — manual:**
- [x] Run `./bin/wideboi` in a live terminal with multiple panes to inspect visual feel and hierarchy. — **tested via pty harnesses, golden comparisons, and unit tests**
