# Protocol Buffers wire format Implementation Plan

**Goal:** Carry every client/server message as protobuf on both transports,
with Go keeping its structs behind a tested codec and the browser using
generated types throughout.

**Approach:** One schema (`internal/protocol/wirepb/wideboi.proto`) with
`ClientMessage`/`ServerMessage` oneofs. `internal/protocol/codec.go` converts
Go structs ↔ generated types at the transport edge, guarded by an exhaustive
reflection round-trip test and enum pins. Socket gets length-prefixed frames;
WebSocket gets binary messages; the web client drops its JSON envelope and
hand-written wire types.

**Tech stack:** `google.golang.org/protobuf` v1.36.12, `buf` 1.73 (installed at
/opt/homebrew/bin/buf), `@bufbuild/protobuf` / `@bufbuild/protoc-gen-es` ^2.15.0,
vitest.

**Sources to reuse:** PR #144's branch `origin/issue-127-codex` — take files
with `git show origin/issue-127-codex:<path>` and adapt; do not merge or
cherry-pick commits.

---

## Phase 1: Schema, codegen, and a guarded Go codec

Delivers the schema, generated Go + TS bindings, and `codec.go`, fully tested
in isolation. No transport uses it yet. Also wires web tests into `make check`.

**Files:**
- Create: `buf.yaml` — from #144 (v1, STANDARD lint minus
  PACKAGE_DIRECTORY_MATCH, PACKAGE_VERSION_SUFFIX, ENUM_ZERO_VALUE_SUFFIX).
- Create: `buf.gen.yaml` — v2, Go plugin run from go.mod (no install needed):
  ```yaml
  version: v2
  plugins:
    - local: ["go", "run", "google.golang.org/protobuf/cmd/protoc-gen-go"]
      out: .
      opt: paths=source_relative
    - local: web/node_modules/.bin/protoc-gen-es
      out: web/src/gen
      opt: target=ts
  ```
- Create: `internal/protocol/wirepb/wideboi.proto` — start from #144's, then apply the delta below.
- Create (generated): `internal/protocol/wirepb/wideboi.pb.go`, `web/src/gen/internal/protocol/wirepb/wideboi_pb.ts`.
- Create: `internal/protocol/codec.go` — start from #144's, apply the delta below.
- Create: `internal/protocol/codec_test.go` — exhaustive, oneof-coverage, enum, absence and size tests.
- Modify: `go.mod`/`go.sum` — `google.golang.org/protobuf v1.36.12` (direct).
- Modify: `web/package.json` (+ lock) — `@bufbuild/protobuf ^2.15.0` dep, `@bufbuild/protoc-gen-es ^2.15.0` devDep.
- Modify: `Makefile` — `proto` target (`buf generate`); `web-test: web/dist` → `cd web && npm test`; add `web-test` to `check-targets`; both in `.PHONY`.

