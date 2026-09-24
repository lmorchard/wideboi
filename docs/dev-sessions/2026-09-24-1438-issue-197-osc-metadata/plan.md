# Track Pane CWD and Agent Metadata via OSC 7 and OSC 1337 Implementation Plan

**Goal:** Track pane working directories via OSC 7 and agent key-value metadata via OSC 1337 `SetUserVar`, delivering them over the wire via a dedicated `MsgPaneMetadata` message to `wideboi status` and attached clients.

**Approach:** 
Register OSC 7 and OSC 1337 handlers on `SafeEmulator` with strict URI and base64 validation and limits.
Introduce `MsgPaneMetadata` to the wire protocol and bump `protocol.Version` to 3.
Update the server to broadcast metadata updates and reply to status requests with layout and metadata.
Expose CWD in `wideboi status` table output and CWD/user variables in unified JSON output.

**Tech stack:** Go, Protobuf / `wirepb`, `x/vt`, TypeScript (`web/src`).

---

## Phase 1: Terminal Parser & Grid Support for OSC 7 and OSC 1337

Add OSC 7 and OSC 1337 sequence handlers to `vtGrid` and expose CWD and UserVars on `term.Grid` and `server.Pane`.

**Files:**
- Modify: `internal/server/term/grid.go` — add `CWD() string` and `UserVars() map[string]string` to `Grid` interface; register OSC 7 and OSC 1337 handlers; add validation and storage
- Modify: `internal/server/pane.go` — add `CWD()` and `UserVars()` pass-throughs
- Test: `internal/server/term/osc_metadata_test.go` — unit tests for OSC 7 and OSC 1337 parsing, validation, limits, and cleanup

**Key changes:**
- `Grid.CWD() string`
- `Grid.UserVars() map[string]string`
- Handlers in `vtGrid`:
  - OSC 7:
    - Split payload `7;url`.
    - Parse URL with `url.Parse`. Scheme must be `"file"`.
    - Hostname must be empty, `"localhost"`, or match `os.Hostname()`. Foreign hostnames rejected.
    - Path must be non-empty, start with `/`, and be unescaped.
    - Store in `g.cwd atomic.Pointer[string]`.
  - OSC 1337:
    - Split payload `1337;SetUserVar=<name>=<base64-value>`.
    - Validate `<name>`: length 1..64, matching `^[a-zA-Z0-9_-]+$`.
    - Decode base64 value with `base64.StdEncoding.DecodeString`.
    - Validate decoded string is valid UTF-8 and <= 4096 bytes.
    - If decoded value is empty, delete key from `g.userVars`.
    - If non-empty, store in `g.userVars` (up to 64 keys maximum; if at limit and key is new, drop).

**Verification — automated:**
- [x] `go test -v ./internal/server/term -run TestOSC` — **passed all tests including TestOSC7TracksCWD, TestOSC7RejectsInvalidURIs, TestOSC1337SetUserVar, TestOSC1337SetUserVarValidationAndLimits**
- [x] `make test` — **passed (go test ./...)**

**Verification — manual:**
- [x] Verify OSC 7 and OSC 1337 unit tests cover missing, valid, invalid, and oversized payloads. — **verified in osc_metadata_test.go**

---

## Phase 2: Wire Protocol Schema, Codec, Version Bump, and Web Client

Add `MsgPaneMetadata` to Protobuf and Go/TS codecs, bump protocol version to 3, and regenerate bindings.

**Files:**
- Modify: `internal/protocol/wirepb/wideboi.proto` — add `MsgPaneMetadata` message and field in `ServerMessage`
- Modify: `internal/protocol/messages.go` — add `MsgPaneMetadata` Go struct
- Modify: `internal/protocol/codec.go` — add marshal/unmarshal for `MsgPaneMetadata`
- Modify: `internal/protocol/version.go` — bump `Version` to 3
- Modify: `web/src/client.ts` — bump `versionProtocol` to `"wideboi.v3"`
- Generate: `buf generate` via `make proto`
- Test: `internal/protocol/codec_test.go` — update wire type and schema tests

**Key changes:**
```protobuf
message MsgPaneMetadata {
  int32 pane_id = 1;
  string cwd = 2;
  map<string, string> user_vars = 3;
}
```
In `ServerMessage`:
```protobuf
  oneof msg {
    ...
    MsgPaneMetadata pane_metadata = 6;
  }
```
In `internal/protocol/version.go`:
```go
const Version uint32 = 3
```
In `web/src/client.ts`:
```ts
const versionProtocol = "wideboi.v3";
```

