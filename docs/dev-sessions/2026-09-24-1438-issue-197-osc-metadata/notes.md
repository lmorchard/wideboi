# Dev Session Notes: Track Pane CWD and Agent Metadata via OSC 7 and OSC 1337

**Issue:** #197 (https://github.com/lmorchard/wideboi/issues/197)
**Branch:** `issue-197-osc-metadata`
**Worktree:** `.worktrees/issue-197-osc-metadata`

## What was built

1. **OSC 7 CWD Tracking:**
   - Supported sequence: `ESC ] 7 ; file://[hostname]/path ST|BEL`.
   - Hostname validation: matches local machine hostname, `localhost`, or empty hostname. Foreign hostnames are ignored.
   - Path percent-decoding and normalization via `filepath.Clean`.
   - Exposed through `term.Grid.CWD()` and `server.Pane.CWD()`.

2. **OSC 1337 `SetUserVar` Agent Metadata:**
   - Supported sequence: `ESC ] 1337 ; SetUserVar=<name>=<base64-value> ST|BEL`.
   - Key names validated: 1..64 chars `^[a-zA-Z0-9_-]+$`.
   - Value validated: valid base64, valid UTF-8, max 4096 bytes decoded.
   - Deletion semantics: empty decoded string unsets the variable.
   - Per-pane capacity: up to 64 user variables per pane.
   - Exposed through `term.Grid.UserVars()` and `server.Pane.UserVars()`.

3. **Wire Protocol Delivery:**
   - Added `MsgPaneMetadata` message to protobuf and Go structs with `pane_id`, `cwd`, and `user_vars`.
   - Added `pane_metadata = 6` to `ServerMessage` oneof.
   - Bumped `protocol.Version` from `2` to `3` in `internal/protocol/version.go` and `web/src/client.ts` (`wideboi.v3`).
   - Regenerated protobuf bindings for Go and TypeScript (`make proto`).
   - Documented in `docs/PROTOCOL.md`.

4. **Server Tracking & Broadcast:**
   - Server tracks metadata state per pane on the 33ms ticker in `Server.Run`, broadcasting `MsgPaneMetadata` only when CWD or user variables change.
   - Server sends initial metadata for all active panes on `MsgAttach` and `MsgStatusRequest`.
   - Terminal client and web client store pane metadata in their internal state.

5. **`wideboi status` CLI Presentation:**
   - Table mode includes `CWD` column: `PANE ID  WIDTH  HEIGHT  STATUS  TITLE  CWD` (shows `-` if unset).
   - `--json` mode encodes a structured status object:
     ```json
     {
       "columns": [...],
       "pane_statuses": {...},
       "pane_titles": {...},
       "pane_metadata": {
         "1": {
           "pane_id": 1,
           "cwd": "/path",
           "user_vars": { "key": "val" }
         }
       }
     }
     ```
   - Maintains compatibility with legacy unmarshal into `MsgLayoutSnapshot`.

## Verification Evidence

- `go test ./internal/server/term -run TestOSC`: passed unit tests covering valid, invalid, malformed, and boundary payloads for OSC 7 and OSC 1337.
- `go test ./internal/protocol`: all codec round-trip tests and schema coverage tests passed for `MsgPaneMetadata`.
- `make proto-check`: clean, zero diff between committed bindings and schema generation.
- `go test ./internal/server -run "TestMetadata|TestMsgStatusRequest"`: verified change-driven broadcasts and status query metadata delivery.
- `go test ./cmd/wideboi -run "TestRunStatus|TestEndToEndPTYMetadataReporting"`: verified table and JSON status formats and end-to-end PTY execution with a real child process emitting OSC 7 and OSC 1337.
- `make check`: all gate targets passed:
  - `fmt-check`
  - `lint` (`go vet`)
  - `seam-check`
  - `test` (Go unit tests)
  - `web-test` (vitest unit tests, 7 files / 23 tests)
  - `web-accept` (Playwright browser test in Chromium)
  - `race` (`go test -race -count=1 ./...`)
  - `verify-exit` (signal exits in real PTY)
  - `smoke` (37/37 smoke tests passed)
  - `golden` (golden wire snapshot matches)
  - `attach-check` (25/25 attach check tests passed)

## Commits on branch

- `Phase 1: Terminal parser and grid support for OSC 7 and OSC 1337` (`2214735`)
- `Phase 2: Wire protocol schema, codec, version bump, and web client` (`94a4970`)
- `Phase 3: Server metadata tracking, broadcast loop, and client storage` (`bfe568e`)
- `Phase 4: wideboi status CLI presentation and integration tests` (`2998f4a`)

## Copilot Review & Follow-up Fixes

1. **Base64 unbounded allocation guard:** Checked `len(valBase64) > base64.StdEncoding.EncodedLen(4096)` before decoding in `vtGrid`.
2. **JSON tags on MsgPaneMetadata:** Added `json:"pane_id"`, `json:"cwd"`, and `json:"user_vars"` on `MsgPaneMetadata` to ensure snake_case fields match the documented schema. Added test assertions in `status_test.go`.
3. **Prune closed pane metadata in web app:** Pruned `paneMetadata` against the active column IDs upon receiving `layoutSnapshot`.
4. **Reject control characters in OSC 7 paths:** Added check for ASCII control characters (`< 0x20` or `0x7f`) in cleaned CWD paths to prevent terminal-control or tabwriter injection.
5. **Strict error handling on status metadata collection:** In `status.go`, return an error instead of partial output if connection closes or times out before metadata for all snapshot columns is collected.
6. **Serialize server metadata sends:** Added `metaSendMu` in `server.go` to serialize `broadcastMetadataIfChanged` and `sendPaneMetadataTo`.
7. **Clean test server shutdown:** Added `MsgShutdown` delivery in `osc_status_test.go` teardown.