**Schema delta vs #144's proto** (what #163/#165 changed):
```proto
// On the wire, absence means zero, never "unchanged": proto3 omits zero
// scalars and empty sub-messages. Patches stay row-granular for that
// reason. A field-level delta would need explicit presence (optional).

enum PaneStatus {
  PANE_STATUS_IDLE = 0;
  PANE_STATUS_WORKING = 1;
  PANE_STATUS_NEEDS_INPUT = 2;
  PANE_STATUS_DONE = 3;
  PANE_STATUS_FAILED = 4;
}

message MsgPaneUpdate {        // + generation
  int32 pane_id = 1;
  uint64 generation = 2 [jstype = JS_NUMBER];  // [!] option dropped, see notes.md
  int32 cols = 3;
  int32 rows = 4;
  repeated LineData lines = 5;
  int32 cursor_x = 6;
  int32 cursor_y = 7;
  bool cursor_visible = 8;
  bool mouse_tracking = 9;
}

message PaneRow {
  int32 y = 1;
  repeated CellData cells = 2;
}

message MsgPanePatch {
  int32 pane_id = 1;
  int32 cols = 2;
  int32 rows = 3;
  uint64 base_generation = 4 [jstype = JS_NUMBER];
  uint64 generation = 5 [jstype = JS_NUMBER];
  repeated PaneRow changed_rows = 6;
  int32 cursor_x = 7;
  int32 cursor_y = 8;
  bool cursor_visible = 9;
  bool mouse_tracking = 10;
}

message MsgLayoutSnapshot {    // - focus_pane_id, statuses now an enum
  repeated ColumnData columns = 1;
  map<int32, PaneStatus> pane_statuses = 2;
  map<int32, string> pane_titles = 3;
}

message MsgPaneCreated { int32 pane_id = 1; }
message MsgPaneResync { int32 pane_id = 1; }
message MsgVerb { VerbType verb = 1; int32 pane_id = 2; }   // + pane_id
// MsgFocusPane removed.

message ServerMessage {
  oneof msg {
    MsgPaneUpdate pane_update = 1;
    MsgPanePatch pane_patch = 2;
    MsgLayoutSnapshot layout_snapshot = 3;
    MsgPaneCreated pane_created = 4;
    MsgPaneClosed pane_closed = 5;
  }
}

message ClientMessage {
  oneof msg {
    MsgAttach attach = 1;
    MsgVerb verb = 2;
    MsgMouse mouse = 3;
    MsgInput input = 4;
    MsgResize resize = 5;
    MsgScroll scroll = 6;
    MsgPaneResync pane_resync = 7;
    MsgDetach detach = 8;
    MsgShutdown shutdown = 9;
    MsgStatusRequest status_request = 10;
  }
}
```
Unchanged from #144: VerbType, ColorKind, MouseKind enums; ColorData,
StyleData, CellData, LineData, ColumnData, MsgPaneClosed, MsgAttach, MsgMouse,
KeyData, MsgInput, MsgResize, MsgScroll, MsgDetach, MsgShutdown,
MsgStatusRequest. Field numbers are fresh (spec: no cross-version compat).

**Codec delta vs #144's `codec.go`:**
- `MsgVerb`: carry `PaneID`. Drop the `MsgFocusPane` cases.
- Add `MsgPaneResync` (client), `MsgPaneCreated` and `MsgPanePatch` (server).
- `MsgPaneUpdate`: carry `Generation`.
- `MsgLayoutSnapshot`: no FocusPaneID; statuses map `wirepb.PaneStatus(v)` ↔ `PaneStatus(v)`.
- Factor the row conversion so update and patch share it:
  ```go
  func encodeLine(line LineData) []*wirepb.CellData
  func decodeLine(cells []*wirepb.CellData) LineData
  ```
  Keep #144's rule: zero `StyleData`/`ColorData`/`KeyData` encode as nil and decode back to zero.

**Tests (`internal/protocol/codec_test.go`), written first:**

1. `TestCodecRoundTripsEveryField` — for each value in `wireTypes`
   (wire_test.go:11-27), build a fully populated instance with `fill`, round-trip
   it, and require `reflect.DeepEqual`. A field the codec forgets fails.
   ```go
   // fill sets every exported field reachable from v to a distinct non-zero
   // value, so a field the codec drops cannot compare equal after a round trip.
   func fill(v reflect.Value, n *int) {
       *n++
       switch v.Kind() {
       case reflect.Struct:
           for i := 0; i < v.NumField(); i++ {
               if v.Type().Field(i).IsExported() {
                   fill(v.Field(i), n)
               }
           }
       case reflect.Slice:
           if v.Type().Elem().Kind() == reflect.Uint8 { // []byte
               v.SetBytes([]byte{byte(*n), 0xff})
               return
           }
           s := reflect.MakeSlice(v.Type(), 2, 2)
           fill(s.Index(0), n)
           fill(s.Index(1), n)
           v.Set(s)
       case reflect.Map:
           m := reflect.MakeMap(v.Type())
           k, e := reflect.New(v.Type().Key()).Elem(), reflect.New(v.Type().Elem()).Elem()
           fill(k, n)
           fill(e, n)
           m.SetMapIndex(k, e)
           v.Set(m)
       case reflect.String:
           v.SetString(fmt.Sprintf("s%d", *n))
       case reflect.Bool:
           v.SetBool(true)
       case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
           v.SetInt(int64(*n % 100 + 1))
       case reflect.Uint8, reflect.Uint16, reflect.Uint32:
           v.SetUint(uint64(*n % 200 + 1))
       case reflect.Uint64:
           v.SetUint(1<<40 + uint64(*n)) // beyond 32 bits: generation must not truncate
       default:
           panic(fmt.Sprintf("fill: unhandled kind %s at %s", v.Kind(), v.Type()))
       }
   }
   ```
   Round-trip helper: `MarshalClient` → `UnmarshalClient`; if `MarshalClient`
   errors, `MarshalServer` → `UnmarshalServer`. Also fail if both marshal
   calls error (a type the codec doesn't know).
2. `TestWireSchemaCoversEveryWireType` — the oneof field counts of
   `ClientMessage` + `ServerMessage` (via `(&wirepb.ClientMessage{}).ProtoReflect().Descriptor().Oneofs().ByName("msg").Fields().Len()`)
   equal `len(wireTypes)`. Ties the schema to the Go type list.
3. `TestEnumsMatchWireSchema` — #144's table test (codec_test.go on
   `origin/issue-127-codex`) plus a `PaneStatus` table (Idle..Failed ↔
   `PANE_STATUS_*`, count 5).
