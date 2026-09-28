# Distinguish bracketed paste from typed text and macro inputs in MsgInput Implementation Plan

**Goal:** Distinguish clipboard paste events from raw text, macros, and IME inputs in `MsgInput` so that child shells receive bracketed paste markers only for actual clipboard pastes.

**Approach:** Add `bool paste = 4;` to `MsgInput` in protobuf and `Paste bool` to Go `protocol.MsgInput`. On the server, only wrap `m.Data` with bracketed paste markers when `m.Paste && p.grid != nil && p.grid.BracketedPaste()`. Expose `cli.SendPaste` in Go and `sendPasteInput` in TypeScript for clipboard paste, while keeping `SendInput` and `sendTextInput` for unbracketed raw/typed input. Bump protocol to version 18.

**Tech stack:** Go, TypeScript, Protobuf (`buf`), Vitest.

---

## Phase 1: Wire Schema, Protobuf Generation, Codec, and Protocol Version Bump

Add `bool paste = 4;` to `MsgInput` in `wideboi.proto`, regenerate Go and TypeScript protobuf bindings, update `protocol.MsgInput` and `internal/protocol/codec.go`, bump `protocol.Version` from 17 to 18, and update version guard tests.

**Files:**
- Modify: `internal/protocol/wirepb/wideboi.proto` — add `bool paste = 4;` to `MsgInput`
- Regenerate: `internal/protocol/wirepb/wideboi.pb.go` and `web/src/gen/internal/protocol/wirepb/wideboi_pb.ts` via `make proto`
- Modify: `internal/protocol/messages.go` — add `Paste bool` to `MsgInput`
- Modify: `internal/protocol/codec.go` — serialize and deserialize `Paste` in `MsgInput`
- Modify: `internal/protocol/version.go` — bump `Version` to `18`
- Modify: `web/src/version.ts` — bump `PROTOCOL_VERSION = 18;` and `VERSION_PROTOCOL = "wideboi.v18";`
- Modify: `internal/protocol/version_guard_test.go` — add version 18 schema sha256 to `wireSchemaHashes`
- Test: `internal/protocol/wire_test.go`
- Test: `internal/transport/wire_test.go`

**Key changes:**
In `internal/protocol/wirepb/wideboi.proto`:
```protobuf
message MsgInput {
  int32 pane_id = 1;
  KeyData key = 2;
  bytes data = 3;
  bool paste = 4;
}
```

In `internal/protocol/messages.go`:
```go
type MsgInput struct {
	PaneID int
	Key    KeyData
	Data   []byte
	Paste  bool
}
```

In `internal/protocol/codec.go`:
```go
	case MsgInput:
		env.Msg = &wirepb.ClientMessage_Input{Input: &wirepb.MsgInput{PaneId: int32(m.PaneID), Key: encodeKey(m.Key), Data: m.Data, Paste: m.Paste}}
```
and
```go
	case *wirepb.ClientMessage_Input:
		return MsgInput{PaneID: int(m.Input.PaneId), Key: decodeKey(m.Input.Key), Data: m.Input.Data, Paste: m.Input.Paste}, nil
```

In `internal/transport/wire_test.go`:
```go
		protocol.MsgInput{PaneID: 1, Data: []byte("pasted"), Paste: true},
```

**Verification — automated:**
- [x] `make proto` regenerates protobuf bindings cleanly without drift
- [x] `go test -v ./internal/protocol/...` passes (including `TestCodecRoundTripsEveryField` and `TestWireSchemaMatchesProtocolVersion`) — **all 27 tests passed**
- [x] `go test -v ./internal/transport/...` passes (including `TestEveryMessageTypeRoundtrips`) — **all 27 tests passed**

**Verification — manual:**
- [x] Inspect `git diff internal/protocol/wirepb/wideboi.proto` and generated bindings to confirm field number 4 is clean and non-conflicting. — **verified clean bool field 4**

---

## Phase 2: Server-side Bracketed Paste Discrimination

Update `handleInputLocked` in `internal/server/handlers.go` to wrap `m.Data` in bracketed paste markers only when `m.Paste` is true and `p.grid.BracketedPaste()` is true. Raw inputs with `m.Paste == false` are forwarded as unbracketed `RawBytes`.

**Files:**
- Modify: `internal/server/handlers.go` — check `m.Paste` before wrapping bracketed paste markers
- Test: `internal/server/paste_test.go` — test `m.Paste == true` vs `m.Paste == false` when grid bracketed paste is enabled and disabled

**Key changes:**
In `internal/server/handlers.go`:
```go
		if len(m.Data) > 0 {
			data := m.Data
			if m.Paste && p.grid != nil && p.grid.BracketedPaste() {
				data = append([]byte("\x1b[200~"), append(data, []byte("\x1b[201~")...)...)
			}
			p.SendBytes(data)
		} else if !m.Key.IsZero() {
```

In `internal/server/paste_test.go`:
- Extend `TestHandleInputBracketedPaste` to verify:
  1. `MsgInput{PaneID: 2, Data: []byte("echo hi\n"), Paste: false}` into bracketed pane produces raw `"echo hi\n"` without `\x1b[200~` and `\x1b[201~`.
  2. `MsgInput{PaneID: 2, Data: []byte("echo hi\n"), Paste: true}` into bracketed pane produces `"\x1b[200~echo hi\n\x1b[201~"`.
  3. `MsgInput{PaneID: 1, Data: []byte("echo hi\n"), Paste: true}` into unbracketed pane produces raw `"echo hi\n"`.

