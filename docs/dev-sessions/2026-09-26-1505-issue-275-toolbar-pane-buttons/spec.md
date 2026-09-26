# Consolidate Title and Status into Toolbar Pane Selector Buttons Spec

**Goal:** Reclaim ~34px of vertical terminal space by consolidating the standalone title and status divs into a unified single toolbar featuring interactive pane selector buttons.

**Source:** https://github.com/lmorchard/wideboi/issues/275

## Current state
- In `web/src/wideboi-app.ts:2259-2324`, `<div class="title">` (height: 16.8px) and `<div class="status">` (height: 16.8px) sit between `.pane-strip` and `.toolbar`.
- When search is inactive, `.status` renders a static text string with scroll offset and pane statuses.
- When search is active, `.status.search-bar` replaces the content with an absolute search input overlay.
- The toolbar at line 2327-2332 uses a `<select id="focus-pane">` dropdown to select and switch the active pane.
- Height consumed by title and status is ~34px, stealing 2-3 lines of terminal screen space across all layouts.

## Desired end state
1. **Remove `<div class="title">` and static `<div class="status">`** from `terminal-shell`.
2. **Unified single toolbar layout**:
   - The toolbar hosts a horizontally scrollable pane selector button row (`role="tablist"` or button row) on the left, followed by the existing utility controls (`pane-width`, `Follow PTY`, `Fit to Window`, `Search`, `Settings`, `Help`, Tip) on the right.
   - Each pane button displays:
     - Active focus indicator (active styling, distinct background, and/or `●` dot)
     - Pane ID: e.g. `1`
     - Pane Title: e.g. `vim`, `zsh`, truncated via `max-width` + ellipsis
     - Status glyph: `»` (working), `!` (needs input), `✓` (done), `✗` (failed), matching TUI colors and glyphs from `internal/client/theme.go:44-62`
     - Scroll / unread indicator: `[+12 ⤓]` when the pane has active scroll offset or unread output
   - Clicking a pane button calls `focusPane(id)` immediately.
3. **Search overlay**:
   - When search is triggered, `<div class="status search-bar">` (retaining classes for backward compatibility with tests) overlays the pane button strip in the toolbar (or docks in place of the tab strip), restoring the tabs when search is accepted or cancelled.
4. **Mobile viewport (`< 480px`)**:
   - Keep `.mobile-bar` intact with its compact `‹`, `<select>`, zoom, `›`, `⚙` controls.

## Design decisions
- **Decision: Unified single bar rather than two-deck toolbar**
  - **Why:** Maximum vertical space savings (~34px reclaimed).
  - **Rejected:** Two-deck toolbar (one deck for tabs, one deck for utility buttons) which would retain ~17px of vertical overhead.
- **Decision: Search overlays the tab strip in the toolbar**
  - **Why:** Keeps search in the user's focus area without needing a third bar or permanent space reservation.
  - **Rejected:** Pinned search bar above toolbar or modal dialog.
- **Decision: Pane buttons use flex child with `overflow-x: auto` and scrollbar hidden**
  - **Why:** Allows sessions with 10+ panes to scroll smoothly without wrapping awkwardly or breaking toolbar controls.
  - **Rejected:** Hard-wrapping all pane buttons or dropping to a dropdown after N panes.

## Patterns to follow
- TUI Status Glyphs & Styles: `internal/client/theme.go:44-62` (`»`, `!`, `✓`, `✗`).
- Pane button styling: match existing button style in toolbar (`.toolbar button`, `web/src/wideboi-app.ts:173-185`).
- Search bar styling: `web/src/wideboi-app.ts:57-115`.
- Selection handling: `this.focusPane(id)` in `web/src/wideboi-app.ts:1688`.

## What we're NOT doing
- Refactoring `wideboi-app.ts` into sub-components (handled separately in #269).
- Modifying the mobile bar layout or mobile input dock.
- Changing server-side protocol messages or layout snapshots.
- Adding re-orderable / draggable pane tabs.

## Open questions
None. (Design decisions confirmed: single bar, search overlaying tab strip).