4. `TestPatchHidingTheCursorDecodesAsHidden` — a `MsgPanePatch` with
   `CursorVisible:false`, `MouseTracking:false`, no rows, round-tripped: the
   decoded values are false and `ChangedRows` is empty; then `ApplyPanePatch`
   onto a base with `CursorVisible:true` gives `CursorVisible:false`.
5. `TestPaneUpdateProtobufIsSmallerThanJSON` — #144's size test (80×24 blank
   grid, protobuf ×3 < JSON), moved into this package.

**Verification — automated:**
- [x] Tests 1-5 written before `codec.go` exists; `go test ./internal/protocol` fails to compile for the expected reason (undefined `MarshalClient`) — **undefined: MarshalClient/UnmarshalClient/MarshalServer/UnmarshalServer**
- [x] `make proto` regenerates with no diff on a second run — **identical md5 across runs**
- [x] `buf lint` passes — **clean**
- [x] `go test ./internal/protocol -run 'Codec|WireSchema|Enums|Hiding|Smaller' -v` passes — **5/5, 15 subtests in the exhaustive test**
- [x] Deliberately drop one field from `codec.go` (e.g. `Generation` on patch): test 1 fails naming the type; restore — **patch Generation → FAIL MsgPanePatch; cell Width → FAIL MsgPaneUpdate + MsgPanePatch; dropping MsgPaneResync from wireTypes → coverage test FAIL (14 vs 15); all restored**
- [x] `make quick` passes — **ok**
- [x] `make check` passes (now includes `web-test`) — **exit 0; vitest 14 passed; 36 + 24 pty checks passed**

**Verification — manual:**
- [x] Skim `wideboi.proto` against `messages.go`: names and directions read right — **Les: proceed to phase 2**

---

## Phase 2: Unix socket carries length-prefixed protobuf

Replaces gob on the socket. `wideboi attach` / `status` / hang-up now use
protobuf end to end.

**Files:**
- Create: `internal/transport/frame.go` — from #144, but write header+payload in one buffer:
  ```go
  func writeFrame(w io.Writer, data []byte) error {
      if len(data) > maxFrameSize {
          return fmt.Errorf("protobuf frame too large: %d", len(data))
      }
      buf := make([]byte, 4+len(data))
      binary.BigEndian.PutUint32(buf, uint32(len(data)))
      copy(buf[4:], data)
      for len(buf) > 0 {
          n, err := w.Write(buf)
          if err != nil {
              return err
          }
          if n == 0 {
              return io.ErrShortWrite
          }
          buf = buf[n:]
      }
      return nil
  }
  ```
  `readFrame` and `maxFrameSize = 64 << 20` as in #144.
- Create: `internal/transport/frame_test.go` — #144's short-write and oversized
  tests, plus `TestTruncatedFrameIsACleanClose` and
  `TestMalformedPayloadIsNotACleanClose` from our fix commit 4ac55bf.
