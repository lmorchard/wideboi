# Research: OSC 7 and OSC 1337 Pane Metadata

## 1. Terminal Emulator / Parser OSC Processing, Handlers, and Dispatch

- **PTY Input to Emulator**: In `internal/server/pane.go:114-118`, the `pty-reader` goroutine reads bytes from `p.pty.Master.Read` and forwards them to `p.grid.Write(buf[:n])`.
- **Pre-Parser OSC Scanner (`oscScanner`)**: In `internal/server/term/grid.go:339-343` and `internal/server/term/oscfix.go:52-138`, `oscScanner.write` inspects the stream for 7-bit `ESC ] ... ST|BEL` sequences. To circumvent `charmbracelet/x/ansi` treating `0x9C` inside UTF-8 sequences as a string terminator (`oscfix.go:8-14`):
  - For OSC 0 and OSC 2 (titles), if a continuation byte `0x9C` is detected, `oscScanner.finalize` invokes `setTitle` directly (`oscfix.go:165-176`), setting `g.title.Store(&title)` (`grid.go:343`).
  - For non-title OSCs, characters containing `0x9C` are substituted with `\uFFFD` (`oscfix.go:154-158`).
  - Processed bytes are emitted directly to `g.em.Write(b)` (`grid.go:341`).
- **Underlying Emulator**: `vtGrid` wraps `*vt.SafeEmulator` (`github.com/charmbracelet/x/vt`) (`grid.go:140, 216`).
- **OSC 0, 1, 2 Titles**: Handled natively by `x/vt` parser, which fires the `Title` callback registered in `grid.go:227`: `func(s string) { g.title.Store(&s) }`.
- **OSC 133 (Semantic Prompts / Agent Status)**: Registered in `grid.go:235-281` via `g.em.RegisterOscHandler(133, ...)`. Parses payload prefix (`grid.go:245-276`):
  - `"A"`, `"B"` -> `protocol.StatusNeedsInput` (`grid.go:252-257`)
  - `"C"` -> `protocol.StatusWorking` (`grid.go:258-259`)
  - `"D"` / `"D;0"` -> `protocol.StatusDone`; `"D;<code>"` (non-zero) -> `protocol.StatusFailed` (`grid.go:260-268`)
  - Updates `g.status.Store(int32(st))` and latches `g.sawAuthoritativeStatus.Store(true)` (`grid.go:278-279`).
- **OSC 9;4 (Progress Reporting)**: Registered in `grid.go:283-323` via `g.em.RegisterOscHandler(9, ...)`. Validates subcommand `"4"` and state:
  - `"0"` -> `StatusDone`, `"1"` / `"3"` -> `StatusWorking`, `"2"` -> `StatusFailed`, `"4"` -> `StatusNeedsInput` (`grid.go:307-318`).
  - Updates `g.status.Store(int32(st))` and latches `g.sawAuthoritativeStatus.Store(true)` (`grid.go:320-321`).
- **Dispatching to Pane and Server**:
  - `vtGrid` does not push events to `Server`. It writes status and titles to atomic variables: `g.title` (`grid.go:151`), `g.status` (`grid.go:145`), `g.cursorVisible` (`grid.go:141`), `g.mouseModes` (`grid.go:144`), `g.generation` (`grid.go:160`).
  - In `internal/server/server.go:306-322`, `Server.Run` operates a 33ms ticker calling `s.broadcastLayoutIfStatusChanged(ctx)` (`server.go:736-757`), which polls `s.statusGlyphsLocked()` (`server.go:663-706`) and `s.paneTitlesLocked()` (`server.go:708-715`). If any title or status changed, it broadcasts `MsgLayoutSnapshot` via `s.broadcastLayout(ctx)` (`server.go:759-828`).
  - Emulator terminal replies (e.g. CPR) are read from `p.grid.Read` by the `pty-writer` pump goroutine in `pane.go:126-148` and written back to `p.pty.Master`.

