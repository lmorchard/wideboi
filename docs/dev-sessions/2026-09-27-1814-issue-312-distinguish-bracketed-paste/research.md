# Research: Distinguish bracketed paste from typed text and macro inputs in MsgInput

## 1. Input Path Across Client-Server (`MsgInput`)

### Message Definition
* `internal/protocol/messages.go:244-248`:
  ```go
  type MsgInput struct {
      PaneID int
      Key    KeyData
      Data   []byte
  }
  ```
* `internal/protocol/wire.go:187-189`: `Data` carries raw bytes; `Key` carries key event data; `k.IsZero()` distinguishes between the two.

### Client Creation
* **TUI (`cmd/wideboi`):**
  * Keystrokes: `cmd/wideboi/main.go:1170` calls `cli.SendKey(ctx, uv.KeyEvent(ev))` (`internal/client/client.go:626-636`), converting `uv.KeyEvent` via `protocol.EncodeKey` into `protocol.MsgInput{PaneID: focusedID, Key: ...}`.
  * Pasted content: `cmd/wideboi/main.go:1176-1177` handles `uv.PasteEvent` via `handlePaste` (`cmd/wideboi/main.go:1244-1257`), calling `cli.SendInput(ctx, []byte(ev.Content))` (`internal/client/client.go:639-649`), which creates `protocol.MsgInput{PaneID: focusedID, Data: data}`.
* **Web (`web/src/`):**
  * Keystrokes: `web/src/wideboi-app.ts:765` calls `sendKeyboardInput(this.client, this.focusedPaneId, e)` (`web/src/input.ts:19-35`), dispatching `{ case: 'input', value: { paneId, key: { ... } } }`.
  * Text / Pasted text / IME: `web/src/wideboi-app.ts:716, 752, 760, 1194, 1204` calls `sendTextInput(this.client, this.focusedPaneId, text)` (`web/src/input.ts:37-41`), dispatching `{ case: 'input', value: { paneId, data: new TextEncoder().encode(text) } }`.

### Transmission & Serialization
* **TUI Client:** Sent through `c.transport.SendClient` (`internal/client/client.go:634, 647`). Serialized by `protocol.MarshalClient` (`internal/protocol/codec.go:37-38`) into `wirepb.ClientMessage_Input`.
* **Web Client:** Sent through `WideboiClient.send` (`web/src/client.ts:86-93`), serialized via `@bufbuild/protobuf` `toBinary(ClientMessageSchema, ...)` and sent as binary frame over WebSocket (`web/src/client.ts:92`).

### Server Handling
* `internal/server/handlers.go:164-165`: `s.handleClientMsg` dispatches `protocol.MsgInput` to `s.handleInputLocked` (`internal/server/handlers.go:487-521`).
* During upgrade: `internal/server/handlers.go:119-130` drops all client messages except `MsgInput` targeted at pty panes.
* Dashboard pane: `internal/server/handlers.go:489-498` delegates to `s.dashboard.HandleInput(m)`.
* Scrollback reset: `internal/server/handlers.go:501-509` snaps calling client's `clientScrollOffsets` back to 0 on input.
* Data path: `internal/server/handlers.go:510-515`:
  ```go
  if len(m.Data) > 0 {
      data := m.Data
      if p.grid != nil && p.grid.BracketedPaste() {
          data = append([]byte("\x1b[200~"), append(data, []byte("\x1b[201~")...)...)
      }
      p.SendBytes(data)
  }
  ```
  Unconditionally wraps `m.Data` when `p.grid.BracketedPaste()` is true!
* Key path: `internal/server/handlers.go:516-518` decodes `m.Key` and calls `p.SendKey`.
* Child write: Pane's key-writer goroutine (`internal/server/pane.go:214-236`) drains `p.input`: `p.grid.SendKey(ev)` for keys, `p.Write(ev)` for `RawBytes`.

---

## 2. Bracketed Paste Mode

