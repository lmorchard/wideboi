# Spec: Issue 330 Desktop Host Notifications

## Motivation & Background
Wideboi tracks rich pane status via OSC 133 / shell integration (`idle`, `working`, `needs_input`, `done`, `failed`) and provides `smart_jump` (`Ctrl+b a`).
However, when a user is focused on one pane (or another desktop window) and a background command finishes, needs input, fails, or emits a terminal BEL (`\a`), the host desktop receives no alert.

Zellij and other multiplexers bridge background pane events to host notifications:
- **OSC 9**: `\e]9;title: message\a` (macOS Terminal.app, iTerm2, Windows Terminal)
- **OSC 99**: `\e]99;;title: message\a` (Kitty specification)
- **Bell fallback**: `\a`
- **Web client**: HTML5 Web Notifications API (`new Notification(...)`) when tab is backgrounded.

## Requirements

1. **Configuration**:
   - `notifications` option in `config.toml` / `.wideboi.toml`:
     - Options: `"auto"` (default), `"osc9"`, `"osc99"`, `"bell"`, `"off"`.
   - `--notifications <mode>` CLI flag.
   - `WIDEBOI_NOTIFICATIONS` environment variable.

2. **Wire Protocol (v21)**:
   - `message MsgPaneNotification`:
     ```proto
     message MsgPaneNotification {
       int32 pane_id = 1;
       string title = 2;
       string message = 3;
     }
     ```
   - Added as variant in `ServerMessage`:
     `MsgPaneNotification pane_notification = 19;`
   - Bump `protocol.Version` and `PROTOCOL_VERSION` to 21.

3. **Server-Side Triggering**:
   - `vt.Callbacks.Bell`: register callback on each `Grid` to invoke `OnBell`.
   - In `internal/server/server.go`:
     - When a pane is spawned (and when restored in `RestorePanes`), register `p.SetOnBell(func() { s.onPaneBell(id) })`.
     - `onPaneBell(paneID int)`: broadcast `MsgPaneNotification` with the pane's title and `"Alert"` message to all transports.

4. **TUI Client Notification Emission**:
   - In `internal/client/client.go`:
     - Listen for `MsgPaneNotification`: if `m.PaneID != c.focusPaneID`, emit host notification.
     - Listen for `MsgLayoutSnapshot`: if an unfocused pane transitions from `StatusWorking` to `StatusNeedsInput` ("Needs input"), `StatusDone` ("Finished successfully"), or `StatusFailed` ("Failed"), emit host notification.
     - Mode handling:
       - `"off"`: no-op.
       - `"bell"`: write `\a` to `os.Stdout`.
       - `"osc9"`: write `\x1b]9;title: msg\x07`.
       - `"osc99"`: write `\x1b]99;;title: msg\x07`.
       - `"auto"`: detect Kitty via `KITTY_WINDOW_ID != ""` or `TERM` containing `"kitty"`, else use `"osc9"`.
     - Sanitize title and message text before emission: strip control characters (< 0x20, 0x7f, 0x80..0x9f) to prevent escape sequence injection into the host terminal.
     - Testing hook: `SetNotificationEmitter(fn func(title, msg string))` so unit tests don't emit raw escapes to stdout.

5. **Web Client Notification Handling**:
   - In `web/src/wideboi-app.ts`:
     - When tab is hidden (`document.hidden`), if `Notification.permission === 'granted'`:
       - On `MsgPaneNotification` for an unfocused pane: trigger `new Notification(title, { body: message })`.
       - On `MsgLayoutSnapshot` for an unfocused pane transitioning from `WORKING` to `NEEDS_INPUT`, `DONE`, or `FAILED`: trigger notification.
   - In `web/src/components/settings-dialog.ts`:
     - Provide a button in Settings to request notification permission (`Notification.requestPermission()`).
     - Ensure layout does not overflow or clip existing dialog elements (especially `.close-btn`).