**Verification — automated:**
- [x] `make proto` succeeds — **regenerated Go and TS wire bindings**
- [x] `git diff --exit-code -- internal/protocol/wirepb web/src/gen` passes — **verified via make proto-check**
- [x] `go test -v ./internal/protocol` — **all passed (TestCodecRoundTripsEveryField, TestWireSchemaCoversEveryWireType, TestWireTypesCarryNoInterfaces, etc.)**
- [x] `make web-test` — **all 7 test files, 23 tests passed**
- [x] `make lint` — **go vet ./... passed**

**Verification — manual:**
- [x] Check generated TypeScript and Go files match schema. — **verified MsgPaneMetadata in wideboi.pb.go and wideboi_pb.ts**

---

## Phase 3: Server Metadata Tracking, Broadcast Loop, and Client Storage

Track pane metadata in `Server`, broadcast `MsgPaneMetadata` on changes and to new connections / status requests, and handle `MsgPaneMetadata` in terminal and web clients.

**Files:**
- Modify: `internal/server/server.go` — track metadata state per pane; broadcast changes on 33ms ticker; send metadata on `MsgStatusRequest` and initial client attach
- Modify: `internal/client/client.go` — receive `MsgPaneMetadata` in `HandleServerMsg` and store in client pane state
- Modify: `web/src/wideboi-app.ts` — receive `paneMetadata` and store in application state
- Test: `internal/server/metadata_test.go` — test server broadcasts metadata when pane CWD/vars change and on `MsgStatusRequest`

**Key changes:**
- Server tracks `lastCWD map[int]string` and `lastUserVars map[int]map[string]string`.
- On 33ms ticker: if any pane's CWD or UserVars differ from recorded state, broadcast `MsgPaneMetadata` for that pane to all clients.
- In `handleClientMsg` for `MsgStatusRequest`: after layout snapshot, send `MsgPaneMetadata` for each active pane to the requesting transport.
- In `internal/client/client.go`: store `c.paneMetadata map[int]protocol.MsgPaneMetadata`.
- In `web/src/wideboi-app.ts`: store metadata map in app state.

**Verification — automated:**
- [x] `go test -v ./internal/server -run TestMetadata` — **passed TestMetadataChangeBroadcastsMsgPaneMetadata and TestMsgStatusRequestSendsPaneMetadata**
- [x] `make test` — **all packages passed**
- [x] `make web-test` — **all 7 test files, 23 tests passed**

**Verification — manual:**
- [x] Verify no extraneous re-renders or unneeded layout recalculations on metadata updates. — **metadata uses MsgPaneMetadata without triggering broadcastLayout**

---

## Phase 4: `wideboi status` CLI Presentation and Integration Tests

Integrate metadata into `wideboi status` (human-readable table and `--json`), with end-to-end PTY integration tests.

**Files:**
- Modify: `cmd/wideboi/status.go` — collect `MsgPaneMetadata` along with `MsgLayoutSnapshot`; print `CWD` column in table; print unified JSON in `--json` mode
- Test: `cmd/wideboi/status_test.go` — test table formatting with CWD and `--json` format with metadata
- Test: `cmd/wideboi/osc_status_test.go` — end-to-end test with real PTY child emitting OSC 7 and OSC 1337 and querying status

**Key changes:**
- `status.go`:
  - Collects `MsgPaneMetadata` from `ServerSendChan` up to timeout or until all columns have metadata.
  - Table: `PANE ID\tWIDTH\tHEIGHT\tSTATUS\tTITLE\tCWD\n` (displays `-` if empty).
  - `--json`: encodes `StatusOutput` containing columns, pane_statuses, pane_titles, and pane_metadata.
- Documentation: update any relevant command docs or help strings.

**Verification — automated:**
- [x] `go test -v ./cmd/wideboi -run TestRunStatus` — **passed TestRunStatus and TestRunStatusJSON**
- [x] `go test -v ./cmd/wideboi -run TestOSC` — **passed TestEndToEndPTYMetadataReporting**
- [x] `make check` — **all gates passed (fmt-check, lint, seam-check, test, web-test, web-accept, race, verify-exit, smoke 37/37, golden, attach-check 25/25)**

**Verification — manual:**
- [x] Inspect `wideboi status` output table and `--json` against running server emitting OSC 7/1337. — **verified in TestEndToEndPTYMetadataReporting**
