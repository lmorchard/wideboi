# Research: client/server wire protocol (at main 0af1782)

Documentarian findings, gathered before the spec. Paths relative to repo root.

## 1. Message inventory (`internal/protocol/messages.go`, `wire.go`, `pane_patch.go`)

### Client→server
| Type | Fields | Senders | Receiver |
|---|---|---|---|
| `MsgAttach` messages.go:150 | `Cols, Rows int` | client.go:131, main.go:677; web wideboi-app.ts:476 | server.go:315 |
| `MsgStatusRequest` messages.go:245 | — | cmd/wideboi/status.go:30 | server.go:312; status.go:38 expects `MsgLayoutSnapshot` back |
| `MsgVerb` messages.go:156 | `Verb VerbType`, `PaneID int` | client.go:1042 (non-local verbs); web wideboi-app.ts:342 | server.go:354-391 |
| `MsgMouse` messages.go:177 | `PaneID, Kind MouseKind, X, Y, Button, Mod int` | mouse.go:124,225; web wideboi-app.ts:459 | server.go:402 |
| `MsgInput` messages.go:190 | `PaneID int, Key KeyData, Data []byte` | client.go:1100,1111; web input.ts:23,40 | server.go:393 |
| `MsgResize` messages.go:197 | `Cols, Rows int` | client.go:1140; web wideboi-app.ts:168 | server.go:346 |
| `MsgScroll` messages.go:203 | `PaneID, Delta int` | client.go:1122; mouse.go:216/218; web wideboi-app.ts:354/356/448 | server.go:407 |
| `MsgPaneResync` messages.go:118 | `PaneID int` | client.go:264; web wideboi-app.ts:278 | server.go:310 → 418-428 |
| `MsgDetach` messages.go:211 | — | main.go:719 | server.go:216 |
| `MsgShutdown` messages.go:217 | — | main.go:430,559,738 | server.go:208 |

### Server→client
| Type | Fields | Sender | Receivers |
|---|---|---|---|
| `MsgLayoutSnapshot` messages.go:249 | `Columns []ColumnData{PaneID,Width,Height}`, `PaneStatuses map[int]PaneStatus`, `PaneTitles map[int]string` | server.go:739-779 | client.go:142; status.go:38; web wideboi-app.ts:261 |
| `MsgPaneCreated` messages.go:259 | `PaneID int` | server.go:764 (sent before the snapshot) | client.go:140; web wideboi-app.ts:272 |
| `MsgPaneUpdate` messages.go:80 | `PaneID int, Generation uint64, Cols, Rows int, Lines []LineData, CursorX, CursorY int, CursorVisible, MouseTracking bool` | server.go:879/885 | client.go:249; web wideboi-app.ts:274 |
| `MsgPanePatch` messages.go:105 | `PaneID, Cols, Rows int, BaseGeneration, Generation uint64, ChangedRows []PaneRow{Y int; Cells LineData}, CursorX, CursorY int, CursorVisible, MouseTracking bool` | server.go:880-883 | client.go:252; web wideboi-app.ts:276 |
| `MsgPaneClosed` messages.go:264 | `PaneID, ExitCode int` | **no production sender** (tests only) | web wideboi-app.ts:280; no Go client case |

### Nested (wire.go)
`CellData{Content string, Width int, Style StyleData}`; `LineData []CellData` (one entry per column, wide glyph continuation at next index); `StyleData{Fg,Bg,UnderlineColor ColorData; Underline, Attrs uint8}`; `ColorData{Kind ColorKind(uint8); Index, R,G,B,A uint8}` (0 None, 1 Basic, 2 Indexed, 3 RGBA); `KeyData{Text string; Mod int; Code, ShiftedCode, BaseCode rune; IsRepeat bool}`.

Not on the wire: `PlacementData`/`PlacementKind`, `LayoutMode`.

### Numeric semantics
- VerbType 1..12 (ToggleCards=7 reserved); web hardcodes numbers wideboi-app.ts:315-325.
- PaneStatus Idle=0..Failed=4 (messages.go:33-39); web SmartJump ranks by number (wideboi-app.ts:334); status.go:63-76 names them.
- Generation uint64 change counter, only inequality meaningful (term/grid.go:96-106).
- MouseKind Press=0..Wheel=3; Mod is a uv KeyMod bitfield; KeyData.Code named keys at 0x110000+n (web input.ts:1-10).

## 2. Go transports

