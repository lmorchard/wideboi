# Plan: Issue 330 Desktop Notifications

## Proposed Changes

### Phase 1: Configuration & CLI Flags
1. `internal/config/config.go`:
   - Add `Notifications string` to `Config` and `ConfigFlags`.
   - In `Load`: validate and assign `Notifications` with default `"auto"`.
   - Support `WIDEBOI_NOTIFICATIONS` env var.
2. `cmd/wideboi/main.go`:
   - Register `--notifications <mode>` flag.
   - Update help text.
   - In `runClient`: call `cli.SetNotifications(cfg.Notifications)`.
3. `docs/MANUAL.md`:
   - Document notification options.
4. `cmd/wideboi/cli_test.go`:
   - Verify `--notifications` and `WIDEBOI_NOTIFICATIONS` in help output.

### Phase 2: Wire Protocol (v21) & Codec
1. `internal/protocol/wirepb/wideboi.proto`:
   - Add message `MsgPaneNotification { int32 pane_id = 1; string title = 2; string message = 3; }`.
   - Add `MsgPaneNotification pane_notification = 19;` to `ServerMessage`.
2. Run `make proto` to generate Go and TS code.
3. Bump protocol version to 21 in `internal/protocol/version.go` and `web/src/version.ts`.
4. `internal/protocol/messages.go`:
   - Add `MsgPaneNotification struct { PaneID int; Title string; Message string }`.
5. `internal/protocol/codec.go`:
   - Encode/decode `MsgPaneNotification`.
6. `internal/protocol/version_guard_test.go`:
   - Record v21 schema hash.

### Phase 3: Server Bell Callbacks
1. `internal/server/term/grid.go`:
   - Add `OnBell(fn func())` to `Grid` interface.
   - Wire `vt.Callbacks.Bell` in `vtGrid`.
2. `internal/server/pane.go`:
   - Add `p.SetOnBell(fn func())`.
3. `internal/server/server.go`:
   - Wire `OnBell` in `spawnPaneWithSpecLocked` and `RestorePanes`.
   - Implement `onPaneBell(paneID int)`.
4. Tests in `internal/server/server_test.go`:
   - Verify `TestServerBellNotification`.

### Phase 4: TUI Client Notification Emission
1. `internal/client/client.go`:
   - Implement `SetNotifications(mode string)`.
   - Implement `SetNotificationEmitter(fn func(title, msg string))`.
   - Handle `MsgPaneNotification` and `MsgLayoutSnapshot` transitions.
   - Implement `emitHostNotification` and `sanitizeNotificationText`.
2. Unit tests in `internal/client/client_test.go`:
   - Verify status transitions and bell notifications for focused vs unfocused panes.

### Phase 5: Web Client Notifications
1. `web/src/wideboi-app.ts`:
   - Handle `paneNotification` and background status transitions with HTML5 `new Notification`.
2. `web/src/components/settings-dialog.ts`:
   - Add permission request button with clean styling that doesn't overflow or break dialog buttons.

## Verification
- `go test ./...`
- `cd web && npm test`
- `cd web && npm run test:browser`
- `python3 scripts/smoke.py`
- `python3 scripts/attachcheck.py`
- `make check`