* **Terminal Emulator (`term.Grid`):**
  * `internal/server/term/grid.go:326-333`: `vtGrid` registers `EnableMode` and `DisableMode` hooks with `vt.NewSafeEmulator`.
  * `internal/server/term/grid.go:648-661`: `trackTerminalMode` intercepts `ansi.ModeBracketedPaste` (DEC 2004) and records state into `g.bracketedPaste.Store(on)`.
  * `internal/server/term/grid.go:85, 665`: Exposed via interface method `BracketedPaste() bool`.
  * `internal/server/term/grid.go:914, 1046-1048`: Captured in `GridSnapshot.BracketedPaste` and restored on server re-exec/upgrade.
* **Server:**
  * `internal/server/handlers.go:512-514`: Wraps all `m.Data` when `BracketedPaste()` is true.
* **TUI Client (`cmd/wideboi`):**
  * Host terminal bracketed paste enabled at `cmd/wideboi/main.go:1068`.
  * `uv.PasteEvent` received at `cmd/wideboi/main.go:1176-1177`, forwarded via `cli.SendInput` (`cmd/wideboi/main.go:1256`).
* **Web Client (`web/src/`):**
  * Clipboard paste handled in `web/src/wideboi-app.ts:712-721` (Ctrl/Cmd+Shift+V) and `web/src/wideboi-app.ts:749-756` (`document.addEventListener('paste')`), calling `sendTextInput`.

---

## 3. Non-Paste Text Inputs using `MsgInput.Data`

* **Macros (`web/src/macros.ts:41-68`):**
  * `executeMacro` iterates steps. `step.text` calls `sendTextInput(client, paneId, step.text)` (`web/src/macros.ts:45`).
  * Example: `{ text: 'git status' }, { key: 'Enter' }` or `{ text: "git status\n" }`.
  * Wrapped in bracketed paste markers, child shells treat newlines as unexecuted text.
* **IME Composition (`web/src/wideboi-app.ts:758-761`):**
  * Listens to `compositionend` event: `sendTextInput(this.client, this.focusedPaneId, (e as CompositionEvent).data)`.
* **Mobile Direct Input & Mobile Draft (`web/src/wideboi-app.ts:1191-1215`):**
  * Direct input: `handleMobileDirectInput` calls `sendTextInput(this.client, this.focusedPaneId, input.value)`.
  * Draft input: `sendMobileDraft` calls `sendTextInput(this.client, this.focusedPaneId, text)` followed by Enter key event.
* **Test Keystrokes (`cmd/wideboi/main_test.go:273`):**
  * `cli.SendInput(ctx, []byte("x"))`.

---

## 4. Protobuf Wire Schema and Versioning

* `internal/protocol/wirepb/wideboi.proto:216-220`:
  ```protobuf
  message MsgInput {
    int32 pane_id = 1;
    KeyData key = 2;
    bytes data = 3;
  }
  ```
* Go Codec (`internal/protocol/codec.go:37-38, 107-108`):
  * `encodeKey` and `decodeKey` map between protobuf and `protocol.MsgInput`.
* TypeScript Codec (`web/src/gen/internal/protocol/wirepb/wideboi_pb.ts:761-787`).
* Version guard:
  * `internal/protocol/version.go:35`: `const Version uint32 = 17`.
  * `web/src/version.ts:3-4`: `export const PROTOCOL_VERSION = 17;` and `export const VERSION_PROTOCOL = "wideboi.v17";`.
  * `internal/protocol/version_guard_test.go:25-30`: `wireSchemaHashes` maps version to sha256 hash of `FileDescriptorProto`. Any `.proto` edit requires:
    1. `make proto` (runs `buf generate`).
    2. Bump `protocol.Version` in `internal/protocol/version.go` (17 -> 18).
    3. Update `web/src/version.ts`.
    4. Update `wireSchemaHashes[18]` in `version_guard_test.go`.

---

## 5. Existing Tests

* `internal/server/paste_test.go:19`: `TestHandleInputBracketedPaste` (tests wrapping of `m.Data` when bracketed paste is enabled).
* `cmd/wideboi/paste_test.go:13`: `TestHandlePaste` (tests `uv.PasteEvent` forwarding).
* `web/src/input.test.ts:26`: tests `sendTextInput`.
* `web/src/macros.test.ts`: tests macro step execution order.
* `internal/protocol/version_guard_test.go`: schema and version consistency.