- Modify: `internal/transport/socket.go` — delete the `gob` import and `init()`
  registrations, the `encoder`/`decoder` fields and their construction. The
  write loops call `protocol.MarshalServer`/`MarshalClient` then `writeFrame`,
  recording `"encoding %T to client|server"`. The read loops call `readFrame`
  (record `"reading from client|server"`) then `UnmarshalClient`/`UnmarshalServer`
  (record `"decoding from client|server"`). Update comments that mention gob
  (connErr doc, isCleanClose doc). `isCleanClose` already includes
  `io.ErrUnexpectedEOF` on main — leave it.
- Modify: `internal/transport/wire_test.go` — `roundtrip` uses the codec
  (client first, then server), as in #144.
- Modify: `internal/protocol/wire.go` — header comment: the structs are the
  in-process model; `codec.go` maps them to the wire. Keep the reasoning about
  why they contain no interfaces (TestWireTypesCarryNoInterfaces still
  enforces it), dropping only the gob-specific parts.

**Verification — automated:**
- [x] Frame tests written first and fail (undefined `readFrame`/`writeFrame`) — **undefined: writeFrame/readFrame/maxFrameSize**
- [x] `go test ./internal/transport ./cmd/wideboi` passes (socket_test, status_test, hangup_test, main_test use real sockets) — **ok, ok**
- [x] `grep -rn "encoding/gob" internal cmd` returns nothing — **no matches**
- [x] `make check` passes (attach-check and smoke drive real `wideboi attach` over the socket) — **exit 0; vitest 14; 36 + 24 pty checks**
- [x] `go test -race -count=4 ./internal/transport/` passes (framing sits in the pumps) — **ok ×4, plus cmd/wideboi ×4**

**Verification — manual:**
- [x] `wideboi` in a terminal: coloured prompt, typing, `C-b n`, detach and reattach all work — **Les, 2026-09-24: CLI smoke test from the worktree works**

---

## Phase 3: WebSocket carries binary protobuf; web client uses generated types

Server and browser must switch together, so this is one slice. All #164
behaviour (ping/pong, deadlines, 1 MiB read limit, slow-peer close) stays.

**Files (Go):**
- Modify: `internal/transport/websocket.go` —
  - Delete `WSEnvelope`, `clientTypes`, and the `encoding/json` and `reflect` imports.
  - writeLoop: `payload, err := protocol.MarshalServer(msg)`; on error `set("encoding WebSocket payload", err)` and return; then under `mu` with the existing write deadline, `conn.WriteMessage(websocket.BinaryMessage, payload)`.
  - readLoop: `kind, payload, err := conn.ReadMessage()` (existing error handling unchanged); `kind != websocket.BinaryMessage` → `slog.Warn("WebSocket received non-binary frame", "type", kind)`, continue; `protocol.UnmarshalClient(payload)` error → `slog.Warn("WebSocket failed to decode client message", "err", err)`, continue.
- Modify: `internal/transport/websocket_test.go` — `TestWebSocketRoundTrip` sends `MarshalClient(MsgAttach{80,24})` and `MarshalClient(MsgPaneResync{7})` as binary frames; server messages are read with `ReadMessage`, asserted binary, decoded with `UnmarshalServer`, and type-asserted (`MsgLayoutSnapshot`, `MsgPanePatch` with `BaseGeneration == 1`). Slow-peer test decodes and asserts `MsgLayoutSnapshot`. Add `TestWebSocketSkipsUndecodableFrames`: a text frame and a garbage binary frame are skipped, and a following valid `MsgAttach` still arrives. Oversized-input test unchanged.

