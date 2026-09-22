# Spec: Issue #23 — Support OSC 9;4 Progress

## Goal

Add support for the ConEmu / Windows Terminal progress reporting protocol (`OSC 9;4`),
which is emitted by CLI coding agents (including Claude Code) during active turns.
Map these progress notifications to `term.PaneStatus` and integrate them with the
authoritative status latch currently governed by `sawOSC133`.

---

## Current State

1. `internal/server/term/grid.go` registers an OSC handler only for command `133` (shell integration semantic prompts).
2. `vtGrid` tracks status authority via `sawOSC133 atomic.Bool`. When clear, `Write` sets `StatusWorking` and `Status()` decays to `StatusIdle` after 3 seconds of silence. When set, fallback heuristics are disabled.
3. Interactive CLI agents (like Claude Code) do not emit shell integration prompts (`OSC 133`). Instead, they emit `ESC]9;4;3;BEL` (turn start / indeterminate) and `ESC]9;4;0;BEL` (turn end / clear). Currently, these are unhandled and dropped, leaving agent panes reliant on the 3-second write-decay heuristic.

---

## Desired End State

1. **OSC 9 Handler**: `vtGrid` registers an OSC handler for command `9` via `x/vt` (`RegisterOscHandler(9, ...)`).
2. **Progress Payload Parsing**: The handler splits `data` on `;` (`parts[0]` is `"9"`):
   - Only payloads where `len(parts) >= 3` and `parts[1] == "4"` are handled as progress.
   - States map to `term.PaneStatus`:
     - `"0"` (clear progress) &rarr; `StatusDone`
     - `"1"` (progress percentage) &rarr; `StatusWorking`
     - `"2"` (error) &rarr; `StatusFailed`
     - `"3"` (indeterminate / busy) &rarr; `StatusWorking`
     - `"4"` (warning / paused) &rarr; `StatusNeedsInput`
   - Non-progress OSC 9 sequences (or malformed progress sequences) return `false`, allowing `x/vt` to log them and avoiding false latches.
3. **Unified Authoritative Latch**:
   - `sawOSC133` is renamed / generalized to `sawAuthoritativeStatus atomic.Bool`.
   - Either a valid OSC 133 or a valid OSC 9;4 sequence sets `sawAuthoritativeStatus.Store(true)`.
   - Both `Write()` and `Status()` check `!g.sawAuthoritativeStatus.Load()`.
4. **Precedence**: Last writer wins per pane. A pane receiving OSC 133 followed by OSC 9;4 (or vice versa) simply takes the most recent status.
5. **Testing**: Comprehensive tests in `internal/server/term/osc_test.go`:
   - Table-driven unit tests for all OSC 9;4 states.
   - Verification that valid OSC 9;4 latches `sawAuthoritativeStatus` (disabling idle decay).
   - Verification that malformed or non-progress OSC 9 payloads do not latch.
   - Interleaving OSC 133 and OSC 9;4.

---

## Patterns to Follow

- `internal/server/term/grid.go:220-266`: OSC 133 handler structure, payload splitting, switch matching, and atomic stores.
- `internal/server/term/osc_test.go:19-65`: Table test pattern driving real bytes through `g.Write()`.
- `internal/server/term/osc_test.go:103-120`: Injected idle timeout testing to verify latch behavior without sleep penalties.

---

## What We're NOT Doing

- Not handling other OSC 9 subtypes (such as iTerm2 desktop notifications: `OSC 9 ; <msg> BEL`).
- Not displaying numeric percentage progress in the UI (Wideboi's status model uses glyphs: `»`, `!`, `✓`, `✗`).
- Not changing client/server protocol types (`protocol.MsgLayoutSnapshot` already carries `PaneStatuses`).