**Verification — automated:**
- [x] `go test -v -count=1 ./internal/server -run TestHandleInputBracketedPaste` passes — **passed with verified failure before handler update**
- [x] `go test -count=1 ./internal/server/...` passes — **all packages in internal/server passed**

**Verification — manual:**
- [x] Verify that `p.SendBytes` is called with unmodified slice when `m.Paste == false`. — **verified data unchanged when m.Paste is false**

---

## Phase 3: Go Client & Console TUI Paste Routing

Expose `SendPaste` on `client.Client`, ensure `SendInput` sends `Paste: false`, and update `handlePaste` in `cmd/wideboi` to call `SendPaste`.

**Files:**
- Modify: `internal/client/client.go` — add `SendPaste(ctx context.Context, data []byte)` with `Paste: true`
- Modify: `cmd/wideboi/main.go` — `handlePaste` calls `cli.SendPaste` instead of `cli.SendInput`
- Test: `cmd/wideboi/paste_test.go` — assert `handlePaste` dispatches `MsgInput` with `Paste: true`
- Test: `internal/client/client_test.go` (or in existing client test) — verify `SendInput` sends `Paste: false` and `SendPaste` sends `Paste: true`

**Key changes:**
In `internal/client/client.go`:
```go
// SendPaste forwards raw pasted bytes for the focused pane to the server with bracketed paste intent.
func (c *Client) SendPaste(ctx context.Context, data []byte) {
	c.mu.Lock()
	focusedID := c.focusPaneID
	c.pendingReveal[focusedID] = true
	c.revealCursorLocked(focusedID)
	c.mu.Unlock()

	if focusedID > 0 {
		c.transport.SendClient(ctx, protocol.MsgInput{PaneID: focusedID, Data: data, Paste: true})
	}
}
```

In `cmd/wideboi/main.go`:
```go
func (app *wideboiApp) handlePaste(ctx context.Context, ev uv.PasteEvent) {
	app.clearSelection()
	if app.state.SearchActive {
		app.cli.SearchEdit(ctx, []byte(ev.Content))
		return
	}
	app.cli.SendPaste(ctx, []byte(ev.Content))
}
```

In `cmd/wideboi/paste_test.go`:
- Verify `in.Paste == true` when handling `uv.PasteEvent`.

**Verification — automated:**
- [x] `go test -v -count=1 ./cmd/wideboi -run TestHandlePaste` passes — **verified failure before fix and pass after**
- [x] `go test -count=1 ./internal/client/... ./cmd/wideboi/...` passes — **all tests in internal/client and cmd/wideboi passed**

**Verification — manual:**
- [x] Check that `cli.SendInput` remains unchanged and continues to default `Paste: false`. — **verified SendInput sets Paste: false, SendPaste sets Paste: true**

---

## Phase 4: Web Client Input Helpers & Call Sites

Add `sendPasteInput` helper in `web/src/input.ts` sending `paste: true`, while `sendTextInput` sends `paste: false`. Update web client clipboard paste handlers to use `sendPasteInput`, while macro steps, IME, and mobile draft/direct continue using `sendTextInput`.

**Files:**
- Modify: `web/src/input.ts` — add `sendPasteInput`, update `sendTextInput` with `paste: false`
- Modify: `web/src/wideboi-app.ts` — import and call `sendPasteInput` in `paste` event handler and Ctrl/Cmd+Shift+V handler
- Test: `web/src/input.test.ts` — test `sendPasteInput` emits `{ case: 'input', value: { ..., paste: true } }` and `sendTextInput` emits `paste: false`
- Test: `web/src/macros.test.ts` — verify macro text execution emits `MsgInput` with `paste: false`

**Key changes:**
In `web/src/input.ts`:
```typescript
export function sendTextInput(sender: InputSender, paneID: number, text: string): boolean {
  if (!text) return false;
  sender.send({ case: 'input', value: { paneId: paneID, data: new TextEncoder().encode(text), paste: false } });
  return true;
}

export function sendPasteInput(sender: InputSender, paneID: number, text: string): boolean {
  if (!text) return false;
  sender.send({ case: 'input', value: { paneId: paneID, data: new TextEncoder().encode(text), paste: true } });
  return true;
}
```

In `web/src/wideboi-app.ts`:
```typescript
import { sendKeyboardInput, sendPasteInput, sendTextInput, sendWheelInput } from './input';
```
Lines 716 and 752:
```typescript
sendPasteInput(this.client, this.focusedPaneId, text);
```
and
```typescript
if (!sendPasteInput(this.client, this.focusedPaneId, value)) return;
```

**Verification — automated:**
- [x] `cd web && npm test` passes — **all 19 test files / 152 tests passed**
- [x] `cd web && npm run build` passes — **tsc and vite build passed cleanly**

**Verification — manual:**
- [x] Verify `macros.ts`, IME (`compositionend`), and mobile bar call `sendTextInput` and NOT `sendPasteInput`. — **verified call sites retain sendTextInput with paste: false**

---

## Phase 5: Full Verification & Integration Gate

Run full test suite including race checks, exit contracts, smoke tests, and attach checks.

**Verification — automated:**
- [x] `make quick` passes — **all Go and vitest tests passed**
- [x] `make proto-check` passes — **zero drift in proto generation**
- [x] `make check` passes — **all race, smoke (41/41), attachcheck (29/29), browser (50/50), vitest (152/152) passed**
- [x] Four fresh test runs of `go test -count=1 ./internal/server -run TestHandleInputBracketedPaste` pass — **4/4 consecutive clean passes**

**Verification — manual:**
- [x] Inspect `git diff origin/main` to ensure changes are surgical, idiomatic, and adhere to project conventions. — **verified clean, surgical diff**