## 2. Pane State Storage, Updates, Generation, and Change Notification

- **In `term` (`vtGrid`)** (`internal/server/term/grid.go:139-194`):
  - State held: `em *vt.SafeEmulator`, `cursorVisible atomic.Bool`, `mouseModes atomic.Uint32`, `status atomic.Int32`, `title atomic.Pointer[string]`, `lastWriteTime atomic.Pointer[time.Time]`, `sawAuthoritativeStatus atomic.Bool`, `scrollOffset atomic.Int32`, `generation atomic.Uint64`, `writeResizeMu sync.Mutex`.
  - State updates:
    - `Write(p)` (`grid.go:330-349`): sets `lastWriteTime`; sets `status = StatusWorking` if `!sawAuthoritativeStatus`; passes bytes through `oscScanner`; feeds `em.Write`; increments `generation.Add(1)`.
    - `Resize(cols, rows)` (`grid.go:459-519`): holds `writeResizeMu`, captures cells, resizes `em`, reflows, re-sets cells, increments `generation.Add(1)`.
    - `SetScrollOffset(offset)` (`grid.go:537-548`): updates `scrollOffset`; increments `generation.Add(1)` if offset moved.
    - Callbacks from `SafeEmulator`: DECTCEM visibility (`grid.go:221`), mouse tracking modes DEC 9/1000/1002/1003 (`grid.go:231-232, 407-426`), title (`grid.go:227`), and OSC status (`grid.go:278, 320`) update their atomics directly (callbacks do not bump `generation`).
    - `Status()` (`grid.go:359-370`): falls back to `StatusIdle` if `!sawAuthoritativeStatus` and `time.Since(lastWriteTime) > idleTimeout` (3s).
- **In `SafeEmulator`** (`github.com/charmbracelet/x/vt`):
  - Stores cell matrices (`Lines`), scrollback buffers, cursor position, screen mode flags, and pipe buffers under internal mutex locks.
- **In `Pane`** (`internal/server/pane.go:26-65`):
  - State held: `id`, `pty *ptyx.Pane`, `grid term.Grid`, `cols`, `rows`, `input chan uv.Event` (capacity 256), `dropped atomic.Uint64`, `dead atomic.Bool`, `resizeMu sync.Mutex`, `renderMu sync.RWMutex`.
  - `Resize(cols, rows)` (`pane.go:230-243`): acquires `resizeMu`, sets `p.cols, p.rows`, calls `grid.Resize`, then `pty.Resize`.
  - `UpdateMessage()` (`pane.go:286-338`): acquires `resizeMu` and `renderMu.RLock()`, renders grid to `uv.ScreenBuffer` via `p.Draw()`, builds `protocol.MsgPaneUpdate` with `LineData`, cursor, and mouse tracking state.
  - `Generation()` (`pane.go:341`): queries `p.grid.Generation()`.
- **In `Server`** (`internal/server/server.go:25-91`):
  - State held: `panes map[int]*Pane`, `strip *layout.Strip`, `clientSizes map[transport.Transport]protocol.MsgResize`, `lastStatuses map[int]protocol.PaneStatus`, `lastTitles map[int]string`, `paneGens map[transport.Transport]map[int]uint64`, `paneFrames map[transport.Transport]map[int]protocol.MsgPaneUpdate`.
  - Change Notification & Propagation:
    - In `Server.Run` (`server.go:306-322`), a 33ms ticker runs:
      1. Calls `broadcastLayoutIfStatusChanged(ctx)` (`server.go:736-757`): checks if any pane title or status differs from `lastStatuses` or `lastTitles`. If changed, sends `MsgLayoutSnapshot` to all transports (`server.go:759-815`) and forces a full pane resend via `s.broadcastPaneUpdates(ctx, true)` (`server.go:827`).
      2. If layout did not broadcast, calls `broadcastPaneUpdates(ctx, false)` (`server.go:836-976`). Compares each pane's `p.Generation()` against `s.paneGens[tp][id]`. If `force || !ok || last != gen`:
         - Calls `p.UpdateMessage()` outside locks (`server.go:878`). Sets `update.Generation = gen`.
         - If baseline exists in `s.paneFrames[tp][id]`, generates `MsgPanePatch` via `protocol.BuildPanePatch` (`server.go:908`), otherwise sends full `MsgPaneUpdate`.
         - Verifies `r.pane.Generation() == r.gen` after transmission (`server.go:949`); if equal and accepted, updates `s.paneGens[tp][id] = r.gen` and `s.paneFrames[tp][id] = r.frame` (`server.go:959, 965`).

