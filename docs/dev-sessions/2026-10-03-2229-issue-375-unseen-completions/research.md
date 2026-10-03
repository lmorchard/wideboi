# Research: Distinguish Unseen Completions from Idle Panes (Issue #375)

## 1. Context & Motivation

Wideboi models `protocol.PaneStatus`:
- `StatusIdle` (●)
- `StatusWorking` (▲ / »)
- `StatusNeedsInput` (! / input)
- `StatusDone` (✔ / ✓)
- `StatusFailed` (✖ / ✗)

For long-running tasks and agent turns, output writes keep a pane in `StatusWorking`. When the process finishes its turn and pauses, it decays to `StatusIdle` after the idle timeout (3 seconds).

If an engineer runs several tasks across columns and works in one pane or steps away from their desk:
- When background tasks finish, their status quietly drops to `● idle`.
- The operator cannot distinguish between panes that just completed tasks versus panes that were already idle.
- In Herdr, completed background panes remain marked as **`Done`** (`✓`) until the operator explicitly focuses / inspects that pane, at which point the badge transitions to `seen = true` (`● idle`).

## 2. Invariants & Architecture

1. **Client-Local Focus vs. Server Focus**:
   - In Wideboi, pane focus is client-local presentation (`internal/client/client.go:630-670`, `TestClientsKeepIndependentFocusAcrossSnapshots`).
   - Different clients attached to the same session can focus different panes simultaneously.
   - Therefore, "seen" state must be tracked by each client independently so one client viewing a pane does not prematurely clear the completion badge for another client who hasn't seen it yet.
2. **Server Dashboard (`<prefix> s`)**:
   - The server renders the status dashboard into an in-memory VT grid (`updateDashboardLocked`).
   - The server maintains its own session-level `s.unseenDone` map (based on `s.strip.FocusedPaneID()`) so the status dashboard lists background completed panes as `✔ done` (which also promotes them to priority 3 in the dashboard sorting order from #374).
3. **Smart Jump Integration**:
   - `<prefix> a` (`smartJumpTargetLocked`) jumps to attention-needing panes. Unseen completed panes (`StatusDone`) rank above idle panes, allowing `<prefix> a` to sequentially cycle through completed background jobs!

## 3. Implementation Areas

- `internal/client/client.go`:
  - `unseenDone map[int]bool` field on `Client`.
  - `displayStatusLocked(id int) protocol.PaneStatus` helper.
  - Cleared on focus changes (`FocusPaneID`, `FocusLeft`, `FocusRight`, `FocusLast`, `SmartJump`, mouse clicks, digit keys).
- `internal/client/statusbar.go` & `render.go`:
  - Use `displayStatusLocked(id)` when formatting badges and drawing capsules.
- `internal/server/server.go`:
  - `s.unseenDone map[int]bool` field on `Server`.
  - Marked on `WORKING -> IDLE` transition when unfocused.
  - Cleared when focused in server strip or selected in dashboard.
  - Used in `updateDashboardLocked()`.
- `web/src/wideboi-app.ts`:
  - `unseenDone: Record<number, boolean>`.
  - Display status helper in cards, mobile bar, and smart jump.
