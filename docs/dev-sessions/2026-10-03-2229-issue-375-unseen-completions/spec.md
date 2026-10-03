# Spec: Distinguish Unseen Completions from Idle Panes (Issue #375)

**Goal:** Badge panes with unseen completions as `Done` (`✓`) until the user focuses / views the pane, preventing background completions from silently decaying to `Idle` (`●`) unnoticed.

**Source:** https://github.com/lmorchard/wideboi/issues/375

---

## Current State

- Panes report `StatusWorking` while writing to stdout.
- When an agent turn finishes, write activity stops, and status decays to `StatusIdle` after `DefaultIdleTimeout` (3s).
- Long-running background jobs that complete while the user is looking elsewhere show `● idle`, indistinguishable from untouched shells.

---

## Desired End State

1. **Unseen Completion Transition:**
   - When an **unfocused** pane transitions from `StatusWorking` to `StatusIdle`:
     - It is marked as having an unseen completion (`unseenDone = true`).
     - Visually rendered as **`StatusDone`** (`✓` / `✔ done`).
2. **Clear on Focus (Seen):**
   - As soon as the user focuses or jumps to that pane:
     - The unseen completion flag is cleared.
     - The status badge transitions to **`StatusIdle`** (`●` / `● idle`).
3. **Reset on New Activity:**
   - If the pane begins writing output again (`StatusWorking`), the unseen completion flag is cleared.
4. **Attention Navigation (<prefix> a / Smart Jump):**
   - In `smartJumpTargetLocked`, `StatusDone` ranks higher than `StatusIdle` (rank 2 vs rank 0).
   - Pressing `<prefix> a` cycles through unseen completed panes and focuses them, automatically clearing their `Done` badges as they are reviewed.
5. **Dashboard Overview (<prefix> s):**
   - In `updateDashboardLocked`, unseen completed panes are listed with `StatusDone`, placing them at priority 3 in the dashboard sorting order so completed work surfaces near the top.

---

## Design Decisions

- **Decision:** Track unseen completions per client on `Client` (and on `Server` for server dashboard).
  - **Why:** Preserves the core invariant that pane focus is client-local presentation. Multiple attached clients can review completions at their own pace without clearing badges for each other.
- **Decision:** Integrate into `smartJumpTargetLocked`.
  - **Why:** Turns `<prefix> a` into an attention inbox iterator: jump directly to the next completed or stuck task.

---

## Verification Plan

- Unit tests in `internal/client/client_test.go` and `internal/client/statusbar_test.go`:
  - Verify unfocused pane transitioning `Working -> Idle` receives `StatusDone` badge.
  - Verify focusing the pane clears the badge back to `StatusIdle`.
  - Verify focused pane transitioning `Working -> Idle` transitions directly to `StatusIdle` without sticking at `Done`.
  - Verify new write activity (`Working`) clears the `Done` badge.
  - Verify `smartJumpTargetLocked` selects the unseen completed pane.
- Unit tests in `internal/server/server_test.go`:
  - Verify server dashboard displays `✔ done` for unfocused pane completing work.
- Unit tests in `web/src/`:
  - Verify web client marks unseen completions as `Done` and clears on focus.
- Run `make quick` and ensure clean passes.