## 3. Wire Protocol Structure, Patch/Snapshot Serialization, and `protocol.Version`

- **Wire Protocol Structure**:
  - Defined in Protobuf (`internal/protocol/wirepb/wideboi.proto`) and mirrored in Go structs (`internal/protocol/messages.go`):
    - `MsgLayoutSnapshot` (`wirepb.proto:115-119`, `messages.go:252-258`): `Columns []ColumnData` (`pane_id`, `width`, `height`), `PaneStatuses map[int32]PaneStatus`, `PaneTitles map[int32]string`.
    - `MsgPaneUpdate` (`wirepb.proto:74-84`, `messages.go:80-94`): `pane_id`, `generation` (uint64), `cols`, `rows`, `lines []LineData`, `cursor_x`, `cursor_y`, `cursor_visible`, `mouse_tracking`.
    - `MsgPanePatch` (`wirepb.proto:95-107`, `messages.go:105-118`): `pane_id`, `cols`, `rows`, `base_generation`, `generation`, `shift_rows`, `changed_rows []PaneRow`, `cursor_x`, `cursor_y`, `cursor_visible`, `mouse_tracking`.
- **Go Serialization & Deserialization**:
  - Implemented in `internal/protocol/codec.go`:
    - Server messages: `MarshalServer(msg any) ([]byte, error)` (`codec.go:79-120`) converts Go struct types into `wirepb.ServerMessage` oneof envelopes, sanitizing strings with `validUTF8` (`codec.go:174-179`), and calls `proto.Marshal`. `UnmarshalServer(data []byte) (any, error)` (`codec.go:122-168`) unpacks `wirepb.ServerMessage` into Go protocol values.
    - Client messages: `MarshalClient` and `UnmarshalClient` (`codec.go:19-77`) convert client messages into `wirepb.ClientMessage` oneofs.
- **TypeScript Serialization & Deserialization**:
  - Generated code: `web/src/gen/internal/protocol/wirepb/wideboi_pb.ts`.
  - In `web/src/client.ts:65`: incoming WebSocket binary frames are parsed via `fromBinary(ServerMessageSchema, new Uint8Array(event.data as ArrayBuffer))`.
  - In `web/src/client.ts:80`: outgoing client messages are serialized via `toBinary(ClientMessageSchema, create(ClientMessageSchema, { msg }))`.
- **`protocol.Version` Checking**:
  - Defined in `internal/protocol/version.go:18` (`const Version uint32 = 2`).
  - **Unix Domain Socket**:
    - Handshake frame: 16 bytes consisting of `"WIDEBOI\x00"` (8 bytes) + `Version` (4 bytes big-endian) + `PID` (4 bytes big-endian) (`internal/transport/handshake.go:14-23`).
    - Exchanged upon connection in `transport.HandshakeServer` (`handshake.go:53-83`) and `transport.HandshakeClient` (`handshake.go:85-106`). If `peer.Version != protocol.Version`, returns `MismatchError` (`handshake.go:76-78`).
  - **WebSocket**:
    - Server: `internal/server/server.go:1083-1100` searches client subprotocols for `wideboi.v<Version>`. If absent or mismatched, responds with HTTP 400 Bad Request and rejects the upgrade.
    - Browser Client: `web/src/client.ts:32-44` sets `const versionProtocol = "wideboi.v2"`, offers it in the `WebSocket` constructor, and in `ws.onopen` asserts `ws.protocol === versionProtocol`; if mismatched, immediately closes the socket.

