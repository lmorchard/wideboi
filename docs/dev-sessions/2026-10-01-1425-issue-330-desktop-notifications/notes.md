# Notes: Issue 330 Desktop Notifications

- Worktree: `.worktrees/issue-330-desktop-notifications`
- Branch: `issue-330-desktop-notifications`
- Status: Completed implementation and full verification.

## Decisions & Architecture
- Protocol (v21): Added `MsgPaneNotification` with `pane_id`, `title`, and `message` to `ServerMessage`.
- Configuration: Added `notifications` option (auto, osc9, osc99, bell, off), `--notifications` CLI flag, and `WIDEBOI_NOTIFICATIONS` env var.
- Server: Wired `vt.Callbacks.Bell` to invoke `OnBell` callbacks on `term.Grid`. Panes (both newly spawned and adopted across upgrades) register `SetOnBell(func() { s.onPaneBell(id) })`. `onPaneBell` broadcasts `MsgPaneNotification` to connected clients asynchronously in a goroutine with per-client timeouts, preventing PTY stalls.
- TUI Client: `internal/client/client.go` detects bell notifications for unfocused panes and status transitions from `working` to `needs_input`, `done`, or `failed`. Emits OSC 9, OSC 99 (auto-detecting Kitty), or bell. Sanitizes all emitted text to strip control characters and invalid UTF-8. Verified with thorough test matrix in `TestEmitHostNotificationModesAndSanitization`.
- Web Client: `<wideboi-app>` fires HTML5 `Notification` when tab is backgrounded (`document.hidden`), permission is granted, and `wideboi:notifications` preference is not disabled. `<wideboi-settings>` offers a toggle button between enabled and disabled states.
- Verification: All unit tests in Go and Vitest, Playwright browser suite (53/53), `smoke.py` (41/41), `attachcheck.py` (29/29), `golden.py`, `seam-check`, and `go vet` pass.