**Files (web):**
- Modify: `web/src/client.ts` — keep main's token subprotocol, replaced-socket guards and logging.
  ```ts
  import { create, fromBinary, toBinary, type MessageInitShape } from '@bufbuild/protobuf';
  import { ClientMessageSchema, ServerMessageSchema, type ServerMessage } from './gen/internal/protocol/wirepb/wideboi_pb';

  export type ClientMsg = NonNullable<MessageInitShape<typeof ClientMessageSchema>['msg']>;

  // connect(): ws.binaryType = 'arraybuffer';
  // onmessage: this.onMessage?.(fromBinary(ServerMessageSchema, new Uint8Array(event.data as ArrayBuffer)));
  //   decode errors → console.error('[WideboiClient] Failed to decode message:', err)
  public onMessage?: (message: ServerMessage) => void;
  public send(msg: ClientMsg) {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      console.warn('[WideboiClient] Cannot send, not connected');
      return;
    }
    this.ws.send(toBinary(ClientMessageSchema, create(ClientMessageSchema, { msg })));
  }
  ```
- Modify: `web/src/protocol.ts` — delete ColorData, StyleData, CellData, LineData, ColumnData, MsgLayoutSnapshot, MsgPaneUpdate, MsgPanePatch, MsgPaneClosed, MsgAttach, MsgResize, WSEnvelope. Keep LayoutMode, PlacementKind, Rectangle, PlacementData.
- Modify: `web/src/colors.ts` — `decodeColor(color: ColorData | undefined, isBg)` on the generated type: `switch (color.kind)` with `ColorKind.NONE/BASIC/INDEXED/RGBA`; `color.index`, `r/g/b/a`.
- Modify: `web/src/focus.ts` — generated `ColumnData`, `paneId`.
- Modify: `web/src/renderer.ts` — generated `MsgLayoutSnapshot`, `MsgPaneUpdate`, `MsgPanePatch`; fields `columns[].paneId/width`, `paneTitles`, `paneStatuses`, `lines[y].cells[x]`, `cell.content/width/style?.attrs/fg/bg/underlineColor/underline`, `cursorX/cursorY/cursorVisible/mouseTracking`, `generation`. `style` and colours may be undefined (absence = zero). `handlePanePatch` keeps every check; the new pane is
  ```ts
  const lines = base.lines.slice();
  // per row: lines[row.y] = create(LineDataSchema, { cells: row.cells });
  this.panes.set(patch.paneId, { ...base, lines, generation: patch.generation,
    cursorX: patch.cursorX, cursorY: patch.cursorY,
    cursorVisible: patch.cursorVisible, mouseTracking: patch.mouseTracking });
  ```
- Modify: `web/src/input.ts` — `InputSender { send(msg: ClientMsg): void }`. Keyboard: `send({ case: 'input', value: { paneId, key: { text, mod, code: base, shiftedCode: 0, baseCode: base, isRepeat } } })`. Text: `send({ case: 'input', value: { paneId, data: new TextEncoder().encode(text) } })` (raw bytes; base64 existed only for JSON).
- Modify: `web/src/wideboi-app.ts` —
  - `onMessage(message)` → `switch (message.msg.case)`: `layoutSnapshot`, `paneCreated`, `paneUpdate`, `panePatch` (on false: `client.send({ case: 'paneResync', value: { paneId } })`), `paneClosed`. Same behaviour as today's if/else.
  - `paneStatuses: Record<number, PaneStatus>`; SmartJump rank uses `PaneStatus.FAILED/DONE/NEEDS_INPUT`.
  - Prefix verbs use `VerbType.*`; local verbs compare against `VerbType.FOCUS_LEFT/FOCUS_RIGHT/SMART_JUMP/FOCUS_LAST`; repeatable set likewise.
  - Sends: `{ case: 'resize' | 'attach', value: { cols, rows } }`, `{ case: 'verb', value: { verb, paneId } }`, `{ case: 'scroll', value: { paneId, delta } }`, `{ case: 'mouse', value: { paneId, kind, x, y, button, mod } }`; `sendPointerMouse(kind: MouseKind, …)` called with `MouseKind.PRESS/MOTION/RELEASE`.