## 4. `wideboi status` Query and Display Implementation

- **Entry Point and CLI Flags**:
  - In `cmd/wideboi/main.go:116`: `fs.BoolVar(&opts.jsonOut, "json", false, "output JSON instead of a table (status only)")`.
  - In `cmd/wideboi/main.go:206`: `fatal(runStatus(cfg, opts.jsonOut, os.Stdout))`.
- **Query Flow in `runStatus`** (`cmd/wideboi/status.go:18-64`):
  1. Dials UNIX socket: `net.Dial("unix", cfg.Socket)` (`status.go:19`).
  2. Runs version handshake: `handshakeServer(conn, cfg.Socket)` (`status.go:23`) calling `transport.HandshakeClient(conn)`.
  3. Initializes socket client and pumps: `transport.NewClientSocketConn(conn, 256)` and `cc.RunPumps(ctx)`.
  4. Queries server: `cc.SendClient(ctx, protocol.MsgStatusRequest{})`.
- **Server Response**:
  - In `internal/server/server.go:335-336`, `handleClientMsg` receives `MsgStatusRequest`, sets `needBroadcast = true`, and calls `s.broadcastLayout(ctx)`.
  - `broadcastLayout` transmits `protocol.MsgLayoutSnapshot` populated with `Columns`, `PaneStatuses`, and `PaneTitles`.
- **Display Formatting in `runStatus`**:
  - Waits up to 2s on `cc.ServerSendChan()` (`status.go:37, 61`) and asserts `snap, ok := msg.(protocol.MsgLayoutSnapshot)` (`status.go:41`).
  - **`--json` Output** (`status.go:46-50`): encodes `snap` directly using `json.NewEncoder(w)` with two-space indentation.
  - **Table Output** (`status.go:52-59`):
    - Header: `"PANE ID\tWIDTH\tHEIGHT\tSTATUS\tTITLE\n"`.
    - Iterates `snap.Columns`, resolving status and title.
    - Row format: `fmt.Fprintf(tw, "%d\t%d\t%d\t%s\t%s\n", col.PaneID, col.Width, col.Height, statusName(status), title)`.

## 5. Generic / Catch-All Consumers of Pane Updates and Wire Messages

- **Server**:
  - Connection Reader Pump (`internal/transport/socket.go:121-136`): reads framing, deserializes with `protocol.UnmarshalClient`.
  - Connection Loop Consumer (`internal/server/server.go:218-250`): reads from `tp.ClientSendChan()`, dispatches to `s.handleClientMsg(ctx, tp, msg)`.
  - Message Switch (`internal/server/server.go:326-459`): dispatches `MsgPaneResync`, `MsgStatusRequest`, `MsgAttach`, `MsgResize`, `MsgVerb`, `MsgInput`, `MsgMouse`, `MsgScroll`.
- **Client (Terminal Client)**:
  - Connection Reader Pump (`internal/transport/socket.go:193-207`): deserializes with `protocol.UnmarshalServer`.
  - Presentation Loop (`cmd/wideboi/main.go:722-749`): passes incoming messages to `cli.HandleServerMsg(msg)`.
  - Client Message Switch (`internal/client/client.go:135-267`): dispatches `MsgPaneCreated`, `MsgLayoutSnapshot`, `MsgPaneUpdate`, `MsgPanePatch`.
- **Web Frontend**:
  - WebSocket Reader (`web/src/client.ts:61-71`): decodes binary frames via `fromBinary(ServerMessageSchema, ...)`.
  - Application Message Dispatcher (`web/src/wideboi-app.ts:264-300`): dispatches `layoutSnapshot`, `paneCreated`, `paneUpdate`, `panePatch`, `paneClosed`.
