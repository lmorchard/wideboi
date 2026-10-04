# Research: Detect Interrupted or Hung Working Agents via Inactivity Timeout (Issue #383)

## 1. Context & Motivation

In Wideboi, panes transition to `StatusWorking` (`▲` / `»`) when:
1. Authoritative OSC sequences are received (`OSC 133;C` or `OSC 9;4;1` / `OSC 9;4;3`).
2. PTY writes occur and no authoritative status is active.
3. Terminal title spinners (Braille `\u2800-\u28FF` or arc spinners) are active.

However, agents and commands can be interrupted or hang:
- An operator presses `Ctrl-C` to abort a long agent turn, but the process does not emit an OSC completion sequence or clear its terminal title.
- An agent tool crashes, or an API call hangs indefinitely with silent stdout.
- When authoritative status was latched (`sawAuthoritativeStatus = true`), the fallback idle decay timer is disabled.
- Consequently, a pane can remain permanently stuck in `▲ working`, misleading the operator into thinking an agent is actively progressing when it has actually stopped or hung.

Inspiration: [Workmux](https://github.com/raine/workmux) uses an inactivity tracker that monitors working panes. If a working pane produces zero PTY output and zero status updates for an inactivity threshold, it is flagged as interrupted, self-healing as soon as new output arrives.

## 2. Architecture & Design

1. **Protocol Addition:**
   - Add `StatusInterrupted` (5) to `protocol.PaneStatus` and `PANE_STATUS_INTERRUPTED = 5` to `wirepb.PaneStatus`.
   - Glyph: `?`
   - Description: `"interrupted"`
2. **Inactivity Detection in `vtGrid.Status()`:**
   - Add `workingInactivityTimeout time.Duration` (default: 30 seconds).
   - If a pane evaluates to `StatusWorking` and `time.Since(*lastWriteTime) > workingInactivityTimeout`:
     - Demote/flag status as `StatusInterrupted`.
3. **Self-Healing / Automatic Resumption:**
   - The moment any new PTY write arrives on the pane, `lastWriteTime` updates to `now`.
   - `time.Since(*lastWriteTime)` drops to 0, immediately clearing the interrupted status and resuming active tracking.
4. **Dashboard & Status Bar Integration:**
   - Dashboard Wide/Compact: `? interrupted`
   - Dashboard Drawer: `?`
   - Status Bar badge: `[? 2]`
   - Priority in dashboard sorting: priority 4 (alongside `StatusFailed`), ensuring stalled agents surface immediately for operator review.
   - Smart Jump (`<prefix> a`): ranks `StatusInterrupted` above idle, allowing quick jumping to stalled panes.