- `ClientMessage`/`ServerMessage` are `interface{}` (inproc.go:9,12). InProcChannel passes values unencoded.
- **socket.go**: gob, `init()` registers all 18 types, no direction filtering (socket.go:69-88). Persistent Encoder/Decoder per conn. Errors → `connErr.set` → pump returns. `isCleanClose` (socket.go:55-67) includes EOF, ErrUnexpectedEOF, net.ErrClosed, EPIPE, ECONNRESET, context.Canceled. `Err()` consumed at main.go:635 and hangup.go:42. ServerSocketConn writeLoop `defer sc.Close()`, `closed` channel; no deadlines/ping/limit.
- **websocket.go**: constants read limit 1 MiB, write timeout 10s, pong 90s, ping 30s (websocket.go:16-21). JSON `WSEnvelope{t,p}` (24-27); outbound type name via reflection, no allowlist (79-108). Inbound `clientTypes` allowlist (113-123) — **MsgStatusRequest not included**; unknown type → `slog.Warn` + skip (148-152). Pumps: ping ticker under `mu` with write deadline (70-78); readLoop sets read limit, read deadline, pong handler (128-132); records only unexpected close errors (142-145). Slow peer: non-blocking enqueue, full queue → Close (177-197). Close under closeOnce (207-214).
- `/ws` endpoint server.go:1054-1110 (token via query or `wideboi-token.<b64url>` subprotocol).

## 3. Web client

- protocol.ts: LayoutMode, PlacementKind, Rectangle, ColumnData, PlacementData, ColorData, StyleData, CellData, LineData, MsgLayoutSnapshot, MsgPaneUpdate, MsgPanePatch (`ChangedRows ... | null`), MsgPaneClosed, MsgAttach, MsgResize, WSEnvelope. No TS types for PaneCreated, Verb, Mouse, Input, Scroll, PaneResync.
- Users: client.ts (WSEnvelope), renderer.ts (Layout/Patch/Update/Placement), colors.ts (ColorData), focus.ts (ColumnData), wideboi-app.ts; tests focus.test.ts, renderer.test.ts.
- client.ts: JSON.parse → onMessage unfiltered (46-56); ignores events from a replaced socket; `send(type,payload)` JSON (59-67); token subprotocol (21-25).
- wideboi-app.ts:258-284 if/else on `env.t`: LayoutSnapshot, PaneCreated, PaneUpdate, PanePatch (resync on false), PaneClosed; unknown ignored.
- Sends: Resize :168, Attach :476, PaneResync :278, Verb :342, Scroll :354/356/448, Mouse :459; input.ts:23 (Key, `Data:''`), input.ts:40 (Data base64 of UTF-8).
- renderer.ts:84-111 `handlePanePatch` mirrors `ApplyPanePatch`; false → resync.

## 4. Tests coupled to wire format

- internal/protocol/wire_test.go: `wireTypes` list (11-27), no-interfaces, exported-fields tests.
- internal/transport/wire_test.go: gob `roundtrip` (20-33), color kinds, key input, every-message-type roundtrip (145-182).
- socket_test.go: unix socket roundtrip (43-121), MsgPaneClosed filler (128-155).
- websocket_test.go: raw JSON envelope roundtrip incl. `"BaseGeneration":1` substring (16-104), close behaviour, slow peer (150-195), oversized input (197-228).
- server tests use InProcChannel (unencoded). cmd/wideboi status_test.go uses a real gob socket; `status --json` uses Go json on MsgLayoutSnapshot (status.go:43-46) — independent of the wire.
- web: input.test.ts asserts `['MsgInput', {...}]` send args incl. base64 Data `'w6nnlYw='`; lifecycle.test.ts:27 feeds raw JSON `MsgPaneClosed`; renderer.test.ts builds Layout/Update/Patch literals; client.test.ts token subprotocol only.
- scripts/attachcheck.py: end-to-end over a real socket, no message parsing.

## 5. Generic consumers

| Location | Unknown type behaviour |
|---|---|
| socket.go gob.Register | encode/decode error, pump stops |
| websocket.go outbound reflection | sends anything |
| websocket.go clientTypes | warn + skip |
| server.go:309-411 handleClientMsg | no default, ignored |
| client.go:139-266 HandleServerMsg | no default, ignored (incl. MsgPaneClosed) |
| status.go:38-41 | error if first msg is not a snapshot |
| wideboi-app.ts:261-283 | no else, ignored |
| colors.ts, wire.go Decode switches | default fallbacks |
| wireTypes / transport wire_test lists | hand-maintained, unchecked if missing |