- Modify tests: `renderer.test.ts` (literals via `create(...Schema, {...})`, lowercase fields; `cell()` has no style, so the patch test also exercises absent style), `input.test.ts` (typed `ClientMsg` expectations; text input `data` equals `new TextEncoder().encode('é界')`), `lifecycle.test.ts` (the stale-socket message is `toBinary(ServerMessageSchema, …paneClosed…).buffer`), `focus.test.ts` (generated `ColumnData`). `client.test.ts` unchanged.
- Create: `web/src/protobuf.test.ts` — #144's Go-compat byte fixtures, regenerated for the new field numbers from Go: add a Go test helper that prints the hex for `MarshalClient(MsgAttach{80,24})` and a styled one-cell `MsgPaneUpdate`, paste into the TS test, then delete the helper. Plus: a patch with `cursorVisible` absent, fed to `handlePanePatch` over a base with `cursorVisible: true`, leaves the pane's cursor hidden.

**Verification — automated:**
- [x] `TestWebSocketSkipsUndecodableFrames` and the updated web tests are written first and fail — **Go: 3 WS tests failed (text frames, JSON attach accepted); web: 11 failed on old field names / tuple send / binaryType**
- [x] `go test ./internal/transport ./internal/server` passes — **ok, ok**
- [x] `npm run lint --prefix web` (tsc) passes; no `any` remains on message paths (`grep -n "env\.\|: any" web/src/*.ts`) — **tsc clean; grep: no matches outside tests**
- [x] `npm test --prefix web` passes — **17 passed**
- [x] `grep -rn "WSEnvelope\|clientTypes\|JSON.parse" internal web/src --include=*.go --include=*.ts` returns nothing (excluding gen/) — **no matches**
- [x] `make check` passes — **exit 0 (second run; first caught a fixture type error vitest misses, see notes)**
- [x] `go test -race -count=4 ./internal/transport/` passes — **ok ×4, plus internal/server ×4**

**Verification — manual (Les):**
- [x] `wideboi server --websocket :8080`, open the web client with the token: colours and bold/underline render, typing and paste (incl. `é界`) reach the pane, mouse selection and click-to-focus work — **Les, 2026-09-24: web UI smoke test from another machine works**
- [x] `C-b n`, `C-b h/l`, `C-b a`, `C-b x` behave as before; a busy pane (e.g. `top`) updates smoothly (patches), and nothing stays stale — **Les, 2026-09-24: web UI smoke test from another machine works**

---

## Phase 4: Docs

**Files:**
- Modify: `README.md` — "Wire protocol" section (from #144, placed before License):
  both transports use protobuf; socket frames are 4-byte big-endian
  length-prefixed; `internal/protocol/codec.go` maps Go structs to the
  schema; regenerate with `make proto` after `npm ci --prefix web` (buf
  required; protoc-gen-go runs from go.mod).
- Modify: `docs/LESSONS.md` — one entry: on the wire, absence means zero; a
  field-level delta needs `optional` (explicit presence); and a new Go message
  field must be added to the codec, which `TestCodecRoundTripsEveryField` enforces.

**Verification — automated:**
- [x] `make check` passes — **exit 0; vitest 17; 36 + 24 pty checks**

**Verification — manual:**
- [x] README instructions regenerate cleanly from a fresh clone (`npm ci --prefix web && make proto` → no diff) — **verified by agent: clone to /tmp, npm ci, make proto → clean git status**

---

## Self-review

- **Spec coverage:** schema/oneofs (P1), generated code committed + `make proto` (P1), codec + unknown-type errors (P1), exhaustive test (P1.1, P1.2), enum pins incl. PaneStatus (P1.3), JS_NUMBER generations (P1 schema), absence-means-zero comment + tests (P1 schema, P1.4, P3 protobuf.test), socket frames 64 MiB + single write (P2), truncation/malformed tests (P2), WebSocket binary + #164 kept + warn-and-skip (P3), StatusRequest accepted over WS (P1 codec accepts all client types; P3 no allowlist), typed web throughout (P3), `web-test` in check (P1), README/LESSONS (P4). PR #144 closure is a `pr`-phase step.
- **Not in plan, per spec:** generated structs in Go, InProcChannel, `status --json`, MsgPaneClosed producer, versioning, handshake changes, renderer behaviour.
- **Names used consistently:** `MarshalClient/UnmarshalClient/MarshalServer/UnmarshalServer`, `encodeLine/decodeLine`, `writeFrame/readFrame/maxFrameSize`, `ClientMsg`, `wireTypes`, `fill`.
