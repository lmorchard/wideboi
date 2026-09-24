# Track pane CWD and agent metadata via OSC 7 and OSC 1337 Spec

**Goal:** Track pane working directories via OSC 7 and agent key-value metadata via OSC 1337 `SetUserVar`, exposing them through a separate wire message (`MsgPaneMetadata`) to `wideboi status` (table and `--json`) and attached clients.

**Source:** https://github.com/lmorchard/wideboi/issues/197

## Current state

- **PTY Input & Terminal Grid:** Pane PTY reads pass to `term.Grid.Write()` in `internal/server/pane.go:114-118`. Bytes pass through `oscScanner` (`internal/server/term/oscfix.go:52-138`) to `vt.SafeEmulator` (`internal/server/term/grid.go:140`).
- **OSC Handlers:** Currently `internal/server/term/grid.go:235-323` registers OSC handlers for 133 (prompt/status) and 9;4 (progress). OSC 0/1/2 titles are handled via `SafeEmulator.Title` callback (`grid.go:227`).
- **Wire Protocol & Codec:** Messages are defined in `internal/protocol/wirepb/wideboi.proto` and mirrored in `internal/protocol/messages.go`. Go serialization is in `internal/protocol/codec.go`, and TypeScript generation is in `web/src/gen/internal/protocol/wirepb/wideboi_pb.ts`. Handshake verifies `protocol.Version = 2` (`internal/protocol/version.go:18`, `internal/transport/handshake.go:14-23`, `web/src/client.ts:32-44`).
- **Status Reporting:** `wideboi status` sends `MsgStatusRequest` over UNIX socket and prints `MsgLayoutSnapshot` as a table or raw JSON (`cmd/wideboi/status.go:18-64`).

## Desired end state

1. **OSC 7 CWD Tracking:**
   - Supported sequence: `ESC ] 7 ; file://[hostname]/path ST|BEL`.
   - Hostname check: must be empty, `"localhost"`, or match local machine's `os.Hostname()`. Sequences with foreign hostnames are ignored.
   - Scheme must be `file`. Path must be percent-decoded, non-empty, and absolute.
   - Per-pane CWD stored in `vtGrid` and queryable via `term.Grid.CWD() string`.

2. **OSC 1337 SetUserVar Metadata:**
   - Supported sequence: `ESC ] 1337 ; SetUserVar=<name>=<base64-value> ST|BEL`.
   - Key names: `^[a-zA-Z0-9_-]+$`, max 64 bytes.
   - Value: standard base64 encoded UTF-8 string. Max decoded size 4096 bytes (4KB). Max 64 variables per pane.
   - Removal semantics: if decoded value is empty (`""`), the variable is deleted from the pane's metadata.
   - Malformed base64, invalid UTF-8, keys exceeding 64 bytes, values exceeding 4KB, or keys beyond the 64-variable limit are dropped silently without crashing, erroring, or corrupting state.
   - Per-pane user variables stored in `vtGrid` and queryable via `term.Grid.UserVars() map[string]string`.

3. **Wire Protocol Delivery:**
   - New wire message: `MsgPaneMetadata`:
     ```protobuf
     message MsgPaneMetadata {
       int32 pane_id = 1;
       string cwd = 2;
       map<string, string> user_vars = 3;
     }
     ```
   - Added as oneof field `pane_metadata = 6` in `ServerMessage`.
   - Protobuf generation updated for Go and TypeScript (`make proto`).
   - `protocol.Version` bumped from `2` to `3` in `internal/protocol/version.go:18` and `web/src/client.ts:32` (`wideboi.v3`).
   - Server broadcasts `MsgPaneMetadata` to attached clients when a pane's CWD or UserVars change (detected during the 33ms server run loop).
   - On `MsgStatusRequest` and initial client attach, server sends `MsgPaneMetadata` for all active panes alongside `MsgLayoutSnapshot`.

4. **`wideboi status` CLI Presentation:**
   - **Table mode:** Add a `CWD` column:
     `PANE ID  WIDTH  HEIGHT  STATUS  TITLE  CWD`
     If CWD is unset/empty, renders `-`.
   - **JSON mode (`--json`):** Encodes a unified JSON status object:
     ```json
     {
       "columns": [...],
       "pane_statuses": {...},
       "pane_titles": {...},
       "pane_metadata": {
         "1": {
           "pane_id": 1,
           "cwd": "/path/to/dir",
           "user_vars": { "key": "value" }
         }
       }
     }
     ```
   - Client and web apps handle `MsgPaneMetadata` safely and update internal pane metadata store without injecting arbitrary strings into chrome/UI.

## Design decisions

- **Decision:** Deliver metadata via a separate `MsgPaneMetadata` message rather than stuffing it into `MsgLayoutSnapshot`.
  - **Why:** Keeps `MsgLayoutSnapshot` focused on physical grid layout/geometry, titles, and status glyphs. Allows targeted broadcasts when metadata changes without forcing client layout recalculations.
  - **Rejected:** Merging CWD and user variables into `MsgLayoutSnapshot`.

- **Decision:** Bump `protocol.Version` to 3.
  - **Why:** Mandated by `docs/LESSONS.md`: "Bump protocol.Version when the wire changes... Increase it for any change an older peer would misread or reject."
  - **Rejected:** Leaving version at 2 and hoping mismatched clients/servers handle unknown oneof fields gracefully.

- **Decision:** Accept OSC 7 `file://` URIs only when hostname is empty, `"localhost"`, or matches local `os.Hostname()`.
  - **Why:** Prevents remote shell sessions over SSH or container panes from reporting paths that do not exist or are misleading on the local host.
  - **Rejected:** Blindly accepting any hostname in OSC 7.

- **Decision:** Deleting user variables on empty decoded value in OSC 1337 `SetUserVar`.
  - **Why:** Matches iTerm2 `SetUserVar` convention where clearing a variable unsets it, avoiding memory leaks of stale agent keys.
  - **Rejected:** Retaining keys with empty string values indefinitely.

- **Decision:** Enforce 64-byte key limit, 4KB value limit, 64-variable count limit, and valid UTF-8.
  - **Why:** Prevents memory exhaustion or DoS attacks from rogue terminal programs, and protects the protobuf wire encoder from invalid UTF-8 crashes (#175).
  - **Rejected:** Unbounded string or map storage.

## Patterns to follow

- OSC Registration in `internal/server/term/grid.go:235-323`: registration on `g.em.RegisterOscHandler(num, ...)`, payload splitting on `;`.
- Broadcast loop in `internal/server/server.go:306-322, 736-757`: 33ms check comparing cached snapshots/state against current values, broadcasting to transports.
- Wire messages in `internal/protocol/wirepb/wideboi.proto`, `internal/protocol/messages.go`, and `internal/protocol/codec.go:79-170`.
- Socket handshake in `internal/transport/handshake.go:14-23` and `web/src/client.ts:32-44`.
- Status command in `cmd/wideboi/status.go:18-64`.

## What we're NOT doing

- We are NOT rendering arbitrary user variables in terminal chrome or the tab bar.
- We are NOT implementing other OSC 1337 subcommands (e.g., inline images, badges, notifications, file downloads).
- We are NOT implementing in-app visual dashboard UI in this change (issue 197 explicitly states: "Follow-up to #133. This provides data for the separate in-app dashboard issue but does not require that UI to land first").
- We are NOT modifying existing layout, column-sizing, or reflow algorithms.

## Open questions

*(None. All design questions were decided during brainstorm.)*
