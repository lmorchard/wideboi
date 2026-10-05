# Research: OSC and event plumbing (2026-10-05)

From a documentarian pass. Items marked ✔ were re-read first-hand.

## OSC in panes
- `internal/server/term/grid.go` registers only OSC 133 (365), 9 (413), 7 (455) and 1337 (487). Each handler receives the whole payload, e.g. `"133;A"` (366-374). ✔ no 52.
- Unhandled OSC: vt `osc.go:14-19` calls `logf`, which is a no-op because wideboi never calls `SetLogger`, so the sequence is silently dropped. Handlers run last-registered-first, and the first one returning true wins (`handlers.go:146-157`).
- vt has no clipboard handling or callback (`callbacks.go`). ✔ (grep)
- ✔ vt's parser data buffer is 4 MB (`vt/emulator.go:89`). Bytes past that are dropped silently (`ansi/parser_decode.go:339`), and the handler still fires with truncated data.
- `oscfix.go` replaces UTF-8 that contains 0x9C inside OSC strings. It keeps a 4096-byte raw copy only for re-setting titles. Base64 payloads are ASCII, so it doesn't affect them.
- Grid data leaves by polling (Title/CWD/UserVars/Status, `server.go:520-530`, `broadcastMetadataIfChanged` 929-980). The one exception is the bell, which uses a callback.

## One-shot pane→client event: bell (the model to copy)
- vt Bell callback (`grid.go:340`) → `g.onBell` (set by `OnBell`, 775). It runs on the PTY reader goroutine.
- Wired at spawn (`server.go:588-590`) and on upgrade adoption (`upgrade.go:358-360`).
- ✔ `onPaneBell` → `go broadcastPaneBell` (`server.go:615-650`). It copies `s.transports` under `s.mu` and sends `MsgPaneNotification` to **every** transport, with a 100 ms timeout each and no attached filter.
- TUI: `client.go:258-261` → `emitHostNotificationTo` writes to `os.Stdout` on the main goroutine, outside `screenLock`.
- Web: `wideboi-app.ts:739-745`.

## Protocol
- Schema `internal/protocol/wirepb/wideboi.proto`, `ServerMessage` oneof. `make proto` / `make proto-check` (`Makefile:238-247`).
- Go structs in `messages.go`, converted only in `codec.go`. An unknown type is an error (`codec.go:447,640`).
- `version.go` Version = 29. `web/src/version.ts` must match. `version_guard_test.go` holds the descriptor hash per version (4-step comment).
- Last new server→client message: `MsgPaneNotification` (v21, commit 2eb45c9). Use its file list as the checklist.
- Web: `wideboi-app.ts:595` switch has no default, so unknown cases are ignored.

## TUI host writes
- `writeClipboard(scr, text)` (`cmd/wideboi/main.go:1377-1389`) needs `screenLock` held and `!stopped`. It writes OSC 52 via `scr.WriteString` + `Flush`, then calls `writeLocalClipboard`, which is skipped under SSH.

## Web clipboard
- `copyToClipboard` (`wideboi-app.ts:996-1030`): `navigator.clipboard.writeText`, falling back to `execCommand('copy')`. No `isSecureContext` check. Writes without a user gesture will usually be blocked by browsers (not verified here).

## Config
- `internal/config/config.go`: booleans use `*bool` + resolved (`Mouse`/`MouseEnabled` 43-46, resolved 692). `applyFile` sets mouse/notifications before the trust gate (381-406), so untrusted project files can set them. The sensitive list lives after 406.
- Client-only settings are applied in `cmd/wideboi/main.go` (e.g. `cli.SetNotifications`, 1007).
- Docs: `docs/MANUAL.md` §5 "Mouse and Clipboard" (236-259) and the config section (~500-530).
