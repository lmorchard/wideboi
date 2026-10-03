# Research: Detecting Agent Permission Prompts and Blockers (Issue #376)

## 1. Context & Problem Statement

Wideboi models pane status via `protocol.PaneStatus`:
- `StatusIdle` (●)
- `StatusWorking` (▲ / »)
- `StatusNeedsInput` (! / input)
- `StatusDone` (✔ / ✓)
- `StatusFailed` (✖ / ✗)

Currently, `StatusNeedsInput` is only triggered by:
- Semantic prompt OSC 133 sequences (`133;A`, `133;B`)
- ConEmu progress OSC 9;4 sequences (`9;4;4`)

Modern AI coding agents (Claude Code, OpenCode, Codex, Aider) do not emit OSC 133 or OSC 9;4 out of the box. Instead, when they pause to ask for tool permissions, file edit approvals, or questions:
1. They stop writing to the PTY master while waiting on stdin.
2. In `internal/server/term/grid.go:589-601`, `vtGrid.Status()` checks `time.Since(*lastWriteTime) > idleTimeout` (default 3s).
3. After 3 seconds, `vtGrid.Status()` falls back to `StatusIdle`.
4. As a result, `StatusNeedsInput` is effectively never triggered for AI coding agents. Blocked agents are shown as `idle`, desktop notifications (#330) are not triggered, and operator attention is not alerted.

Furthermore, when Claude Code or Codex is thinking or executing a long-running background command, it often writes nothing to stdout for > 3s, but updates its OSC terminal title with spinner glyphs (Braille `\u2800-\u28FF` or arc spinners `\u25D0-\u25D3`). Wideboi currently ignores this and marks the pane as `idle` after 3s.

## 2. Key Code Locations

- `internal/server/term/grid.go`:
  - `vtGrid`: Struct holding emulator (`em`), write times (`lastWriteTime`), atomic status (`status`), `sawAuthoritativeStatus`, `generation`, `outputGen`, `title`.
  - `Status()` (lines 589–601): Returns `protocol.PaneStatus`. If `!sawAuthoritativeStatus && status == StatusWorking`, checks `time.Since(lastWriteTime) > idleTimeout`.
  - `DumpText()` (lines 1116–1220): Can extract non-blank lines from the bottom of the screen (`tailLines int`).
  - `Write()` (lines 537–557): Updates `lastWriteTime`, increments `outputGen` and `generation`, processes OSC sequences.
- `internal/server/server.go`:
  - `statusGlyphsLocked()` (line 788): Queries `p.Status()` on all panes.
  - `broadcastLayoutIfStatusChanged()` (line 963): Runs on every 33ms ticker tick, checks `sameStatusMap(statusGlyphsLocked(), lastStatuses)`.
- `internal/server/term/osc_test.go`:
  - Existing unit tests for OSC 133, OSC 9;4, and idle timeout behavior.

## 3. Findings & Constraints

1. **Performance on 33ms Ticker**:
   `Status()` is called every 33ms by `broadcastLayoutIfStatusChanged`. We cannot afford to rescan the screen buffer or run regexes on every 33ms tick if nothing has changed.
   - Solution: Use `g.outputGen` (incremented on `Write`). When writes pause, we scan the screen tail and title once for that generation, cache the result, and return the cached status on subsequent ticks until new writes occur.

2. **OSC Title Spinners**:
   - Claude Code emits OSC 0/2 titles with Braille spinner characters (`\u2800` to `\u28FF`) like `⠋ Thinking...`.
   - Arc spinners (`\u25D0` to `\u25D3`) like `◐ ◓ ◑ ◒`.
   - If an active spinner is present in the title, the agent is working even if stdout is silent.

3. **Blocker Signatures**:
   - Claude Code: `"esc to cancel"`, `"enter to confirm"`, `"Do you want to proceed?"`, `"waiting for permission"`, `"do you want to run"`, `"do you want to allow"`.
   - OpenCode: `"△ Permission required"`, `"esc dismiss"` with `"enter confirm"` or `"enter submit"`.
   - Codex: `"Action Required"` in title; `"Do you trust the contents of this directory?"`, `"Trust and continue"`.
   - Generic: `"[y/N]"`, `"[Y/n]"`, `"(y/n)"`.

4. **Working Signatures on Screen**:
   - Interrupt hints: `"esc to interrupt"`, `"ctrl+c to interrupt"`, `"press esc to interrupt"`.
   - Progress bars: `[■⬝]{4,}`.
